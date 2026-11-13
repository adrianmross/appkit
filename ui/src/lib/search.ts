import { getPref } from "./settings"

// The search box's `key:value` syntax: "price:<800k beds:>=3 garage" is two
// qualifiers and the free text "garage". The app says which keys exist and
// what each one matches.
export type SearchQualifier = { key: string; op: "=" | ">" | ">=" | "<" | "<="; value: string }

export type ParsedSearch = { free: string; qualifiers: SearchQualifier[] }

export type Qualifier<Row> = {
  key: string
  /** Shown beside the key in the suggestions: "Applied date, like >2026-01-01". */
  hint: string
  match: (row: Row, q: SearchQualifier) => boolean
  /** Values to offer for what has been typed after the colon. */
  values?: (partial: string, rows: Row[]) => { value: string; hint?: string }[]
}

const OPS = [">=", "<=", ">", "<"] as const

const syntaxOn = () => getPref("searchSyntax") !== "off"

// `keys` are the qualifier keys the app knows. A known key with nothing
// after it is still being typed, so it is ignored rather than searched for
// as the literal text "status:".
export function parseSearch(input: string, keys: string[] = []): ParsedSearch {
  // With the syntax turned off in Settings, everything is plain text.
  if (!syntaxOn()) return { free: input.trim(), qualifiers: [] }
  const qualifiers: SearchQualifier[] = []
  const free: string[] = []
  for (const token of input.trim().split(/\s+/).filter(Boolean)) {
    const m = token.match(/^([a-zA-Z_]+):(.*)$/)
    if (!m) {
      free.push(token)
      continue
    }
    const [, key, rest] = m
    let op: SearchQualifier["op"] = "="
    let value = rest
    for (const candidate of OPS) {
      if (rest.startsWith(candidate)) {
        op = candidate
        value = rest.slice(candidate.length)
        break
      }
    }
    if (!value) {
      if (!keys.includes(key.toLowerCase())) free.push(token)
      continue
    }
    qualifiers.push({ key: key.toLowerCase(), op, value })
  }
  return { free: free.join(" "), qualifiers }
}

// For a qualifier on a sortable string such as an ISO date: "=" is a prefix
// match (applied:2026-03), the rest compare.
export function compareText(rowValue: string | undefined, op: SearchQualifier["op"], value: string): boolean {
  if (!rowValue) return false
  if (op === "=") return rowValue.startsWith(value)
  return op === ">" ? rowValue > value : op === ">=" ? rowValue >= value : op === "<" ? rowValue < value : rowValue <= value
}

// A qualifier with a key the app doesn't define filters nothing.
export function matchesQualifiers<Row>(row: Row, qualifiers: SearchQualifier[], defs: Qualifier<Row>[]): boolean {
  return qualifiers.every((q) => defs.find((d) => d.key === q.key)?.match(row, q) ?? true)
}

export type SearchToken = { text: string; kind: "space" | "free" | "key" | "op" | "value" | "unknown" }

// Splits input the way parseSearch reads it, for syntax highlighting. Unknown
// keys still filter nothing, so they are marked rather than hidden.
export function tokenize(input: string, keys: string[] = []): SearchToken[] {
  const syntax = syntaxOn()
  const out: SearchToken[] = []
  for (const part of input.split(/(\s+)/)) {
    if (!part) continue
    if (/^\s+$/.test(part)) {
      out.push({ text: part, kind: "space" })
      continue
    }
    const m = syntax ? part.match(/^([a-zA-Z_]+):(.*)$/) : null
    if (!m || !m[2]) {
      out.push({ text: part, kind: "free" })
      continue
    }
    const known = keys.includes(m[1].toLowerCase())
    out.push({ text: m[1] + ":", kind: known ? "key" : "unknown" })
    const op = OPS.find((o) => m[2].startsWith(o))
    if (op) out.push({ text: op, kind: "op" })
    out.push({ text: m[2].slice(op?.length || 0), kind: known ? "value" : "unknown" })
  }
  return out
}

// The whitespace-delimited word around the caret.
export function tokenAt(input: string, caret: number) {
  let start = caret
  while (start > 0 && !/\s/.test(input[start - 1])) start--
  let end = caret
  while (end < input.length && !/\s/.test(input[end])) end++
  return { start, end, text: input.slice(start, end) }
}
