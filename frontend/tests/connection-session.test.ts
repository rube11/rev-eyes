import assert from 'node:assert/strict'
import { test } from 'node:test'
import { ConnectionSession } from '../src/even/connection-session.js'

function deferred() {
  let resolve!: () => void
  const promise = new Promise<void>((done) => { resolve = done })
  return { promise, resolve }
}

test('reconnect waits for the previous runtime cleanup', async () => {
  const session = new ConnectionSession()
  const gate = deferred()
  const stopping = deferred()
  const events: string[] = []
  await session.start(async () => async () => {
    events.push('stopping'); stopping.resolve(); await gate.promise; events.push('stopped')
  })
  const restart = session.start(async () => { events.push('started'); return () => undefined })
  await stopping.promise
  assert.deepEqual(events, ['stopping'])
  gate.resolve()
  await restart
  assert.deepEqual(events, ['stopping', 'stopped', 'started'])
  await session.stop()
})

test('repeated restart requests keep only the newest queued initialization', async () => {
  const session = new ConnectionSession()
  const events: string[] = []
  await Promise.all([1, 2, 3].map((id) => session.start(async () => {
    events.push(`start ${id}`)
    return () => { events.push(`stop ${id}`) }
  })))
  assert.deepEqual(events, ['start 3'])
  await session.stop()
  assert.deepEqual(events, ['start 3', 'stop 3'])
})

test('a superseded pending setup is cleaned up before a new runtime starts', async () => {
  const session = new ConnectionSession()
  const started = deferred()
  const ready = deferred()
  const events: string[] = []
  const first = session.start(async () => {
    started.resolve(); await ready.promise
    return () => { events.push('old cleanup') }
  })
  await started.promise
  const second = session.start(async () => {
    events.push('new setup'); return () => undefined
  })
  ready.resolve()
  await Promise.all([first, second])
  assert.deepEqual(events, ['old cleanup', 'new setup'])
  await session.stop()
})

test('sign-out during pending setup cleans up the late runtime once', async () => {
  const session = new ConnectionSession()
  const started = deferred()
  const ready = deferred()
  let cleanups = 0
  const pending = session.start(async () => {
    started.resolve(); await ready.promise
    return () => { cleanups += 1 }
  })
  await started.promise
  const stopped = session.stop()
  ready.resolve()
  await Promise.all([pending, stopped])
  await session.stop()
  assert.equal(cleanups, 1)
})

test('a rejected initialization does not prevent a later retry', async () => {
  const session = new ConnectionSession()
  await assert.rejects(session.start(async () => { throw new Error('no bridge') }), /no bridge/)
  let started = false
  await session.start(async () => { started = true; return () => undefined })
  assert.equal(started, true)
  await session.stop()
})

test('a failed cleanup prevents the next runtime from starting until cleanup succeeds', async () => {
  const session = new ConnectionSession()
  let failCleanup = true
  await session.start(async () => () => { if (failCleanup) throw new Error('not stopped') })
  let starts = 0
  const initialize = async () => { starts += 1; return () => undefined }
  await assert.rejects(session.start(initialize), /not stopped/)
  assert.equal(starts, 0)
  failCleanup = false
  await session.start(initialize)
  assert.equal(starts, 1)
  await session.stop()
})
