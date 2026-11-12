"use client"

import { useEffect, useRef, useState } from "react"
import { defaultKeymap, history, historyKeymap } from "@codemirror/commands"
import { markdownLanguage } from "@codemirror/lang-markdown"
import type { MarkdownParser } from "@lezer/markdown"
import { codeFolding, foldAll, foldNodeProp, Language, LanguageSupport, foldGutter, foldKeymap, foldService, foldedRanges, HighlightStyle, syntaxHighlighting, unfoldEffect } from "@codemirror/language"
import { EditorState, type Extension } from "@codemirror/state"
import { EditorView, keymap } from "@codemirror/view"
import { tags } from "@lezer/highlight"
import { vim } from "@replit/codemirror-vim"
import { usePref } from "../../lib/use-pref"
import { Button } from "../ui/button"

export type NoteSection = {
  id: string
  title: string
  md: string
  onSave: (md: string) => Promise<unknown>
  // Renaming the # heading renames it; absent, the title is fixed.
  onRename?: (title: string) => Promise<unknown>
  // Deleting the # heading deletes it; absent, it cannot be deleted.
  onDelete?: () => Promise<unknown>
}

type Part = { title: string; body: string }

// The whole document as one file: each part under its own # heading.
export function compose(sections: NoteSection[]) {
  return sections.map((s) => `# ${s.title}\n${s.md.trim() ? `\n${s.md.trim()}\n` : ""}`).join("\n")
}

