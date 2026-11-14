import assert from "node:assert/strict"
import * as nodeModule from "node:module"
import { test } from "node:test"

// The sources import each other without an extension, as a bundler allows.
type Next = (specifier: string, context: unknown) => unknown
// ponytail: the installed @types/node predates registerHooks (Node 22.15), hence the cast.
;(nodeModule as unknown as { registerHooks(h: { resolve(specifier: string, context: unknown, next: Next): unknown }): void }).registerHooks({
  resolve(specifier, context, next) {
    try {
      return next(specifier, context)
    } catch (err) {
      if (!specifier.startsWith(".")) throw err
      return next(specifier + ".ts", context)
    }
  },
})
const { matchesQualifiers, parseSearch, quoteValue, tokenAt, tokenize } = await import("./search.ts")

test("a quoted value keeps its spaces", () => {
  const input = 'hood:"Logan Circle" price:<800k garage'
  assert.deepEqual(parseSearch(input, ["hood", "price"]), {
    free: "garage",
    qualifiers: [
      { key: "hood", op: "=", value: "Logan Circle" },
      { key: "price", op: "<", value: "800k" },
    ],
  })
  // The highlighted copy must be the typed text, character for character.
  assert.equal(tokenize(input, ["hood", "price"]).map((t) => t.text).join(""), input)
  assert.deepEqual(tokenAt(input, 12), { start: 0, end: 19, text: 'hood:"Logan Circle"' })
  // Still being typed: the open quote runs to the end.
  assert.equal(tokenAt('beds:2 hood:"Logan Ci', 21).text, 'hood:"Logan Ci')
  assert.deepEqual(tokenAt("a  b", 2), { start: 2, end: 2, text: "" })
  assert.equal(quoteValue("Logan Circle"), '"Logan Circle"')
  assert.equal(quoteValue("shaw"), "shaw")
})

test("a qualifier without a match rule filters nothing", () => {
  const defs = [{ key: "hood", hint: "" }, { key: "beds", hint: "", match: (r: { beds: number }, q: { value: string }) => r.beds === Number(q.value) }]
  assert.ok(matchesQualifiers({ beds: 2 }, parseSearch("hood:shaw beds:2", ["hood", "beds"]).qualifiers, defs))
  assert.ok(!matchesQualifiers({ beds: 3 }, parseSearch("hood:shaw beds:2", ["hood", "beds"]).qualifiers, defs))
})
