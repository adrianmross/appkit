"use client"

import { useEffect, useMemo, useRef, useState, type KeyboardEvent } from "react"
import { usePathname, useRouter, useSearchParams } from "next/navigation"
// @ts-ignore command-score ships no types, and the app compiling this source has no declaration for it
import untypedScore from "command-score"
import { Search } from "lucide-react"
import { matchesQualifiers, parseSearch, tokenAt, tokenize, type Qualifier, type SearchToken } from "../lib/search"
import { getPref } from "../lib/settings"
import { cn } from "../lib/utils"
import { Input } from "./ui/input"

const commandScore = untypedScore as (target: string, query: string) => number

type Item = { id: string; group: string; text: string; hint?: string; apply?: string; href?: string; count?: number }

const KIND: Record<SearchToken["kind"], string> = {
  space: "",
  free: "",
  key: "font-medium text-sky-600 dark:text-sky-400",
  op: "text-amber-600 dark:text-amber-400",
  value: "text-emerald-700 dark:text-emerald-400",
  unknown: "text-muted-foreground",
}
// Fixed so the typed text and its highlighted copy line up exactly.
const TEXT = { fontSize: "0.9375rem", lineHeight: "1.5rem" }

// What the box searches. `load` runs each time the box takes focus; the rows
// feed both the suggestions and the counts beside each qualifier value.
export type SearchSource<Row> = {
  /** The list page: typing filters it through ?q=, from anywhere in the app. */
  path: string
  /** Heading over the suggested rows, and the box's accessible name: "Listings". */
  label: string
  qualifiers: Qualifier<Row>[]
  load: () => Promise<Row[]>
  /** What free text is scored against. */
  text: (row: Row) => string
  /** How a suggested row reads, and where picking it goes. */
  item: (row: Row) => { id: string; text: string; hint?: string; href: string }
}

