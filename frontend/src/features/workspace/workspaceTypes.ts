export type WorkspaceView =
  | 'now'
  | 'conversations'
  | 'memories'
  | 'watches'
  | 'tasks'

export const workspaceResources = [
  'conversations',
  'memories',
  'watches',
  'tasks',
] as const

export type WorkspaceResource = (typeof workspaceResources)[number]

export type AutomationKind = 'reminder' | 'watch'

export type ProposalDecision = 'accepted' | 'rejected'

export type Speaker = 'user' | 'assistant' | 'unknown'

export type TranscriptItem = {
  id: string
  speaker: Speaker
  text: string
  startedAt: string
}

export type ConversationItem = {
  id: string
  title: string
  summary: string
  status: 'active' | 'ended' | 'expired'
  startedAt: string
  lastActivityAt: string
  transcript: TranscriptItem[]
}

export type MemoryKind =
  | 'fact'
  | 'preference'
  | 'relationship'
  | 'event'
  | 'goal'
  | 'instruction'

export type MemoryItem = {
  id: string
  title: string
  summary: string
  topics: string[]
  kind: MemoryKind
  status: 'active' | 'superseded' | 'forgotten'
  createdAt: string
  updatedAt: string
}

export type WatchItem = {
  id: string
  query: string
  condition: string
  intervalMinutes: number
  expiresAt: string
  status: 'proposed' | 'active' | 'rejected' | 'expired'
  createdAt: string
  nextCheckAt?: string
  lastCheckedAt?: string
  seenCount: number
}

export type TaskItem = {
  id: string
  title: string
  schedule: string
  dueAt: string
  status: 'proposed' | 'accepted' | 'rejected'
  createdAt: string
  resolvedAt?: string
}

export type WorkspaceData = {
  conversations: ConversationItem[]
  memories: MemoryItem[]
  watches: WatchItem[]
  tasks: TaskItem[]
}

export type NewMemoryInput = {
  title: string
  summary: string
  kind: MemoryKind
  topic: string
}
