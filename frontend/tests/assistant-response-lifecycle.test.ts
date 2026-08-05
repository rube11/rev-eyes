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

test("keeps the conversation active through the post-display grace period", () => {
  type Task = { callback: () => void; delayMs: number; canceled: boolean }
  const tasks: Task[] = []
  const events: string[] = []
  const lifecycle = new AssistantResponseLifecycle({
    conversationGraceMs: 8_000,
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
  assert.deepEqual(tasks.map((task) => task.delayMs), [5_000, 13_000])

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
