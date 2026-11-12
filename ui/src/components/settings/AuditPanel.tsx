"use client"

import { Fragment, useEffect, useRef, useState } from "react"
import { Check, ChevronRight, Copy, Search } from "lucide-react"
import { api } from "../../lib/api-client"
import { brand } from "../../lib/brand"
import type { Audit, Page } from "../../lib/types"
import { cn } from "../../lib/utils"
import { Button } from "../ui/button"
import { InputGroup, InputGroupAddon, InputGroupInput } from "../ui/input-group"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "../ui/select"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "../ui/table"

const RANGES: [string, string, number][] = [
  ["all", "All time", 0],
  ["1h", "Last hour", 3600e3],
  ["24h", "Last 24 hours", 86400e3],
  ["7d", "Last 7 days", 7 * 86400e3],
]

function useCopy() {
  const [copied, setCopied] = useState("")
  return [
    copied,
    async (key: string, text: string) => {
      await navigator.clipboard.writeText(text)
      setCopied(key)
      setTimeout(() => setCopied((c) => (c === key ? "" : c)), 1500)
    },
  ] as const
}

const shellQuote = (s: string) => (/^[\w./:@-]+$/.test(s) ? s : `'${s.replace(/'/g, `'\\''`)}'`)

export function AuditPanel() {
  const [entries, setEntries] = useState<Audit[] | null>(null)
  const [total, setTotal] = useState(0)
  const [cursor, setCursor] = useState<string | null>(null)
  const [text, setText] = useState("")
  const [q, setQ] = useState("")
  const [range, setRange] = useState("all")
  const [since, setSince] = useState("")
  const [open, setOpen] = useState("")
  const [copied, copy] = useCopy()
  const seq = useRef(0)

  // Debounce typing into the server query.
  useEffect(() => {
    const t = window.setTimeout(() => setQ(text.trim()), 250)
    return () => window.clearTimeout(t)
  }, [text])

  // The range is resolved when picked, not on every render, so the query is stable.
  function pickRange(next: string) {
    setRange(next)
    const ms = RANGES.find((r) => r[0] === next)?.[2] || 0
    setSince(ms ? new Date(Date.now() - ms).toISOString() : "")
  }

  function params(after?: string) {
    const p = new URLSearchParams({ limit: "100" })
    if (q) p.set("q", q)
    if (since) p.set("since", since)
    if (after) p.set("cursor", after)
    return p
  }

  async function load(after?: string) {
    const id = ++seq.current
    try {
      const page = await api<Page<Audit>>("/v1/audit?" + params(after))
      if (id !== seq.current) return
      setEntries((prev) => (after ? [...(prev || []), ...(page.data || [])] : page.data || []))
      setTotal(page.page?.total ?? 0)
      setCursor(page.page?.next_cursor || null)
    } catch {
      if (id === seq.current) setEntries([])
    }
  }

  useEffect(() => {
    // Refetch when the search or time range changes.
    // eslint-disable-next-line react-hooks/set-state-in-effect, react-hooks/exhaustive-deps
    void load()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [q, since])

  const command = [`${brand().cli} audit list`, q && `--q ${shellQuote(q)}`, since && `--since ${since}`, "--limit 100"]
    .filter(Boolean)
    .join(" ")

  const forAgent = () =>
    [
      `# ${brand().shortName} audit log (${entries?.length ?? 0} of ${total} entries, newest first)`,
      `Filters: ${[q && `q="${q}"`, since && `since=${since}`].filter(Boolean).join(", ") || "none"}`,
      `Reproduce: ${command}`,
      `Trace one record end to end: ${brand().cli} audit list --entity-id <entity_id> --sort created_at`,
      "Each entry is who did what, when. Only successful changes are recorded. A request that was rejected leaves no entry: look for the missing step, and check the server log by request_id.",
      "",
      "```json",
      JSON.stringify(entries ?? [], null, 2),
      "```",
    ].join("\n")

  const filtered = !!(q || since)

  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap items-center gap-2">
        <InputGroup className="w-auto min-w-56 flex-1">
          <InputGroupAddon>
            <Search />
          </InputGroupAddon>
          <InputGroupInput
            data-search="audit"
            aria-label="Search audit log"
            placeholder="Search actor, action, entity, summary, or details"
            value={text}
            onChange={(e) => setText(e.target.value)}
            onKeyDown={(e) => {
              if (e.key !== "Escape") return
              if (text) setText("")
              else e.currentTarget.blur()
            }}
          />
        </InputGroup>
        <Select value={range} onValueChange={pickRange}>
          <SelectTrigger className="w-40" aria-label="Time range">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {RANGES.map(([v, l]) => (
              <SelectItem key={v} value={v}>
                {l}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </div>
      <div className="flex flex-wrap items-center gap-2">
        <Button size="sm" variant="outline" disabled={!entries?.length} onClick={() => copy("agent", forAgent())}>
          {copied === "agent" ? <Check /> : <Copy />} Copy for an agent
        </Button>
        <Button size="sm" variant="outline" onClick={() => copy("cmd", command)}>
          {copied === "cmd" ? <Check /> : <Copy />} Copy CLI command
        </Button>
        <span className="ml-auto text-xs text-muted-foreground">
          {entries ? `${entries.length} of ${total}${filtered ? " matching" : ""}` : "Loading…"}
        </span>
      </div>
      <div className="rounded-md border">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead className="w-6" />
              <TableHead>When</TableHead>
              <TableHead>Actor</TableHead>
              <TableHead>Action</TableHead>
              <TableHead>Entity</TableHead>
              <TableHead className="w-full">Summary</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {entries?.map((e) => (
              <Fragment key={e.id}>
                <TableRow className="cursor-pointer" onClick={() => setOpen(open === e.id ? "" : e.id)}>
                  <TableCell>
                    <ChevronRight className={cn("size-4 text-muted-foreground transition-transform", open === e.id && "rotate-90")} />
                  </TableCell>
                  <TableCell className="whitespace-nowrap text-muted-foreground">{new Date(e.created_at).toLocaleString()}</TableCell>
                  <TableCell className="whitespace-nowrap">{e.actor_name || "System"}</TableCell>
                  <TableCell className="whitespace-nowrap font-mono text-xs">{e.action}</TableCell>
                  <TableCell className="whitespace-nowrap">{e.entity_type}</TableCell>
                  <TableCell className="max-w-0 truncate">{e.summary}</TableCell>
                </TableRow>
                {open === e.id && (
                  <TableRow>
                    <TableCell />
                    <TableCell colSpan={5} className="whitespace-normal">
                      <div className="flex flex-col gap-2 py-1">
                        <pre className="max-h-64 overflow-auto rounded bg-muted p-2 font-mono text-xs">{JSON.stringify(e, null, 2)}</pre>
                        <div className="flex flex-wrap gap-2">
                          <Button size="sm" variant="outline" onClick={() => copy(e.id, JSON.stringify(e, null, 2))}>
                            {copied === e.id ? <Check /> : <Copy />} Copy entry
                          </Button>
                          {e.entity_id && (
                            <Button size="sm" variant="outline" onClick={() => copy(e.id + "t", `${brand().cli} audit list --entity-id ${e.entity_id} --sort created_at`)}>
                              {copied === e.id + "t" ? <Check /> : <Copy />} Copy trace command
                            </Button>
                          )}
                        </div>
                      </div>
                    </TableCell>
                  </TableRow>
                )}
              </Fragment>
            ))}
          </TableBody>
        </Table>
        {entries == null && <p className="p-4 text-sm text-muted-foreground">Loading…</p>}
        {entries?.length === 0 && (
          <p className="p-4 text-sm text-muted-foreground">{filtered ? "No entries match." : "No activity yet."}</p>
        )}
      </div>
      {cursor && (
        <Button variant="outline" className="self-center" onClick={() => load(cursor)}>
          Load more
        </Button>
      )}
    </div>
  )
}