// Splits the file at its # headings. Text before the first heading belongs
// to the first part.
function split(text: string): Part[] {
  const out: { title: string; body: string[] }[] = []
  const before: string[] = []
  let fence = false
  for (const line of text.split("\n")) {
    if (/^\s*(```|~~~)/.test(line)) fence = !fence
    const h = !fence && line.match(/^#\s+(.*?)\s*$/)
    if (h) out.push({ title: h[1], body: [] })
    else if (out.length) out[out.length - 1].body.push(line)
    else before.push(line)
  }
  if (out.length) out[0].body.unshift(...before)
  return out.map((p) => ({ title: p.title, body: p.body.join("\n").trim() }))
}

// What saving the file means for each part. The same number of headings
// lines up by position, so renames carry; otherwise headings line up by
// name, a part whose heading is gone is deleted (its text has already moved
// under the heading above it), and a new heading is a new part.
function plan(sections: NoteSection[], parts: Part[]) {
  if (parts.length === sections.length) return { pairs: sections.map((s, i) => ({ s, p: parts[i] })), gone: [] as NoteSection[], added: [] as Part[] }
  const used = new Set<number>()
  const pairs: { s: NoteSection; p: Part }[] = []
  const gone: NoteSection[] = []
  for (const s of sections) {
    const i = parts.findIndex((p, k) => !used.has(k) && p.title === s.title)
    if (i < 0) gone.push(s)
    else {
      used.add(i)
      pairs.push({ s, p: parts[i] })
    }
  }
  return { pairs, gone, added: parts.filter((_, k) => !used.has(k)) }
}

// Markdown with none of its own folds (every heading, list and paragraph),
// so only the # parts below fold.
const md = new LanguageSupport(new Language(markdownLanguage.data, (markdownLanguage.parser as MarkdownParser).configure({ props: [foldNodeProp.add(() => () => null)] }), [], "markdown"))

// Only a # heading folds: one fold per part, down to the next # heading.
const partFolds = foldService.of((state, from) => {
  const line = state.doc.lineAt(from)
  if (!/^#\s/.test(line.text)) return null
  let end = line
  for (let n = line.number + 1; n <= state.doc.lines; n++) {
    const next = state.doc.line(n)
    if (/^#\s/.test(next.text)) break
    end = next
  }
  // Trailing blank lines stay visible as the gap before the next heading.
  while (end.number > line.number && !end.text.trim()) end = state.doc.line(end.number - 1)
  return end.number > line.number ? { from: line.to, to: end.to } : null
})

const highlight = HighlightStyle.define([
  { tag: tags.heading, fontWeight: "600" },
  { tag: tags.processingInstruction, color: "var(--muted-foreground)", opacity: "0.6" },
  { tag: tags.list, color: "var(--muted-foreground)" },
  { tag: tags.emphasis, fontStyle: "italic" },
  { tag: tags.strong, fontWeight: "600" },
  { tag: tags.monospace, backgroundColor: "var(--muted)", borderRadius: "3px" },
])

const theme = EditorView.theme({
  "&": { fontSize: "13px", backgroundColor: "transparent", color: "var(--foreground)" },
  "&.cm-focused": { outline: "none" },
  ".cm-content": { fontFamily: "var(--font-mono, ui-monospace, monospace)", lineHeight: "1.5rem", padding: "8px 0", caretColor: "var(--foreground)" },
  ".cm-gutters": { backgroundColor: "transparent", border: "none", color: "var(--muted-foreground)" },
  ".cm-foldGutter .cm-gutterElement": { width: "1.25rem", textAlign: "center", cursor: "pointer", fontFamily: "var(--font-mono, ui-monospace, monospace)" },
  ".cm-foldPlaceholder": { background: "transparent", border: "none", color: "var(--muted-foreground)", padding: "0 4px", margin: "0", cursor: "pointer" },
  ".cm-activeLine": { backgroundColor: "color-mix(in oklab, var(--muted) 60%, transparent)" },
  ".cm-selectionBackground, &.cm-focused .cm-selectionBackground": { backgroundColor: "color-mix(in oklab, var(--foreground) 15%, transparent)" },
  ".cm-line": { padding: "0 12px 0 4px" },
})

// Notes in an editor. With several sections it is the whole file: each part
// under a # heading, folded behind ---> until the cursor reaches it, saved
// and folded again when you click out. single is one file as it is.
// zc, zo and za fold with Vim keys on (Settings, Appearance).
export function NotesEditor({ sections, writable, single = false, onCreate }: { sections: NoteSection[]; writable: boolean; single?: boolean; onCreate?: (title: string, body: string) => Promise<unknown> }) {
  const host = useRef<HTMLDivElement>(null)
  const view = useRef<EditorView | null>(null)
  const latest = useRef({ sections, onCreate })
  const [error, setError] = useState("")
  const [confirm, setConfirm] = useState<{ names: string[]; apply: () => Promise<unknown> } | null>(null)
  const text = single ? (sections[0]?.md ?? "") : compose(sections)
  const vimOn = usePref("vimKeys") === "on"

  useEffect(() => {
    latest.current = { sections, onCreate }
  })

  useEffect(() => {
    if (!host.current) return
    const save = async (v: EditorView) => {
      const { sections: current, onCreate: create } = latest.current
      const doc = v.state.doc.toString()
      if (single) {
        const s = current[0]
        if (s && doc.trim() !== s.md.trim()) await s.onSave(doc.trim())
        return
      }
      const { pairs, gone, added } = plan(current, split(doc))
      const stuck = gone.filter((s) => !s.onDelete)
      if (stuck.length) {
        setError(`${stuck.map((s) => s.title).join(", ")} cannot be deleted; put its # heading back.`)
        return
      }
      setError("")
      const apply = () =>
        Promise.all([
          ...pairs.map(({ s, p }) => {
            const jobs: Promise<unknown>[] = []
            if (p.body !== s.md.trim()) jobs.push(s.onSave(p.body))
            if (s.onRename && p.title && p.title !== s.title) jobs.push(s.onRename(p.title))
            return Promise.all(jobs)
          }),
          ...gone.map((s) => s.onDelete?.()),
          ...added.map((p) => create?.(p.title, p.body)),
        ])
      // Deleting by removing a heading is easy to do by accident: ask once.
      if (gone.length) setConfirm({ names: gone.map((s) => s.title), apply })
      else await apply()
    }
    // Opens the fold that starts on the cursor's line.
    const openAtCursor = EditorView.updateListener.of((u) => {
      if (!u.selectionSet) return
      const line = u.state.doc.lineAt(u.state.selection.main.head)
      const effects: ReturnType<typeof unfoldEffect.of>[] = []
      foldedRanges(u.state).between(line.to, line.to + 1, (from, to) => {
        if (from === line.to) effects.push(unfoldEffect.of({ from, to }))
      })
      if (effects.length) u.view.dispatch({ effects })
    })
    const handlers = EditorView.domEventHandlers({
      blur: (_e, v) => {
        void save(v)
        if (!single) foldAll(v)
      },
      keydown: (e, v) => {
        if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) {
          e.preventDefault()
          void save(v)
          return true
        }
        return false
      },
    })
    const extensions: Extension[] = [
      vimOn ? vim() : [],
      history(),
      md,
      single ? [] : [partFolds, codeFolding({ placeholderText: "--->" }), foldGutter({ openText: "v", closedText: ">" }), keymap.of(foldKeymap), openAtCursor],
      syntaxHighlighting(highlight),
      keymap.of([...defaultKeymap, ...historyKeymap]),
      EditorView.lineWrapping,
      EditorState.readOnly.of(!writable),
      EditorView.contentAttributes.of({ "aria-label": "Notes", spellcheck: "true" }),
      handlers,
      theme,
    ]
    const v = new EditorView({ parent: host.current, state: EditorState.create({ doc: text, extensions }) })
    view.current = v
    if (!single) foldAll(v)
    return () => {
      v.destroy()
      view.current = null
    }
    // The editor is made once; text changes arrive through the effect below.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [writable, single, vimOn])

  // Saves and agent edits land when you are not typing in it.
  useEffect(() => {
    const v = view.current
    if (!v || v.hasFocus || confirm || v.state.doc.toString() === text) return
    v.dispatch({ changes: { from: 0, to: v.state.doc.length, insert: text } })
    if (!single) foldAll(v)
  }, [text, single, confirm])

  return (
    <div className="flex flex-col gap-1">
      <div ref={host} className="overflow-hidden rounded-lg border bg-muted/30 focus-within:ring-2 focus-within:ring-ring" />
      {confirm ? (
        <div className="flex flex-wrap items-center gap-2 rounded-md border border-destructive/40 px-3 py-2 text-xs">
          <span className="flex-1">
            Delete {confirm.names.join(", ")}? Its text stays, under the heading above.
          </span>
          <Button
            size="xs"
            variant="ghost"
            onClick={() => {
              setConfirm(null)
              const v = view.current
              if (v) v.dispatch({ changes: { from: 0, to: v.state.doc.length, insert: text } })
            }}
          >
            Keep
          </Button>
          <Button
            size="xs"
            variant="destructive"
            onClick={() => {
              const apply = confirm.apply
              setConfirm(null)
              void apply()
            }}
          >
            Delete
          </Button>
        </div>
      ) : (
        <p className={error ? "text-xs text-destructive" : "text-xs text-muted-foreground"}>
          {error ||
            (single
              ? "Click out or ⌘↵ saves · a heading ending in ? is a question"
              : "Move onto a ---> line to open it · click out or ⌘↵ saves · a new # heading adds a file, removing one deletes it")}
        </p>
      )}
    </div>
  )
}
