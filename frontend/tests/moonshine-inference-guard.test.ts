import assert from "node:assert/strict"
import test from "node:test"

import { guardMoonshineInference } from "../src/even/moonshine-inference-guard.js"

type InferenceModelStub = {
  generate: (audio: Float32Array) => Promise<string>
}

function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((complete) => {
    resolve = complete
  })
  return { promise, resolve }
}

function flushTasks(): Promise<void> {
  return new Promise((resolve) => setImmediate(resolve))
}

test("serializes overlapping inference on one Moonshine model", async () => {
  const first = deferred<string>()
  const second = deferred<string>()
  const releases = [first, second]
  const calls: number[] = []
  let active = 0
  let maxActive = 0
  const model = {
    generate: async (audio: Float32Array) => {
      active += 1
      maxActive = Math.max(maxActive, active)
      calls.push(audio[0])
      const result = await releases[calls.length - 1].promise
      active -= 1
      return result
    },
  }
  const lifecycle = guardMoonshineInference(model)
  assert.ok(lifecycle)
  lifecycle.beginSession()

  const firstResult = model.generate(new Float32Array([1]))
  const secondResult = model.generate(new Float32Array([2]))
  await flushTasks()
  assert.deepEqual(calls, [1])

  first.resolve("first")
  assert.equal(await firstResult, "first")
  await flushTasks()
  assert.deepEqual(calls, [1, 2])

  second.resolve("second")
  assert.equal(await secondResult, "second")
  assert.equal(maxActive, 1)
})

test("invalidates stale results without letting an old owner stop a new one", async () => {
  const oldRelease = deferred<string>()
  const newRelease = deferred<string>()
  let calls = 0
  const model: InferenceModelStub = {
    generate: () => {
      calls += 1
      return calls === 1 ? oldRelease.promise : newRelease.promise
    },
  }
  const oldLifecycle = guardMoonshineInference(model)
  const newLifecycle = guardMoonshineInference(model)
  assert.ok(oldLifecycle)
  assert.ok(newLifecycle)

  oldLifecycle.beginSession()
  const oldResult = model.generate(new Float32Array([1]))
  await flushTasks()

  newLifecycle.beginSession()
  const newResult = model.generate(new Float32Array([2]))
  oldLifecycle.endSession()

  oldRelease.resolve("stale")
  assert.equal(await oldResult, "")
  await flushTasks()
  newRelease.resolve("current")
  assert.equal(await newResult, "current")
})

test("retries transient ONNX session failures once", async () => {
  let calls = 0
  const errors: string[] = []
  const model: InferenceModelStub = {
    generate: async () => {
      calls += 1
      if (calls === 1) {
        throw new Error("Session already started")
      }
      return "recovered"
    },
  }
  const lifecycle = guardMoonshineInference(model, {
    onError: (message) => errors.push(message),
  })
  assert.ok(lifecycle)
  lifecycle.beginSession()

  assert.equal(await model.generate(new Float32Array([1])), "recovered")
  assert.equal(calls, 2)
  assert.deepEqual(errors, [])
})

test("contains inference failures and continues processing later speech", async () => {
  let calls = 0
  const errors: string[] = []
  const model: InferenceModelStub = {
    generate: async () => {
      calls += 1
      if (calls === 1) {
        throw new Error("model failed")
      }
      return "next segment"
    },
  }
  const lifecycle = guardMoonshineInference(model, {
    onError: (message) => errors.push(message),
  })
  assert.ok(lifecycle)
  lifecycle.beginSession()

  assert.equal(await model.generate(new Float32Array([1])), "")
  assert.equal(await model.generate(new Float32Array([2])), "next segment")
  assert.equal(errors.length, 1)
  assert.match(errors[0], /model failed/u)
})

test("bounds queued inference work", async () => {
  const firstRelease = deferred<string>()
  const errors: string[] = []
  let calls = 0
  const model: InferenceModelStub = {
    generate: async () => {
      calls += 1
      if (calls === 1) {
        return firstRelease.promise
      }
      return `result ${calls}`
    },
  }
  const lifecycle = guardMoonshineInference(model, {
    maxPending: 2,
    onError: (message) => errors.push(message),
  })
  assert.ok(lifecycle)
  lifecycle.beginSession()

  const first = model.generate(new Float32Array([1]))
  const second = model.generate(new Float32Array([2]))
  const dropped = model.generate(new Float32Array([3]))
  assert.equal(await dropped, "")
  assert.match(errors[0], /queue full/u)

  firstRelease.resolve("first")
  assert.equal(await first, "first")
  assert.equal(await second, "result 2")
  assert.equal(calls, 2)
})