export function SearchBox<Row>({ source, placeholder, onBlurEmpty }: { source: SearchSource<Row>; placeholder: string; onBlurEmpty?: () => void }) {
  const pathname = usePathname()
  const router = useRouter()
  const params = useSearchParams()
  const onList = pathname === source.path
  const defs = source.qualifiers
  const keys = useMemo(() => defs.map((d) => d.key), [defs])
  const [value, setValue] = useState(onList ? params.get("q") || "" : "")
  const [caret, setCaret] = useState(0)
  const [scroll, setScroll] = useState(0)
  const [open, setOpen] = useState(false)
  const [active, setActive] = useState(-1)
  const [rows, setRows] = useState<Row[]>([])
  const input = useRef<HTMLInputElement>(null)

  useEffect(() => {
    // Syncs the box with the URL's ?q= when it changes elsewhere.
    // eslint-disable-next-line react-hooks/set-state-in-effect
    if (onList) setValue(params.get("q") || "")
  }, [onList, params])

  function load() {
    source.load().then(setRows, () => {})
  }

  function commit(next: string) {
    setValue(next)
    window.clearTimeout((window as unknown as { __search?: number }).__search)
    ;(window as unknown as { __search?: number }).__search = window.setTimeout(() => {
      const merged = new URLSearchParams(onList ? params.toString() : undefined)
      if (next) merged.set("q", next)
      else merged.delete("q")
      router[onList ? "replace" : "push"](`${source.path}?${merged.toString()}`)
    }, 200)
  }

  const items = useMemo(() => {
    if (!open) return []
    const syntax = getPref("searchSyntax") !== "off"
    const tok = tokenAt(value, caret)
    const out: Item[] = []
    const m = syntax ? tok.text.match(/^([a-zA-Z_]+):(.*)$/) : null
    const def = m && defs.find((d) => d.key === m[1].toLowerCase())
    if (m && def) {
      // Count against the rows the other filters already keep, so a choice
      // that would leave nothing is not offered.
      const others = parseSearch(value.slice(0, tok.start) + " " + value.slice(tok.end), keys).qualifiers
      const pool = others.length ? rows.filter((r) => matchesQualifiers(r, others, defs)) : rows
      out.push(
        ...(def.values?.(m[2], pool) ?? [])
          .map(({ value: v, hint }): Item => {
            const apply = `${def.key}:${v}`
            return { id: def.key + v, group: def.key, text: v, hint, apply, count: pool.filter((r) => matchesQualifiers(r, parseSearch(apply, keys).qualifiers, defs)).length }
          })
          .filter((item) => item.count! > 0)
          .sort((a, b) => b.count! - a.count!)
          .slice(0, 8),
      )
    } else {
      if (syntax && (tok.text || !value)) {
        out.push(
          ...defs
            .filter((d) => d.key.startsWith(tok.text.toLowerCase()) && d.key !== tok.text.toLowerCase())
            .map((d): Item => ({ id: "q" + d.key, group: "Filters", text: d.key + ":", hint: d.hint, apply: d.key + ":" })),
        )
      }
      const { free, qualifiers } = parseSearch(value, keys)
      // Suggest only rows the current filters would keep.
      const pool = qualifiers.length ? rows.filter((r) => matchesQualifiers(r, qualifiers, defs)) : rows
      const ranked = free
        ? pool.map((r) => ({ r, s: commandScore(source.text(r), free) })).filter((x) => x.s > 0).sort((x, y) => y.s - x.s).map((x) => x.r)
        : pool
      out.push(
        ...ranked.slice(0, 5).map((r): Item => {
          const it = source.item(r)
          return { id: "r" + it.id, group: source.label, text: it.text, hint: it.hint, href: it.href }
        }),
      )
    }
    return out
  }, [open, value, caret, rows, defs, keys, source])

  function pick(item: Item) {
    if (item.href) {
      setOpen(false)
      router.push(item.href)
      return
    }
    const tok = tokenAt(value, caret)
    const complete = !item.apply!.endsWith(":")
    const next = value.slice(0, tok.start) + item.apply + (complete ? " " : "") + value.slice(tok.end).replace(/^\s+/, "")
    const pos = tok.start + item.apply!.length + (complete ? 1 : 0)
    commit(next)
    setCaret(pos)
    setActive(-1)
    requestAnimationFrame(() => input.current?.setSelectionRange(pos, pos))
  }

  function onKeyDown(e: KeyboardEvent<HTMLInputElement>) {
    const down = e.key === "ArrowDown" || (e.ctrlKey && e.key === "n")
    const up = e.key === "ArrowUp" || (e.ctrlKey && e.key === "p")
    if (e.key === "Escape") {
      if (open && items.length) setOpen(false)
      else e.currentTarget.blur()
    } else if ((down || up) && items.length) {
      e.preventDefault()
      setOpen(true)
      setActive((i) => (down ? (i + 1) % items.length : (i <= 0 ? items.length : i) - 1))
    } else if (e.key === "Enter" && active >= 0 && items[active]) {
      e.preventDefault()
      pick(items[active])
    } else if (e.key === "Tab" && !e.shiftKey && items.length && items[active >= 0 ? active : 0].apply) {
      e.preventDefault()
      pick(items[active >= 0 ? active : 0])
    }
  }

  const sync = (el: HTMLInputElement) => {
    setCaret(el.selectionStart ?? el.value.length)
    setScroll(el.scrollLeft)
  }
  let lastGroup = ""

  return (
    <div className="relative w-full">
      <Search className="pointer-events-none absolute top-1/2 left-3 z-10 size-4 -translate-y-1/2 text-muted-foreground" />
      <div aria-hidden className="pointer-events-none absolute inset-0 flex items-center overflow-hidden pl-9 pr-3" style={TEXT}>
        <span className="whitespace-pre" style={{ transform: `translateX(${-scroll}px)` }}>
          {tokenize(value, keys).map((t, i) => (
            <span key={i} className={KIND[t.kind]}>
              {t.text}
            </span>
          ))}
        </span>
      </div>
      <Input
        ref={input}
        data-search="global"
        role="combobox"
        aria-expanded={open && items.length > 0}
        aria-controls="search-suggestions"
        aria-label={`Search ${source.label.toLowerCase()}`}
        placeholder={placeholder}
        autoComplete="off"
        spellCheck={false}
        autoFocus={!!onBlurEmpty}
        className="h-10 pl-9 text-transparent caret-foreground"
        style={TEXT}
        value={value}
        onChange={(e) => {
          commit(e.target.value)
          sync(e.target)
          setOpen(true)
          setActive(-1)
        }}
        onFocus={(e) => {
          load()
          sync(e.currentTarget)
          setOpen(true)
        }}
        onBlur={(e) => {
          setOpen(false)
          if (!e.currentTarget.value) onBlurEmpty?.()
        }}
        onSelect={(e) => sync(e.currentTarget)}
        onScroll={(e) => setScroll(e.currentTarget.scrollLeft)}
        onKeyUp={(e) => sync(e.currentTarget)}
        onClick={() => setOpen(true)}
        onKeyDown={onKeyDown}
      />
      {open && items.length > 0 && (
        <div
          id="search-suggestions"
          role="listbox"
          className="absolute top-full right-0 left-0 z-50 mt-1 max-h-80 overflow-y-auto rounded-lg border bg-popover p-1 text-popover-foreground shadow-md"
        >
          {items.map((item, i) => {
            const header = item.group !== lastGroup ? ((lastGroup = item.group), item.group) : null
            return (
              <div key={item.id}>
                {header && (
                  <div className="px-2 pt-1.5 pb-1 text-xs font-medium text-muted-foreground first-letter:uppercase">
                    {header === "Filters" || header === source.label ? header : `${header} values`}
                  </div>
                )}
                <div
                  role="option"
                  aria-selected={i === active}
                  // mouseDown, not click: keep focus in the input.
                  onMouseDown={(e) => {
                    e.preventDefault()
                    pick(item)
                  }}
                  onMouseEnter={() => setActive(i)}
                  className={cn("flex cursor-pointer items-baseline justify-between gap-3 rounded-md px-2 py-1.5 text-sm", i === active && "bg-muted")}
                >
                  <span className={cn("truncate", item.apply && "font-mono text-xs")}>{item.text}</span>
                  <span className="flex shrink-0 items-baseline gap-2">
                    {item.hint && <span className="truncate text-xs text-muted-foreground">{item.hint}</span>}
                    {item.count != null && <span className="min-w-5 text-right text-xs font-medium tabular-nums">{item.count}</span>}
                  </span>
                </div>
              </div>
            )
          })}
          <div className="border-t px-2 pt-1.5 pb-1 text-[11px] text-muted-foreground">↑↓ move · Tab or Enter select · Esc close</div>
        </div>
      )}
    </div>
  )
}
