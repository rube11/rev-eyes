import { supabase } from '../../shared/api/supabase'
import type {
  ConversationItem,
  MemoryItem,
  MemoryKind,
  NewMemoryInput,
  TaskItem,
  TranscriptItem,
  WatchItem,
  WorkspaceData,
} from './workspaceTypes'

type SessionRow = {
  id: string
  status: ConversationItem['status']
  started_at: string
  last_activity_at: string
}

type TranscriptRow = {
  id: string
  session_id: string
  speaker: TranscriptItem['speaker']
  text: string
  started_at: string
}

type MemoryRow = {
  id: string
  title: string
  summary: string
  topics: string[]
  kind: MemoryKind
  status: MemoryItem['status']
  created_at: string
  updated_at: string
}

type WatchRow = {
  id: string
  query: string
  condition: string
  interval_minutes: number
  expires_at: string
  status: WatchItem['status']
  created_at: string
  next_check_at: string | null
  last_checked_at: string | null
  seen_urls: string[]
}

type TaskRow = {
  id: string
  title: string
  schedule: string
  due_at: string
  status: TaskItem['status']
  created_at: string
  resolved_at: string | null
}

function shorten(text: string, limit: number): string {
  const normalized = text.replace(/\s+/gu, ' ').trim()
  return normalized.length <= limit
    ? normalized
    : `${normalized.slice(0, limit - 1).trimEnd()}…`
}

function mapMemory(row: MemoryRow): MemoryItem {
  return {
    id: row.id,
    title: row.title,
    summary: row.summary,
    topics: row.topics,
    kind: row.kind,
    status: row.status,
    createdAt: row.created_at,
    updatedAt: row.updated_at,
  }
}

export async function loadWorkspaceData(
  userId: string,
  signal: AbortSignal,
): Promise<WorkspaceData> {
  const [
    sessionsResult,
    transcriptResult,
    memoriesResult,
    watchesResult,
    tasksResult,
  ] = await Promise.all([
    supabase
      .from('sessions')
      .select('id,status,started_at,last_activity_at')
      .eq('user_id', userId)
      .order('last_activity_at', { ascending: false })
      .limit(50)
      .abortSignal(signal),
    supabase
      .from('transcript_utterances')
      .select('id,session_id,speaker,text,started_at')
      .eq('user_id', userId)
      .order('started_at', { ascending: false })
      .limit(1000)
      .abortSignal(signal),
    supabase
      .from('memories')
      .select('id,title,summary,topics,kind,status,created_at,updated_at')
      .eq('user_id', userId)
      .eq('status', 'active')
      .order('updated_at', { ascending: false })
      .limit(200)
      .abortSignal(signal),
    supabase
      .from('watches')
      .select(
        'id,query,condition,interval_minutes,expires_at,status,created_at,next_check_at,last_checked_at,seen_urls',
      )
      .eq('user_id', userId)
      .order('created_at', { ascending: false })
      .limit(50)
      .abortSignal(signal),
    supabase
      .from('task_proposals')
      .select('id,title,schedule,due_at,status,created_at,resolved_at')
      .eq('user_id', userId)
      .order('due_at', { ascending: true })
      .limit(100)
      .abortSignal(signal),
  ])

  const firstError = [
    sessionsResult.error,
    transcriptResult.error,
    memoriesResult.error,
    watchesResult.error,
    tasksResult.error,
  ].find(Boolean)

  if (firstError) {
    throw new Error(firstError.message)
  }

  const transcriptBySession = new Map<string, TranscriptItem[]>()
  for (const row of (transcriptResult.data ?? []) as TranscriptRow[]) {
    const item: TranscriptItem = {
      id: row.id,
      speaker: row.speaker,
      text: row.text,
      startedAt: row.started_at,
    }
    const transcript = transcriptBySession.get(row.session_id) ?? []
    transcript.push(item)
    transcriptBySession.set(row.session_id, transcript)
  }
  for (const transcript of transcriptBySession.values()) {
    transcript.sort(
      (left, right) =>
        new Date(left.startedAt).getTime() -
        new Date(right.startedAt).getTime(),
    )
  }

  const conversations = ((sessionsResult.data ?? []) as SessionRow[]).map(
    (session, index): ConversationItem => {
      const transcript = transcriptBySession.get(session.id) ?? []
      const firstUserLine = transcript.find((line) => line.speaker === 'user')
      const lastAssistantLine = [...transcript]
        .reverse()
        .find((line) => line.speaker === 'assistant')

      return {
        id: session.id,
        title: firstUserLine
          ? shorten(firstUserLine.text, 58)
          : `Conversation ${String(index + 1).padStart(2, '0')}`,
        summary: lastAssistantLine
          ? shorten(lastAssistantLine.text, 118)
          : 'No assistant response was recorded.',
        status: session.status,
        startedAt: session.started_at,
        lastActivityAt: session.last_activity_at,
        transcript,
      }
    },
  )

  const watches = ((watchesResult.data ?? []) as WatchRow[]).map(
    (row): WatchItem => ({
      id: row.id,
      query: row.query,
      condition: row.condition,
      intervalMinutes: row.interval_minutes,
      expiresAt: row.expires_at,
      status: row.status,
      createdAt: row.created_at,
      nextCheckAt: row.next_check_at ?? undefined,
      lastCheckedAt: row.last_checked_at ?? undefined,
      seenCount: row.seen_urls.length,
    }),
  )

  const tasks = ((tasksResult.data ?? []) as TaskRow[]).map(
    (row): TaskItem => ({
      id: row.id,
      title: row.title,
      schedule: row.schedule,
      dueAt: row.due_at,
      status: row.status,
      createdAt: row.created_at,
      resolvedAt: row.resolved_at ?? undefined,
    }),
  )

  return {
    conversations,
    memories: ((memoriesResult.data ?? []) as MemoryRow[]).map(mapMemory),
    watches,
    tasks,
  }
}

