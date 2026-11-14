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
  /** Whether a loaded row passes, for the suggestions and the counts beside values. Leave it out when the server filters: nothing is counted. */
  match?: (row: Row, q: SearchQualifier) => boolean
  /** Values to offer for what has been typed after the colon. */
  values?: (partial: string, rows: Row[]) => { value: string; hint?: string }[]
}

const OPS = [">=", "<=", ">", "<"] as const

// A word ends at whitespace, except inside double quotes: hood:"Logan Circle"
// is one word. A quote left open runs to the end of the input.
const WORD = /(?:[^\s"]|"[^"]*"?)+/g
const unquote = (s: string) => s.replace(/"/g, "")
/** A qualifier value as it must be typed: quoted when it holds a space. */
export const quoteValue = (v: string) => (/\s/.test(v) ? `"${v}"` : v)

const syntaxOn = () => getPref("searchSyntax") !== "off"

// `keys` are the qualifier keys the app knows. A known key with nothing
// after it is still being typed, so it is ignored rather than searched for
// as the literal text "status:".
export function parseSearch(input: string, keys: string[] = []): ParsedSearch {
  // With the syntax turned off in Settings, everything is plain text.
  if (!syntaxOn()) return { free: input.trim(), qualifiers: [] }
  const qualifiers: SearchQualifier[] = []
  const free: string[] = []
  for (const token of input.match(WORD) ?? []) {
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
    value = unquote(value)
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
  return qualifiers.every((q) => defs.find((d) => d.key === q.key)?.match?.(row, q) ?? true)
}

export type SearchToken = { text: string; kind: "space" | "free" | "key" | "op" | "value" | "unknown" }

// Splits input the way parseSearch reads it, for syntax highlighting. Unknown
// keys still filter nothing, so they are marked rather than hidden.
export function tokenize(input: string, keys: string[] = []): SearchToken[] {
  const syntax = syntaxOn()
  const out: SearchToken[] = []
  for (const part of input.match(new RegExp("\\s+|" + WORD.source, "g")) ?? []) {
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

// The word around the caret, read the way parseSearch splits words.
export function tokenAt(input: string, caret: number) {
  for (const m of input.matchAll(WORD)) {
    if (m.index <= caret && caret <= m.index + m[0].length) return { start: m.index, end: m.index + m[0].length, text: m[0] }
  }
  return { start: caret, end: caret, text: "" }
}
