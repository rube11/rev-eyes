import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readConversationTranscript } from '../src/features/workspace/conversationTranscript.js'
import type { TranscriptItem } from '../src/features/workspace/workspaceTypes.js'

const message = (id: number): TranscriptItem => ({
  id: String(id), speaker: id % 2 ? 'assistant' : 'user',
  text: `Message ${id}`, startedAt: new Date(id * 1000).toISOString(),
})

test('loads beyond the recent-message limit and keeps the back-and-forth in order', async () => {
  const messages = Array.from({ length: 1203 }, (_, index) => message(index))
  const requests: number[] = []
  const result = await readConversationTranscript(async (offset, limit) => {
    requests.push(offset)
    return messages.slice(offset, offset + limit)
  }, new AbortController().signal)
  assert.deepEqual(requests, [0, 500, 1000])
  assert.deepEqual(result, messages)
})

test('empty saved conversations return an empty log', async () => {
  assert.deepEqual(await readConversationTranscript(async () => [], new AbortController().signal), [])
})

test('an incomplete fetch rejects instead of claiming a complete log', async () => {
  await assert.rejects(readConversationTranscript(async (offset) => {
    if (offset) throw new Error('Network failed')
    return Array.from({ length: 500 }, (_, index) => message(index))
  }, new AbortController().signal), /Network failed/)
})

test('leaving a chat cancels pagination and discards a late response', async () => {
  const controller = new AbortController()
  let requests = 0
  await assert.rejects(readConversationTranscript(async () => {
    requests++
    controller.abort()
    return Array.from({ length: 500 }, (_, index) => message(index))
  }, controller.signal), { name: 'AbortError' })
  assert.equal(requests, 1)
})

test('duplicate messages are deduplicated and sorted by timestamp', async () => {
  const result = await readConversationTranscript(async () => [message(2), message(1), message(2)], new AbortController().signal)
  assert.deepEqual(result, [message(1), message(2)])
})
