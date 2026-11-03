import assert from 'node:assert/strict'
import { test } from 'node:test'
import { getAssistantStatus } from '../src/features/workspace/assistantStatus.js'

test('only known ready or in-progress states show an active connection', () => {
  for (const state of ['Connected', 'Sleeping', 'Listening', 'Thinking', 'Sending', 'Reading reply', 'Starting microphone', 'Couldn’t read reply', 'Couldn’t load threads', 'T3 working', 'Transcription unavailable']) {
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

test('specific failures are named instead of the generic fallback', () => {
  assert.equal(getAssistantStatus('Glasses display update timed out').label, 'Display issue')
  assert.equal(getAssistantStatus('Glasses page failed (2)').label, 'Display issue')
  assert.equal(getAssistantStatus('Server listening unavailable').label, 'Speech unavailable')
  assert.equal(getAssistantStatus('Couldn’t load threads').label, 'T3 unavailable')
})

test('ready text says where speech goes when a thread is on the glasses', () => {
  assert.match(getAssistantStatus('Connected', false, 'Fix audio').detail, /“Fix audio” in T3/)
  assert.match(getAssistantStatus('Connected').detail, /Eyes will take it from there/)
})
