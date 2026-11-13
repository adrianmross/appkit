import assert from "node:assert/strict"
import { test } from "node:test"
import { brand, configure, storageKey, tokenCookie } from "./brand.ts"
import { isPublicPath } from "./public-paths.ts"

test("storage keys and the token cookie follow the configured slug", () => {
  assert.equal(storageKey("token"), "app.token")
  configure({ slug: "acme", name: "Acme Tracker" })
  assert.equal(storageKey("token"), "acme.token")
  assert.equal(storageKey("section.notes"), "acme.section.notes")
  assert.equal(tokenCookie(false), "acme_token")
  assert.equal(tokenCookie(true), "__Host-acme_token")
  assert.equal(brand().name, "Acme Tracker")
  // Fields configure() was not given keep their defaults.
  assert.equal(brand().wordmark.light, "/brand/wordmark.svg")
})

test("public paths: exact, or everything under a trailing slash", () => {
  assert.ok(isPublicPath("/sign-in"))
  assert.ok(isPublicPath("/api/auth/sign-in"))
  assert.ok(!isPublicPath("/sign-in-as-admin"))
  assert.ok(!isPublicPath("/"))
  assert.ok(isPublicPath("/", ["/"]))
  assert.ok(!isPublicPath("/listings", ["/"]))
  assert.ok(isPublicPath("/docs/cli", ["/docs/"]))
})
