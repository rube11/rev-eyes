import assert from 'node:assert/strict'
import { test } from 'node:test'
import {
  applyMemoryEdit, familyLabel, familyOf, isStaleMemory, profileEntries, recentEntries,
} from '../src/features/workspace/memoryModel.js'
import type { MemoryItem } from '../src/features/workspace/workspaceTypes.js'

const day = 86_400_000
const now = Date.parse('2026-09-16T12:00:00Z')
const ago = (days: number) => new Date(now - days * day).toISOString()
const memory = (id: string, extra: Partial<MemoryItem> = {}): MemoryItem => ({
  id, title: id, summary: `Summary ${id}`, topics: [], kind: 'fact', status: 'active',
  layer: 'core', assignedLayer: 'core', pinned: false, createdAt: ago(5), updatedAt: ago(5), observedAt: ago(5), ...extra,
})

test('profile orders pinned first, then instruction, goal, relationship, fact, newest observation', () => {
  const entries = profileEntries([
    memory('fact-old', { observedAt: ago(9) }),
    memory('fact-new', { observedAt: ago(1) }),
    memory('goal', { kind: 'goal' }),
    memory('pinned-pref', { kind: 'preference', pinned: true }),
    memory('rule', { kind: 'instruction' }),
    memory('detail', { layer: 'detail' }),
    memory('expired', { expiresAt: ago(1) }),
    memory('forgotten', { status: 'forgotten' }),
  ], now)
  assert.deepEqual(entries.map((item) => item.id), ['pinned-pref', 'rule', 'goal', 'fact-new', 'fact-old'])
})

test('profile drops duplicate summaries and caps the section', () => {
  const entries = profileEntries([
    memory('a', { summary: 'Prefers  aisle seats.' }),
    memory('b', { summary: 'prefers aisle seats.' }),
    ...Array.from({ length: 20 }, (_, index) => memory(`m${index}`, { summary: `Fact ${index}` })),
  ], now)
  assert.equal(entries.length, 16)
  assert.equal(entries.filter((item) => item.summary.toLowerCase().includes('aisle')).length, 1)
})

test('recent entries are the expiring layer, soonest first', () => {
  const entries = recentEntries([
    memory('later', { layer: 'recent', expiresAt: new Date(now + 5 * day).toISOString() }),
    memory('soon', { layer: 'recent', expiresAt: new Date(now + day).toISOString() }),
    memory('no-expiry', { layer: 'recent' }),
    memory('core'),
  ], now)
  assert.deepEqual(entries.map((item) => item.id), ['soon', 'later'])
})

test('stale flags durable beliefs untouched for 90 days unless pinned or recent', () => {
  assert.equal(isStaleMemory(memory('p', { kind: 'preference', updatedAt: ago(91) }), now), true)
  assert.equal(isStaleMemory(memory('p', { kind: 'preference', updatedAt: ago(89) }), now), false)
  assert.equal(isStaleMemory(memory('p', { kind: 'preference', updatedAt: ago(91), pinned: true }), now), false)
  assert.equal(isStaleMemory(memory('f', { kind: 'fact', updatedAt: ago(200) }), now), false)
  assert.equal(isStaleMemory(memory('e', { kind: 'goal', layer: 'recent', updatedAt: ago(200) }), now), false)
})

test('edits apply optimistically without touching unrelated fields', () => {
  const at = new Date(now).toISOString()
  const base = memory('m', { topics: ['work'], sourceConversationId: 'c1' })
  const forgotten = applyMemoryEdit(base, { action: 'forget' }, at)
  assert.equal(forgotten.status, 'forgotten')
  assert.equal(forgotten.inactiveAt, at)
  const restored = applyMemoryEdit(forgotten, { action: 'restore' }, at)
  assert.equal(restored.status, 'active')
  assert.equal(restored.inactiveAt, undefined)
  const pinned = applyMemoryEdit(memory('d', { layer: 'detail', assignedLayer: 'detail' }), { action: 'pin' }, at)
  assert.equal(pinned.layer, 'core')
  assert.equal(pinned.pinned, true)
  const unpinned = applyMemoryEdit(pinned, { action: 'unpin' }, at)
  assert.equal(unpinned.layer, 'detail')
  const unpinnedRecent = applyMemoryEdit(memory('r', { layer: 'core', assignedLayer: 'recent', pinned: true }), { action: 'unpin' }, at)
  assert.equal(unpinnedRecent.layer, 'recent')
  const updated = applyMemoryEdit(base, { action: 'update', title: ' New ', summary: ' Body ' }, at)
  assert.equal(updated.title, 'New')
  assert.equal(updated.summary, 'Body')
  assert.deepEqual(updated.topics, ['work'])
  assert.equal(updated.sourceConversationId, 'c1')
})

test('families come from the canonical key taxonomy only', () => {
  assert.equal(familyOf(memory('a', { memoryKey: 'profile.relationship.maya' })), 'relationship')
  assert.equal(familyOf(memory('b', { memoryKey: 'state.activity.current' })), 'activity')
  assert.equal(familyOf(memory('c', { memoryKey: 'profile.food.preference.pho' })), 'food')
  assert.equal(familyOf(memory('d', { memoryKey: 'test.memory.management.1' })), undefined)
  assert.equal(familyOf(memory('e', { memoryKey: 'profile.role' })), undefined)
  assert.equal(familyOf(memory('f')), undefined)
  assert.equal(familyLabel('relationship'), 'People')
  assert.equal(familyLabel('travel_plans'), 'Travel plans')
})
