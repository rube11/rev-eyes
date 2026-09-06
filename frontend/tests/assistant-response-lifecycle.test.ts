import assert from "node:assert/strict"
import test from "node:test"

import {
  AssistantResponseLifecycle,
  responseDisplayMilliseconds,
} from "../src/even/assistant-response-lifecycle.js"

test("calculates bounded reading time from response words", () => {
  assert.equal(responseDisplayMilliseconds(""), 5_000)
  assert.equal(
    responseDisplayMilliseconds("one two three four five six seven eight nine ten"),
    5_333,
  )
  assert.equal(
    responseDisplayMilliseconds(Array.from({ length: 100 }, () => "word").join(" ")),
    14_000,
  )
})

test("keeps follow-up listening open for 30 seconds from the reply", () => {
  type Task = { callback: () => void; delayMs: number; canceled: boolean }
  const tasks: Task[] = []
  const events: string[] = []
  const lifecycle = new AssistantResponseLifecycle({
    onDisplayExpired: () => events.push("display"),
    onConversationExpired: () => events.push("conversation"),
    scheduleTimer: (callback, delayMs) => {
      const task = { callback, delayMs, canceled: false }
      tasks.push(task)
      return task
    },
    cancelTimer: (handle) => {
      const task = handle as Task
      task.canceled = true
    },
  })

  assert.equal(lifecycle.begin("a short response"), 5_000)
  assert.equal(lifecycle.active, true)
  assert.deepEqual(tasks.map((task) => task.delayMs), [5_000, 30_000])

  tasks[0].callback()
  assert.deepEqual(events, ["display"])
  assert.equal(lifecycle.active, true)

  tasks[1].callback()
  assert.deepEqual(events, ["display", "conversation"])
  assert.equal(lifecycle.active, false)
})

test("canceling invalidates both pending deadlines", () => {
  type Task = { callback: () => void; canceled: boolean }
  const tasks: Task[] = []
  let callbacks = 0
  const lifecycle = new AssistantResponseLifecycle({
    onDisplayExpired: () => callbacks += 1,
    onConversationExpired: () => callbacks += 1,
    scheduleTimer: (callback) => {
      const task = { callback, canceled: false }
      tasks.push(task)
      return task
    },
    cancelTimer: (handle) => {
      const task = handle as Task
      task.canceled = true
    },
  })

  lifecycle.begin("response")
  lifecycle.cancel()
  assert.equal(lifecycle.active, false)
  assert.ok(tasks.every((task) => task.canceled))
  for (const task of tasks) {
    task.callback()
  }
  assert.equal(callbacks, 0)
})

test("a new reply invalidates old callbacks and keeps the same 30-second deadline", () => {
  const tasks: { callback: () => void; delayMs: number }[] = []
  const events: string[] = []
  const lifecycle = new AssistantResponseLifecycle({
    onDisplayExpired: () => events.push("display"),
    onConversationExpired: () => events.push("conversation"),
    scheduleTimer: (callback, delayMs) => tasks.push({ callback, delayMs }),
    cancelTimer: () => {},
  })
  lifecycle.begin("Short reply")
  lifecycle.begin("word ".repeat(100))
  tasks[0].callback()
  tasks[1].callback()
  assert.deepEqual(events, [])
  assert.equal(lifecycle.active, true)
  assert.deepEqual(tasks.slice(2).map(task => task.delayMs), [14_000, 30_000])
  tasks[3].callback()
  assert.equal(lifecycle.active, false)
})
