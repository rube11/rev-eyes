import assert from "node:assert/strict"
import test from "node:test"

import {
  AudioCaptureSession,
  type LocalCaptureControls,
} from "../src/even/audio-capture-session.js"

function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((complete) => {
    resolve = complete
  })
  return { promise, resolve }
}

function localControls(
  start: () => Promise<boolean>,
  counters: { cancels: number; disposals: number; stops: number },
): LocalCaptureControls {
  return {
    cancel: () => {
      counters.cancels += 1
    },
    dispose: () => {
      counters.disposals += 1
    },
    start,
    stop: () => {
      counters.stops += 1
    },
  }
}

test("required local capture must start before the session succeeds", async () => {
  const localStart = deferred<boolean>()
  const localEntered = deferred<void>()
  const counters = { cancels: 0, disposals: 0, stops: 0 }
  const session = new AudioCaptureSession({
    device: {
      start: () => Promise.resolve(true),
      stop: () => Promise.resolve(true),
    },
    local: localControls(() => {
      localEntered.resolve()
      return localStart.promise
    }, counters),
    localRequired: true,
  })

  let settled = false
  const starting = session.start().then((started) => {
    settled = true
    return started
  })
  await localEntered.promise
  assert.equal(settled, false)
  localStart.resolve(true)
  assert.equal(await starting, true)
  assert.equal(session.running, true)
  await session.stop(false)
})

test("a failed required local start turns the device back off", async () => {
  let deviceStops = 0
  const counters = { cancels: 0, disposals: 0, stops: 0 }
  const session = new AudioCaptureSession({
    device: {
      start: () => Promise.resolve(true),
      stop: () => {
        deviceStops += 1
        return Promise.resolve(true)
      },
    },
    local: localControls(() => Promise.resolve(false), counters),
    localRequired: true,
  })

  assert.equal(await session.start(), false)
  assert.equal(session.running, false)
  assert.equal(deviceStops, 1)
  assert.equal(counters.cancels, 1)
})

test("shadow-only local startup does not delay legacy capture", async () => {
  const localStart = deferred<boolean>()
  const counters = { cancels: 0, disposals: 0, stops: 0 }
  const session = new AudioCaptureSession({
    device: {
      start: () => Promise.resolve(true),
      stop: () => Promise.resolve(true),
    },
    local: localControls(() => localStart.promise, counters),
    localRequired: false,
  })

  assert.equal(await session.start(), true)
  assert.equal(session.running, true)
  await session.stop(false)
  localStart.resolve(false)
})

test("stop invalidates a required local start in progress", async () => {
  const localStart = deferred<boolean>()
  const localEntered = deferred<void>()
  let deviceStops = 0
  const counters = { cancels: 0, disposals: 0, stops: 0 }
  const session = new AudioCaptureSession({
    device: {
      start: () => Promise.resolve(true),
      stop: () => {
        deviceStops += 1
        return Promise.resolve(true)
      },
    },
    local: localControls(() => {
      localEntered.resolve()
      return localStart.promise
    }, counters),
    localRequired: true,
  })

  const starting = session.start()
  await localEntered.promise
  assert.equal(await session.stop(false), true)
  localStart.resolve(true)
  assert.equal(await starting, false)
  assert.equal(deviceStops, 1)
  assert.equal(counters.cancels, 1)
})

test("policy is revalidated after required local startup", async () => {
  const localStart = deferred<boolean>()
  const localEntered = deferred<void>()
  let allowed = true
  let deviceStops = 0
  const counters = { cancels: 0, disposals: 0, stops: 0 }
  const session = new AudioCaptureSession({
    device: {
      start: () => Promise.resolve(true),
      stop: () => {
        deviceStops += 1
        return Promise.resolve(true)
      },
    },
    local: localControls(() => {
      localEntered.resolve()
      return localStart.promise
    }, counters),
    localRequired: true,
  })

  const starting = session.start(() => allowed)
  await localEntered.promise
  allowed = false
  localStart.resolve(true)
  assert.equal(await starting, false)
  assert.equal(deviceStops, 1)
  assert.equal(counters.cancels, 1)
})
