import assert from 'node:assert/strict'
import { test } from 'node:test'
import { getAssistantStatus } from '../src/features/workspace/assistantStatus.js'

test('only known ready or in-progress states show an active connection', () => {
  for (const state of ['Connected', 'Sleeping', 'Listening', 'Thinking', 'Starting microphone']) {
    assert.equal(getAssistantStatus(state).active, true, state)
  }
  for (const state of ['Connecting', 'Reconnecting', 'Offline', 'Disconnected', 'Microphone unavailable', 'Glasses command failed', '', 'unknown']) {
    assert.equal(getAssistantStatus(state).active, false, state)
  }
})

test('preview does not claim a real glasses connection', () => {
  const status = getAssistantStatus('Connected', true)
  assert.equal(status.active, false)
  assert.equal(status.label, 'Preview')
})

test('standby describes tap-to-wake and automatic end of speech', () => {
  assert.equal(getAssistantStatus(' Sleeping ').label, 'Ready')
  assert.match(getAssistantStatus('Sleeping').detail, /Tap your glasses/)
  assert.match(getAssistantStatus('Listening').detail, /no second tap/)
})
