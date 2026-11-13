"use client"

// Small pieces every list page wants around its table.
import { useEffect, useState, type ReactNode } from "react"
import { ArrowDown, ArrowUp } from "lucide-react"
import { Button } from "./ui/button"
import { Tooltip, TooltipContent, TooltipTrigger } from "./ui/tooltip"

// Jumps to the bottom from the top and back; hidden on a page too short to need it.
export function ScrollJumpButton() {
  const [atTop, setAtTop] = useState(true)
  const [atBottom, setAtBottom] = useState(false)

  useEffect(() => {
    function onScroll() {
      const scrollY = window.scrollY
      const max = document.documentElement.scrollHeight - window.innerHeight
      setAtTop(scrollY < 200)
      setAtBottom(max - scrollY < 200)
    }
    onScroll()
    window.addEventListener("scroll", onScroll, { passive: true })
    return () => window.removeEventListener("scroll", onScroll)
  }, [])

  if (atTop === atBottom) return null
  return (
    <Button
      variant="outline"
      size="icon"
      className="fixed bottom-6 right-6 size-10 rounded-full shadow-md"
      aria-label={atTop ? "Scroll to bottom" : "Scroll to top"}
      onClick={() => window.scrollTo({ top: atTop ? document.documentElement.scrollHeight : 0, behavior: "smooth" })}
    >
      {atTop ? <ArrowDown className="size-4" /> : <ArrowUp className="size-4" />}
    </Button>
  )
}

// A column header that sorts. `sort` is the API's sort value: "field"
// ascending, "-field" descending.
export function SortHeader({
  text,
  field,
  sort,
  onSort,
  defaultDesc,
}: {
  text: string
  field: string
  sort: string
  onSort: (next: string) => void
  defaultDesc?: boolean
}) {
  const isAsc = sort === field
  const isDesc = sort === "-" + field
  function click() {
    if (isAsc) onSort("-" + field)
    else if (isDesc) onSort(field)
    else onSort(defaultDesc ? "-" + field : field)
  }
  return (
    <button type="button" className="flex items-center gap-1 hover:text-foreground" onClick={click}>
      {text}
      {isAsc && <ArrowUp className="size-3" />}
      {isDesc && <ArrowDown className="size-3" />}
    </button>
  )
}

// An icon-only row action with its name as the tooltip and the accessible label.
export function IconAction({
  label: text,
  onClick,
  disabled,
  danger,
  children,
}: {
  label: string
  onClick: () => void
  disabled?: boolean
  danger?: boolean
  children: ReactNode
}) {
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <Button
          variant="ghost"
          size="icon"
          className={`size-7 ${danger ? "text-destructive hover:text-destructive" : ""}`}
          aria-label={text}
          onClick={onClick}
          disabled={disabled}
        >
          {children}
        </Button>
      </TooltipTrigger>
      <TooltipContent>{text}</TooltipContent>
    </Tooltip>
  )
}

// Checkbox selection plus a keyboard highlight over the rows on screen. Mark
// the highlighted row with data-cursor="true" and it is scrolled into view.
export function useRowSelection<Row extends { id: string }>(rows: Row[]) {
  const [selected, setSelected] = useState<Set<string>>(new Set())
  const [cursor, setCursor] = useState<string | null>(null)
  // The highlighted row, dropped if a filter or search removed it.
  const cursorId = rows.find((r) => r.id === cursor)?.id ?? null

  useEffect(() => {
    document.querySelector('[data-cursor="true"]')?.scrollIntoView({ block: "nearest" })
  }, [cursorId])

  return {
    selected,
    selectedRows: rows.filter((r) => selected.has(r.id)),
    allSelected: rows.length > 0 && selected.size === rows.length,
    toggle(id: string) {
      setSelected((prev) => {
        const next = new Set(prev)
        if (next.has(id)) next.delete(id)
        else next.add(id)
        return next
      })
    },
    toggleAll() {
      setSelected((prev) => (prev.size === rows.length ? new Set() : new Set(rows.map((r) => r.id))))
    },
    clear: () => setSelected(new Set()),
    cursorId,
    setCursor,
    // Moves the highlight by d rows, stopping at either end; from nothing it lands on the first row.
    move(d: number) {
      if (!rows.length) return
      const i = rows.findIndex((r) => r.id === cursorId)
      setCursor(rows[Math.min(rows.length - 1, Math.max(0, i < 0 ? 0 : i + d))].id)
    },
  }
}
