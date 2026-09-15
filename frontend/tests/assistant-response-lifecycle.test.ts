import assert from "node:assert/strict"
import test from "node:test"
import { AssistantResponseLifecycle } from "../src/even/assistant-response-lifecycle.js"

function fixture() {
  const tasks: { callback: () => void; delay: number; canceled: boolean }[] = []
  let expired = 0
  const lifecycle = new AssistantResponseLifecycle({
    onConversationExpired: () => expired++,
    scheduleTimer: (callback, delay) => {
      const task = { callback, delay, canceled: false }
      tasks.push(task)
      return task
    },
    cancelTimer: handle => { (handle as typeof tasks[number]).canceled = true },
  })
  return { tasks, lifecycle, expired: () => expired }
}

test("only microphone capture expires; no reading deadline is scheduled", () => {
  const f = fixture()
  f.lifecycle.begin()
  assert.deepEqual(f.tasks.map(task => task.delay), [30_000])
  assert.equal(f.lifecycle.active, true)
  f.tasks[0].callback()
  assert.equal(f.expired(), 1)
  assert.equal(f.lifecycle.active, false)
})

test("cancel invalidates callbacks already queued by the clock", () => {
  const f = fixture()
  f.lifecycle.begin()
  f.lifecycle.cancel()
  f.tasks[0].callback()
  assert.equal(f.tasks[0].canceled, true)
  assert.equal(f.expired(), 0)
  assert.equal(f.lifecycle.active, false)
})

test("a new reply replaces the old microphone deadline", () => {
  const f = fixture()
  f.lifecycle.begin()
  f.lifecycle.begin()
  f.tasks[0].callback()
  assert.equal(f.expired(), 0)
  assert.equal(f.lifecycle.active, true)
  f.tasks[1].callback()
  assert.equal(f.expired(), 1)
  assert.equal(f.lifecycle.active, false)
})
