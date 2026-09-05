import type { WorkspaceData, WorkspaceResource } from './workspaceTypes.js'

export type WorkspaceLoadResult = {
  data: Partial<WorkspaceData>
  failedResources: WorkspaceResource[]
}

export type WorkspaceErrors = Partial<Record<WorkspaceResource, 'unavailable' | 'stale'>>
type Loaders = { [K in WorkspaceResource]?: () => Promise<WorkspaceData[K]> }

// A broken endpoint must not discard successful sibling requests.
export async function settleWorkspaceLoads(loaders: Loaders): Promise<WorkspaceLoadResult> {
  const data: Partial<WorkspaceData> = {}
  const resources = Object.keys(loaders) as WorkspaceResource[]
  const results = await Promise.allSettled(resources.map(async (resource) => {
    const value = await loaders[resource]!()
    Object.assign(data, { [resource]: value })
  }))
  return {
    data,
    failedResources: resources.filter((_, index) => results[index].status === 'rejected'),
  }
}

export function updateWorkspaceErrors(
  current: WorkspaceErrors,
  result: WorkspaceLoadResult,
  loaded: ReadonlySet<WorkspaceResource>,
): WorkspaceErrors {
  const next = { ...current }
  for (const resource of Object.keys(result.data) as WorkspaceResource[]) {
    delete next[resource]
  }
  for (const resource of result.failedResources) {
    next[resource] = loaded.has(resource) ? 'stale' : 'unavailable'
  }
  return next
}