export async function saveMemory(
  userId: string,
  input: NewMemoryInput,
): Promise<MemoryItem> {
  const { data, error } = await supabase
    .from('memories')
    .insert({
      user_id: userId,
      title: input.title.trim(),
      summary: input.summary.trim(),
      topics: [input.topic],
      kind: input.kind,
      details: [],
      entities: [],
      status: 'active',
    })
    .select('id,title,summary,topics,kind,status,created_at,updated_at')
    .single()

  if (error) {
    throw new Error(error.message)
  }

  return mapMemory(data as MemoryRow)
}

const minute = 60_000
const hour = 60 * minute
const day = 24 * hour

function fromNow(offset: number): string {
  return new Date(Date.now() + offset).toISOString()
}

export function createDemoWorkspaceData(): WorkspaceData {
  const conversationOne = 'demo-conversation-01'
  const conversationTwo = 'demo-conversation-02'
  const conversationThree = 'demo-conversation-03'

  return {
    conversations: [
      {
        id: conversationOne,
        title: 'Get the beta ready for Noah',
        summary:
          'The beta build needs a final device check before it goes to Noah this afternoon.',
        status: 'active',
        startedAt: fromNow(-72 * minute),
        lastActivityAt: fromNow(-64 * minute),
        transcript: [
          {
            id: 'demo-line-01',
            speaker: 'user',
            text: 'What still needs to happen before I send the beta to Noah?',
            startedAt: fromNow(-72 * minute),
          },
          {
            id: 'demo-line-02',
            speaker: 'assistant',
            text: 'Run the device check, verify the production API URL, and package the beta build. I can remind you to send it at 3:00 PM.',
            startedAt: fromNow(-71 * minute),
          },
          {
            id: 'demo-line-03',
            speaker: 'user',
            text: 'Yes, remind me at three.',
            startedAt: fromNow(-65 * minute),
          },
          {
            id: 'demo-line-04',
            speaker: 'assistant',
            text: 'Done. I’ll remind you to send the beta build to Noah at 3:00 PM.',
            startedAt: fromNow(-64 * minute),
          },
        ],
      },
      {
        id: conversationTwo,
        title: 'Maya’s presentation',
        summary:
          'Maya presents the Arcline roadmap on Monday. A reminder was suggested for the morning.',
        status: 'ended',
        startedAt: fromNow(-day - 37 * minute),
        lastActivityAt: fromNow(-day - 28 * minute),
        transcript: [
          {
            id: 'demo-line-05',
            speaker: 'user',
            text: 'Maya’s big product presentation is Monday morning.',
            startedAt: fromNow(-day - 37 * minute),
          },
          {
            id: 'demo-line-06',
            speaker: 'assistant',
            text: 'I’ll remember that Maya is presenting the Arcline product roadmap on Monday morning.',
            startedAt: fromNow(-day - 36 * minute),
          },
          {
            id: 'demo-line-07',
            speaker: 'user',
            text: 'I should call her before it starts.',
            startedAt: fromNow(-day - 29 * minute),
          },
          {
            id: 'demo-line-08',
            speaker: 'assistant',
            text: 'Want me to remind you to call Maya at 8:30 AM on Monday?',
            startedAt: fromNow(-day - 28 * minute),
          },
        ],
      },
      {
        id: conversationThree,
        title: 'Find a better flight to Seattle',
        summary:
          'Watching nonstop SFO to SEA fares and looking for a price below $180.',
        status: 'ended',
        startedAt: fromNow(-2 * day - 3 * hour),
        lastActivityAt: fromNow(-2 * day - 2.8 * hour),
        transcript: [
          {
            id: 'demo-line-09',
            speaker: 'user',
            text: 'Keep an eye on nonstop flights from SFO to Seattle next month. Tell me if one drops below $180.',
            startedAt: fromNow(-2 * day - 3 * hour),
          },
          {
            id: 'demo-line-10',
            speaker: 'assistant',
            text: 'I can check every six hours through next Friday and let you know when a nonstop fare drops below $180.',
            startedAt: fromNow(-2 * day - 2.9 * hour),
          },
          {
            id: 'demo-line-11',
            speaker: 'user',
            text: 'Do that.',
            startedAt: fromNow(-2 * day - 2.8 * hour),
          },
        ],
      },
    ],
    memories: [
      {
        id: 'demo-memory-01',
        title: 'Maya leads product at Arcline',
        summary:
          'Maya is a close friend and leads product at Arcline. Her roadmap presentation is Monday morning.',
        topics: ['friends', 'work'],
        kind: 'relationship',
        status: 'active',
        createdAt: fromNow(-day),
        updatedAt: fromNow(-day),
      },
      {
        id: 'demo-memory-02',
        title: 'Prefers aisle seats',
        summary:
          'Choose an aisle seat when comparing or booking flights, especially for trips longer than two hours.',
        topics: ['preferences'],
        kind: 'preference',
        status: 'active',
        createdAt: fromNow(-11 * day),
        updatedAt: fromNow(-11 * day),
      },
      {
        id: 'demo-memory-03',
        title: 'Rev-eyes beta target',
        summary:
          'The current goal is to get the rev-eyes beta onto Noah’s glasses before the end of July.',
        topics: ['work', 'goals'],
        kind: 'goal',
        status: 'active',
        createdAt: fromNow(-16 * day),
        updatedAt: fromNow(-3 * day),
      },
      {
        id: 'demo-memory-04',
        title: 'Keep walking answers brief',
        summary:
          'When the user is walking, answers should be short enough to scan comfortably on the glasses.',
        topics: ['preferences'],
        kind: 'instruction',
        status: 'active',
        createdAt: fromNow(-29 * day),
        updatedAt: fromNow(-8 * day),
      },
    ],
    watches: [
      {
        id: 'demo-watch-01',
        query: 'Even G2 SDK release notes',
        condition: 'A stable version newer than 0.0.12 is published',
        intervalMinutes: 60,
        expiresAt: fromNow(12 * day),
        status: 'active',
        createdAt: fromNow(-4 * day),
        nextCheckAt: fromNow(18 * minute),
        lastCheckedAt: fromNow(-42 * minute),
        seenCount: 6,
      },
      {
        id: 'demo-watch-02',
        query: 'Nonstop SFO to SEA fares',
        condition: 'A fare for next month drops below $180',
        intervalMinutes: 360,
        expiresAt: fromNow(7 * day),
        status: 'active',
        createdAt: fromNow(-2 * day),
        nextCheckAt: fromNow(94 * minute),
        lastCheckedAt: fromNow(-4.4 * hour),
        seenCount: 14,
      },
      {
        id: 'demo-watch-03',
        query: 'Arcline product announcement',
        condition: 'A public roadmap or launch announcement appears',
        intervalMinutes: 720,
        expiresAt: fromNow(21 * day),
        status: 'proposed',
        createdAt: fromNow(-26 * minute),
        seenCount: 0,
      },
    ],
    tasks: [
      {
        id: 'demo-task-01',
        title: 'Send the beta build to Noah',
        schedule: 'today at 3:00 PM',
        dueAt: fromNow(3.2 * hour),
        status: 'accepted',
        createdAt: fromNow(-64 * minute),
        resolvedAt: fromNow(-63 * minute),
      },
      {
        id: 'demo-task-02',
        title: 'Book a dentist appointment',
        schedule: 'tomorrow at 9:00 AM',
        dueAt: fromNow(21 * hour),
        status: 'proposed',
        createdAt: fromNow(-18 * minute),
      },
      {
        id: 'demo-task-03',
        title: 'Call Maya before her presentation',
        schedule: 'Monday at 8:30 AM',
        dueAt: fromNow(2.8 * day),
        status: 'accepted',
        createdAt: fromNow(-day),
        resolvedAt: fromNow(-day + minute),
      },
      {
        id: 'demo-task-04',
        title: 'Check in for the Seattle flight',
        schedule: 'August 14 at 10:20 AM',
        dueAt: fromNow(20 * day),
        status: 'accepted',
        createdAt: fromNow(-5 * day),
        resolvedAt: fromNow(-5 * day + minute),
      },
    ],
  }
}
