"use client"

import { Fragment, useEffect, useState, type ReactNode } from "react"
import { getPref } from "../../lib/settings"
import { cn } from "../../lib/utils"

// Where text reveal runs: the notes view, or anywhere text opens.
export type RevealScope = "notes" | "other"

// The decode effect: on only with Motion full, no reduced-motion request from
// the system, and Text reveal set to this scope (Notes) or Everywhere.
export function revealEnabled(scope: RevealScope) {
  if (typeof window === "undefined") return false
  const fx = getPref("textFx")
  if (getPref("motion") === "reduced" || fx === "off" || (fx === "notes" && scope !== "notes")) return false
  return !window.matchMedia("(prefers-reduced-motion: reduce)").matches
}

const GLYPHS = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789#*+=-/<>"

// Text that settles from random glyphs, left to right, in about 350 ms.
export function Decode({ text, active, scope = "other" }: { text: string; active: boolean; scope?: RevealScope }) {
  const [shown, setShown] = useState(text)
  useEffect(() => {
    if (!active || !revealEnabled(scope) || text.length > 400) {
      // eslint-disable-next-line react-hooks/set-state-in-effect
      setShown(text)
      return
    }
    const start = performance.now()
    let frame = 0
    const tick = (now: number) => {
      const done = Math.min(1, (now - start) / 350)
      const cut = Math.floor(text.length * done)
      setShown(text.slice(0, cut) + [...text.slice(cut)].map((c) => (c === " " || c === "\n" ? c : GLYPHS[(Math.random() * GLYPHS.length) | 0])).join(""))
      if (done < 1) frame = requestAnimationFrame(tick)
    }
    frame = requestAnimationFrame(tick)
    return () => cancelAnimationFrame(frame)
  }, [text, active, scope])
  return <>{shown}</>
}

// `code` and **bold**, the only inline marks the notes use.
function inline(text: string, active: boolean, scope: RevealScope): ReactNode {
  const parts = text.split(/(`[^`]+`|\*\*[^*]+\*\*)/g)
  return parts.map((p, i) =>
    p.startsWith("`") && p.endsWith("`") && p.length > 1 ? (
      <code key={i} className="rounded bg-muted px-1 font-mono text-[0.85em]">
        {p.slice(1, -1)}
      </code>
    ) : p.startsWith("**") && p.endsWith("**") && p.length > 3 ? (
      <strong key={i}>
        <Decode text={p.slice(2, -2)} active={active} scope={scope} />
      </strong>
    ) : (
      <Fragment key={i}>
        <Decode text={p} active={active} scope={scope} />
      </Fragment>
    ),
  )
}

type Block =
  | { kind: "heading"; level: number; text: string }
  | { kind: "question"; text: string }
  | { kind: "para"; text: string }
  | { kind: "list"; items: { text: string; task?: boolean; done?: boolean }[] }
  | { kind: "code"; text: string }

export function parseBlocks(md: string): Block[] {
  const out: Block[] = []
  const lines = md.split("\n")
  let para: string[] = []
  const flush = () => {
    if (para.length) out.push({ kind: "para", text: para.join(" ").trim() })
    para = []
  }
  for (let i = 0; i < lines.length; i++) {
    const line = lines[i]
    if (/^\s*(```|~~~)/.test(line)) {
      flush()
      const body: string[] = []
      for (i++; i < lines.length && !/^\s*(```|~~~)/.test(lines[i]); i++) body.push(lines[i])
      out.push({ kind: "code", text: body.join("\n") })
      continue
    }
    const h = line.match(/^\s*(#{1,6})\s+(.*?)\s*#*\s*$/)
    if (h) {
      flush()
      out.push(h[2].endsWith("?") ? { kind: "question", text: h[2] } : { kind: "heading", level: h[1].length, text: h[2] })
      continue
    }
    const li = line.match(/^\s*[-*]\s+(\[( |x)\]\s+)?(.*)$/)
    if (li) {
      flush()
      const item = { text: li[3], task: !!li[1], done: li[2] === "x" }
      const last = out[out.length - 1]
      if (last?.kind === "list") last.items.push(item)
      else out.push({ kind: "list", items: [item] })
      continue
    }
    if (!line.trim()) flush()
    else para.push(line.trim())
  }
  flush()
  return out
}

// A note rendered for reading. A heading that ends in ? reads as a question.
export function Markdown({ md, active = false, scope = "other", className }: { md: string; active?: boolean; scope?: RevealScope; className?: string }) {
  const blocks = parseBlocks(md)
  if (!blocks.length) return <p className={cn("text-sm text-muted-foreground italic", className)}>Nothing written yet.</p>
  return (
    <div className={cn("flex flex-col gap-2 text-sm leading-relaxed", className)}>
      {blocks.map((b, i) => {
        switch (b.kind) {
          case "heading":
            return (
              <div key={i} className={cn("pt-1 font-medium", b.level <= 2 ? "text-[13px] tracking-wide text-muted-foreground uppercase" : "")}>
                {inline(b.text, active, scope)}
              </div>
            )
          case "question":
            return (
              <div key={i} className="mt-1 border-l-2 border-foreground pl-3 font-medium">
                {inline(b.text, active, scope)}
              </div>
            )
          case "list":
            return (
              <ul key={i} className="flex flex-col gap-1 pl-1">
                {b.items.map((it, j) => (
                  <li key={j} className={cn("flex items-baseline gap-2", it.done && "text-muted-foreground line-through")}>
                    <span aria-hidden className="font-mono text-xs text-muted-foreground">
                      {it.task ? (it.done ? "[x]" : "[ ]") : "·"}
                    </span>
                    <span>{inline(it.text, active, scope)}</span>
                  </li>
                ))}
              </ul>
            )
          case "code":
            return (
              <pre key={i} className="overflow-x-auto rounded-md bg-muted px-3 py-2 font-mono text-[12.5px]">
                {b.text}
              </pre>
            )
          default:
            return (
              <p key={i} className="text-muted-foreground">
                {inline(b.text, active, scope)}
              </p>
            )
        }
      })}
    </div>
  )
}

// A round's notes split at its top-level (##) headings, for the notes view's
// folds. Text before the first heading is its own unnamed section.
export function sections(md: string): { title: string; body: string }[] {
  const out: { title: string; body: string[] }[] = []
  let fence = false
  for (const line of md.split("\n")) {
    if (/^\s*(```|~~~)/.test(line)) fence = !fence
    const h = !fence && line.match(/^\s*##\s+(.*?)\s*#*\s*$/)
    if (h) out.push({ title: h[1], body: [] })
    else {
      if (!out.length) out.push({ title: "", body: [] })
      out[out.length - 1].body.push(line)
    }
  }
  return out.map((s) => ({ title: s.title, body: s.body.join("\n").trim() })).filter((s) => s.title || s.body)
}
