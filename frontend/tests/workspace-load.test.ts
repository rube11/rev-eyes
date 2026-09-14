import assert from 'node:assert/strict'
import { test } from 'node:test'
import { settleWorkspaceLoads, updateWorkspaceErrors } from '../src/features/workspace/workspaceLoad.js'
import type { WorkspaceResource } from '../src/features/workspace/workspaceTypes.js'

test('a memory failure preserves other successfully loaded sections', async () => {
  const result = await settleWorkspaceLoads({
    conversations: async () => [],
    memories: async () => { throw new Error('missing column') },
    watches: async () => [],
    tasks: async () => [],
  })
  assert.deepEqual(result.failedResources, ['memories'])
  assert.deepEqual(result.data, { conversations: [], watches: [], tasks: [] })
  assert.equal('memories' in result.data, false)
})

test('section requests begin in parallel, including after synchronous failure', async () => {
  const started: string[] = []
  let release!: () => void
  const gate = new Promise<void>((resolve) => { release = resolve })
  const pending = settleWorkspaceLoads({
    conversations: async () => { started.push('conversations'); await gate; return [] },
    memories: () => { started.push('memories'); throw new Error('failure') },
    tasks: async () => { started.push('tasks'); return [] },
  })
  assert.deepEqual(started, ['conversations', 'memories', 'tasks'])
  release()
  assert.deepEqual((await pending).failedResources, ['memories'])
})

test('an all-failed load does not invent successful empty sections', async () => {
  const fail = async () => { throw new Error('offline') }
  const result = await settleWorkspaceLoads({ conversations: fail, memories: fail, watches: fail, tasks: fail })
  assert.deepEqual(result.data, {})
  assert.equal(result.failedResources.length, 4)
  assert.deepEqual(updateWorkspaceErrors({}, result, new Set()), {
    conversations: 'unavailable', memories: 'unavailable', watches: 'unavailable', tasks: 'unavailable',
  })
})

test('cached sections become stale; initial failures remain unavailable', () => {
  const errors = updateWorkspaceErrors({}, { data: {}, failedResources: ['memories', 'tasks'] }, new Set<WorkspaceResource>(['memories']))
  assert.deepEqual(errors, { memories: 'stale', tasks: 'unavailable' })
})

test('retry recovery clears only the recovered section error', async () => {
  const result = await settleWorkspaceLoads({ memories: async () => [] })
  assert.deepEqual(result.data, { memories: [] })
  assert.deepEqual(result.failedResources, [])
  assert.deepEqual(updateWorkspaceErrors({ memories: 'unavailable', tasks: 'stale' }, result, new Set()), { tasks: 'stale' })
})

test('failed refreshes do not overwrite cached data with empty arrays', async () => {
  const result = await settleWorkspaceLoads({ memories: async () => { throw new Error('offline') }, tasks: async () => [] })
  const cached = { memories: [{ id: 'existing-memory' }], tasks: [{ id: 'old-task' }] }
  const merged = { ...cached, ...result.data }
  assert.deepEqual(merged.memories, cached.memories)
  assert.deepEqual(merged.tasks, [])
})
