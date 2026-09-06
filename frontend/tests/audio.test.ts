import assert from "node:assert/strict"
import test from "node:test"

import { AudioCaptureController } from "../src/even/audio.js"

function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((complete) => {
    resolve = complete
  })
  return { promise, resolve }
}

test("coalesces concurrent device starts", async () => {
  const pending = deferred<boolean>()
  let starts = 0
  const capture = new AudioCaptureController({
    start: () => {
      starts += 1
      return pending.promise
    },
    stop: () => Promise.resolve(true),
  })

  const first = capture.start()
  const second = capture.start()
  assert.equal(starts, 1)
  pending.resolve(true)

  assert.equal(await first, true)
  assert.equal(await second, true)
  assert.equal(capture.running, true)
})

test("a stop invalidates an in-flight start and leaves the device off", async () => {
  const pending = deferred<boolean>()
  let stops = 0
  const capture = new AudioCaptureController({
    start: () => pending.promise,
    stop: () => {
      stops += 1
      return Promise.resolve(true)
    },
  })

  const starting = capture.start()
  await capture.stop()
  pending.resolve(true)

  assert.equal(await starting, false)
  assert.equal(capture.state, "idle")
  assert.equal(stops, 2)
})

test("failed policy revalidation turns a newly started device back off", async () => {
  let stops = 0
  const capture = new AudioCaptureController({
    start: () => Promise.resolve(true),
    stop: () => {
      stops += 1
      return Promise.resolve(true)
    },
  })

  assert.equal(await capture.start(() => false), false)
  assert.equal(capture.state, "idle")
  assert.equal(stops, 1)
})

test("a restart waits for an older start and stop before touching the device", async () => {
  const firstStart = deferred<boolean>()
  const secondStart = deferred<boolean>()
  const firstStop = deferred<boolean>()
  let starts = 0
  let stops = 0
  const capture = new AudioCaptureController({
    start: () => {
      starts += 1
      return starts === 1 ? firstStart.promise : secondStart.promise
    },
    stop: () => {
      stops += 1
      return stops === 1 ? firstStop.promise : Promise.resolve(true)
    },
  })

  const staleStart = capture.start()
  const stopping = capture.stop()
  const restarted = capture.start()

  assert.equal(starts, 1)
  firstStop.resolve(true)
  await stopping
  assert.equal(starts, 1)

  firstStart.resolve(true)
  assert.equal(await staleStart, false)
  await new Promise((resolve) => setTimeout(resolve, 0))
  assert.equal(starts, 2)

  secondStart.resolve(true)
  assert.equal(await restarted, true)
  assert.equal(capture.running, true)
  assert.equal(stops, 2)
})

test("reports an unconfirmed device stop", async () => {
  const capture = new AudioCaptureController({
    start: () => Promise.resolve(true),
    stop: () => Promise.resolve(false),
  })

  assert.equal(await capture.start(), true)
  assert.equal(await capture.stop(), false)
  assert.equal(capture.state, "idle")
})
