import assert from "node:assert/strict"
import test from "node:test"
import { DiagnosticLog, MAX_ENTRIES } from "../src/diagnostics/log.js"

function storage() {
  let value: string | null = null
  return { getItem: () => value, setItem: (_key: string, next: string) => { value = next } }
}
test("diagnostic history survives reload, preserves timestamps, and is bounded", () => {
  const disk = storage()
  const log = new DiagnosticLog(disk)
  for (let i = 0; i < MAX_ENTRIES + 10; i++) log.add("sample", { frames: i }, i)
  const reopened = new DiagnosticLog(disk)
  assert.equal(reopened.entries.length, MAX_ENTRIES)
  assert.equal(reopened.entries[0].at, 10)
  assert.equal(reopened.entries.at(-1)?.at, MAX_ENTRIES + 9)
  reopened.clear()
  assert.equal(new DiagnosticLog(disk).entries.length, 0)
})
test("storage failures retain in-memory results and expose a warning", () => {
  const log = new DiagnosticLog({ getItem: () => null, setItem: () => { throw new Error("quota") } })
  log.add("speech", { marker: "test one" })
  assert.equal(log.persistent, false)
  assert.equal(log.entries.length, 1)
})
test("corrupt saved data cannot break diagnostics", () => {
  const log = new DiagnosticLog({ getItem: () => "{broken", setItem: () => {} })
  assert.deepEqual(log.entries, [])
  assert.equal(log.persistent, false)
  log.add("opened")
  assert.equal(log.persistent, true)
})
