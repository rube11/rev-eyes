import assert from "node:assert/strict"
import test from "node:test"

import { withTimeout } from "../src/even/promise-timeout.js"

test("returns work that finishes before the deadline", async () => {
  assert.equal(
    await withTimeout(Promise.resolve("ready"), 50, "too slow"),
    "ready",
  )
})

test("rejects work that does not finish before the deadline", async () => {
  const pending = new Promise<never>(() => undefined)
  await assert.rejects(withTimeout(pending, 5, "start timed out"), /start timed out/u)
})
