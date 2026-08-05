import {
  workspaceResources,
  type WorkspaceResource,
} from "../features/workspace/workspaceTypes.js"

export type RealtimeServerMessage = {
  type: string
  id?: string
  text?: string
  error?: string
  awaitingConfirmation?: boolean
  resources?: WorkspaceResource[]
}

const workspaceResourceSet = new Set<WorkspaceResource>(workspaceResources)

function parseWorkspaceResources(value: unknown): WorkspaceResource[] | undefined {
  if (!Array.isArray(value)) {
    return undefined
  }
  const resources = value.filter(
    (resource): resource is WorkspaceResource =>
      typeof resource === "string" &&
      workspaceResourceSet.has(resource as WorkspaceResource),
  )
  return resources.length > 0 ? [...new Set(resources)] : undefined
}

export function parseRealtimeServerMessage(
  data: unknown,
): RealtimeServerMessage | undefined {
  if (typeof data !== "string") {
    return undefined
  }
  try {
    const value: unknown = JSON.parse(data)
    if (
      typeof value !== "object" ||
      value === null ||
      !("type" in value) ||
      typeof value.type !== "string"
    ) {
      return undefined
    }
    return {
      type: value.type,
      id: "id" in value && typeof value.id === "string" ? value.id : undefined,
      text: "text" in value && typeof value.text === "string"
        ? value.text
        : undefined,
      error: "error" in value && typeof value.error === "string"
        ? value.error
        : undefined,
      awaitingConfirmation:
        "awaiting_confirmation" in value &&
        typeof value.awaiting_confirmation === "boolean"
          ? value.awaiting_confirmation
          : undefined,
      resources:
        "resources" in value
          ? parseWorkspaceResources(value.resources)
          : undefined,
    }
  } catch {
    return undefined
  }
}
