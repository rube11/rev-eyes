import type { MemoryEdit, MemoryItem, MemoryKind, MemoryLayer } from './workspaceTypes.js'

/** Mirrors the backend profile projection: pinned first, then by kind, then newest observation. */
const kindRank: Record<MemoryKind, number> = {
  instruction: 0,
  goal: 1,
  relationship: 2,
  fact: 3,
  preference: 4,
  event: 4,
}

export const profileLimit = 16
export const staleAfterMs = 90 * 24 * 60 * 60 * 1000
export const shelfOrder: MemoryLayer[] = ['core', 'recent', 'detail']
export const shelfLabels: Record<MemoryLayer, string> = {
  core: 'Core',
  recent: 'Recent',
  detail: 'Everything else',
}

/** Human labels for the canonical memory_key families the extractor produces. */
export const familyLabels: Record<string, string> = {
  relationship: 'People',
  role: 'Role',
  nutrition: 'Nutrition',
  food: 'Food',
  health: 'Health',
  activity: 'Right now',
}

/** The family segment of a canonical key such as profile.relationship.maya or state.activity.current. */
export function familyOf(memory: MemoryItem): string | undefined {
  const parts = memory.memoryKey?.split('.') ?? []
  if (parts.length < 3) return undefined
  if (parts[0] !== 'profile' && parts[0] !== 'state') return undefined
  return parts[1]
}

export function familyLabel(family: string): string {
  return familyLabels[family] ?? family.charAt(0).toUpperCase() + family.slice(1).replace(/_/gu, ' ')
}

export function isActiveMemory(memory: MemoryItem, now: number): boolean {
  return memory.status === 'active' && (!memory.expiresAt || Date.parse(memory.expiresAt) > now)
}

function summaryKey(memory: MemoryItem): string {
  return memory.summary.replace(/\s+/gu, ' ').trim().toLowerCase()
}

export function profileEntries(memories: MemoryItem[], now: number): MemoryItem[] {
  const seen = new Set<string>()
  return memories
    .filter((memory) => isActiveMemory(memory, now) && memory.layer === 'core')
    .sort((a, b) =>
      Number(b.pinned) - Number(a.pinned) ||
      kindRank[a.kind] - kindRank[b.kind] ||
      Date.parse(b.observedAt) - Date.parse(a.observedAt) ||
      a.id.localeCompare(b.id))
    .filter((memory) => {
      const key = summaryKey(memory)
      if (!key || seen.has(key)) return false
      seen.add(key)
      return true
    })
    .slice(0, profileLimit)
}

export function recentEntries(memories: MemoryItem[], now: number): MemoryItem[] {
  return memories
    .filter((memory) => isActiveMemory(memory, now) && memory.layer === 'recent' && memory.expiresAt)
    .sort((a, b) => Date.parse(a.expiresAt!) - Date.parse(b.expiresAt!))
}

/** Durable beliefs Eyes has not heard about in a while and the user never pinned. */
export function isStaleMemory(memory: MemoryItem, now: number): boolean {
  return isActiveMemory(memory, now)
    && !memory.pinned
    && memory.layer !== 'recent'
    && (memory.kind === 'preference' || memory.kind === 'relationship'
      || memory.kind === 'instruction' || memory.kind === 'goal')
    && now - Date.parse(memory.updatedAt) > staleAfterMs
}

export function applyMemoryEdit(memory: MemoryItem, edit: MemoryEdit, at: string): MemoryItem {
  switch (edit.action) {
    case 'forget':
      return { ...memory, status: 'forgotten', inactiveAt: at, updatedAt: at }
    case 'restore':
      return { ...memory, status: 'active', inactiveAt: undefined, updatedAt: at }
    case 'pin':
      return { ...memory, pinned: true, layer: 'core', updatedAt: at }
    case 'unpin':
      return { ...memory, pinned: false, layer: memory.assignedLayer, updatedAt: at }
    case 'update':
      return { ...memory, title: edit.title.trim(), summary: edit.summary.trim(), updatedAt: at }
  }
}
