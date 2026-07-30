import { useEffect, useMemo, useState } from 'react'
import type { FormEvent } from 'react'

import type {
  AutomationKind,
  MemoryKind,
  NewMemoryInput,
  ProposalDecision,
  TaskItem,
  WatchItem,
  WorkspaceData,
  WorkspaceView,
} from './workspaceTypes'

type WorkspaceProps = {
  data: WorkspaceData
  email: string
  glassesStatus: string
  latestResponse: string
  dataError?: string
  isDemo: boolean
  onCreateMemory: (input: NewMemoryInput) => Promise<void>
  onDeleteAutomation: (
    kind: AutomationKind,
    resourceId: string,
  ) => Promise<void>
  onResolveAutomation: (
    kind: AutomationKind,
    resourceId: string,
    decision: ProposalDecision,
  ) => Promise<void>
  onSignOut: () => void
}

type NavItem = {
  id: WorkspaceView
  label: string
}

const navItems: NavItem[] = [
  { id: 'now', label: 'Home' },
  { id: 'conversations', label: 'Conversations' },
  { id: 'memories', label: 'Memories' },
  { id: 'watches', label: 'Watches' },
  { id: 'tasks', label: 'Tasks' },
]

const viewTitles: Record<WorkspaceView, string> = {
  now: 'Home',
  conversations: 'Conversations',
  memories: 'Memories',
  watches: 'Watches',
  tasks: 'Tasks',
}

const memoryKinds: MemoryKind[] = [
  'fact',
  'preference',
  'relationship',
  'event',
  'goal',
  'instruction',
]

const memoryTopics = [
  'work',
  'personal',
  'friends',
  'family',
  'relationships',
  'health',
  'preferences',
  'goals',
  'places',
  'other',
]

function isWorkspaceView(value: string): value is WorkspaceView {
  return navItems.some((item) => item.id === value)
}

function initialView(): WorkspaceView {
  const hash = window.location.hash.replace(/^#/u, '')
  return isWorkspaceView(hash) ? hash : 'now'
}

function formatClock(value: Date): string {
  return new Intl.DateTimeFormat(undefined, {
    hour: 'numeric',
    minute: '2-digit',
  }).format(value)
}

function formatDay(value: Date): string {
  return new Intl.DateTimeFormat(undefined, {
    weekday: 'short',
    month: 'short',
    day: 'numeric',
  }).format(value)
}

function formatDateTime(value: string): string {
  return new Intl.DateTimeFormat(undefined, {
    month: 'short',
    day: 'numeric',
    hour: 'numeric',
    minute: '2-digit',
  }).format(new Date(value))
}

function formatDate(value: string): string {
  return new Intl.DateTimeFormat(undefined, {
    month: 'short',
    day: 'numeric',
    year: 'numeric',
  }).format(new Date(value))
}

function relativeTime(value: string): string {
  const difference = new Date(value).getTime() - Date.now()
  const absolute = Math.abs(difference)
  const formatter = new Intl.RelativeTimeFormat(undefined, { numeric: 'auto' })

  if (absolute >= 86_400_000) {
    return formatter.format(Math.round(difference / 86_400_000), 'day')
  }
  if (absolute >= 3_600_000) {
    return formatter.format(Math.round(difference / 3_600_000), 'hour')
  }
  if (absolute >= 60_000) {
    return formatter.format(Math.round(difference / 60_000), 'minute')
  }
  return 'just now'
}

function formatInterval(minutes: number): string {
  if (minutes < 60) {
    return `${minutes} min`
  }
  if (minutes % 60 === 0) {
    const hours = minutes / 60
    return `${hours} ${hours === 1 ? 'hour' : 'hours'}`
  }
  return `${Math.floor(minutes / 60)}h ${minutes % 60}m`
}

function shorten(value: string, limit: number): string {
  const text = value.replace(/\s+/gu, ' ').trim()
  return text.length <= limit ? text : `${text.slice(0, limit - 1).trimEnd()}…`
}

function connectedFromStatus(status: string): boolean {
  const normalized = status.toLowerCase().trim()
  return (
    normalized.length > 0 &&
    !['connecting', 'reconnecting', 'disconnected', 'offline'].includes(
      normalized,
    )
  )
}

function friendlyDeviceStatus(status: string): string {
  const normalized = status.toLowerCase()
  if (normalized === 'listening') {
    return 'Listening'
  }
  if (normalized === 'sleeping') {
    return 'Standby'
  }
  if (normalized === 'thinking') {
    return 'Thinking'
  }
  if (normalized === 'starting microphone') {
    return 'Starting microphone'
  }
  if (normalized === 'microphone unavailable') {
    return 'Mic unavailable'
  }
  if (normalized === 'glasses command failed') {
    return 'Needs attention'
  }
  if (normalized === 'connected') {
    return 'Connected'
  }
  if (normalized.includes('connect') && !normalized.includes('disconnect')) {
    return 'Connecting'
  }
  if (normalized === 'disconnected' || normalized === 'offline') {
    return 'Offline'
  }
  return 'Needs attention'
}

function NavIcon({ view }: { view: WorkspaceView }) {
  const common = {
    width: 20,
    height: 20,
    viewBox: '0 0 24 24',
    fill: 'none',
    stroke: 'currentColor',
    strokeWidth: 1.7,
    strokeLinecap: 'round' as const,
    strokeLinejoin: 'round' as const,
    'aria-hidden': true,
  }

  if (view === 'now') {
    return (
      <svg {...common}>
        <path d="M3.5 10.5 12 3l8.5 7.5" />
        <path d="M5.5 9.5V21h13V9.5M9.5 21v-6h5v6" />
      </svg>
    )
  }
  if (view === 'conversations') {
    return (
      <svg {...common}>
        <path d="M4 5.5h16v11H9l-5 4v-15Z" />
        <path d="M8 9h8M8 13h5" />
      </svg>
    )
  }
  if (view === 'memories') {
    return (
      <svg {...common}>
        <path d="M12 20.5c4.2-2.7 7-6 7-10.2A4.8 4.8 0 0 0 14.2 5c-1 0-1.8.3-2.2 1-.4-.7-1.2-1-2.2-1A4.8 4.8 0 0 0 5 10.3c0 4.2 2.8 7.5 7 10.2Z" />
      </svg>
    )
  }
  if (view === 'watches') {
    return (
      <svg {...common}>
        <path d="M2.8 12s3.4-5.5 9.2-5.5 9.2 5.5 9.2 5.5-3.4 5.5-9.2 5.5S2.8 12 2.8 12Z" />
        <circle cx="12" cy="12" r="2.4" />
      </svg>
    )
  }
  return (
    <svg {...common}>
      <path d="M7 3.5v3M17 3.5v3M4 9h16" />
      <rect x="4" y="5.5" width="16" height="15" rx="2" />
      <path d="m8.5 14 2 2 4.5-5" />
    </svg>
  )
}

function StatusMark({ active = false }: { active?: boolean }) {
  return (
    <span
      className={`status-mark${active ? ' status-mark--active' : ''}`}
      aria-hidden="true"
    />
  )
}

function EmptyState({
  title,
  body,
}: {
  title: string
  body: string
}) {
  return (
    <div className="empty-state">
      <span className="empty-state__rule" aria-hidden="true" />
      <h3>{title}</h3>
      <p>{body}</p>
    </div>
  )
}

function PageIntro({
  eyebrow,
  title,
  description,
  action,
}: {
  eyebrow: string
  title: string
  description: string
  action?: React.ReactNode
}) {
  return (
    <header className="page-intro">
      <div>
        <p className="section-label">{eyebrow}</p>
        <h1>{title}</h1>
        <p className="page-intro__description">{description}</p>
      </div>
      {action ? <div className="page-intro__action">{action}</div> : null}
    </header>
  )
}

type DaybookEntry = {
  id: string
  view: Exclude<WorkspaceView, 'now'>
  label: string
  title: string
  body: string
  timestamp: string
}

function formatDaybookDate(value: string, currentTime: Date): string {
  const date = new Date(value)
  const currentDay = new Date(
    currentTime.getFullYear(),
    currentTime.getMonth(),
    currentTime.getDate(),
  ).getTime()
  const entryDay = new Date(
    date.getFullYear(),
    date.getMonth(),
    date.getDate(),
  ).getTime()
  const difference = Math.round((entryDay - currentDay) / 86_400_000)

  if (difference === 0) return 'Today'
  if (difference === -1) return 'Yesterday'
  if (difference === 1) return 'Tomorrow'

  return new Intl.DateTimeFormat(undefined, {
    weekday: 'short',
    month: 'short',
    day: 'numeric',
  }).format(date)
}

function DaybookRow({
  entry,
  currentTime,
  onNavigate,
}: {
  entry: DaybookEntry
  currentTime: Date
  onNavigate: (view: WorkspaceView) => void
}) {
  return (
    <button
      className="home-daybook-row"
      type="button"
      onClick={() => onNavigate(entry.view)}
    >
      <time dateTime={entry.timestamp}>
        <strong>{formatClock(new Date(entry.timestamp))}</strong>
        <span>{formatDaybookDate(entry.timestamp, currentTime)}</span>
      </time>
      <span className="home-daybook-row__content">
        <small>{entry.label}</small>
        <strong>{entry.title}</strong>
        <span>{shorten(entry.body, 120)}</span>
      </span>
      <span className="home-daybook-row__arrow" aria-hidden="true">
        →
      </span>
    </button>
  )
}

function NowView({
  data,
  onNavigate,
  onAddMemory,
  currentTime,
}: {
  data: WorkspaceData
  onNavigate: (view: WorkspaceView) => void
  onAddMemory: () => void
  currentTime: Date
}) {
  const nowTime = currentTime.getTime()
  const activeWatches = data.watches.filter((watch) => watch.status === 'active')
  const proposedTasks = data.tasks
    .filter((task) => task.status === 'proposed')
    .sort(
      (left, right) =>
        new Date(right.createdAt).getTime() -
        new Date(left.createdAt).getTime(),
    )
  const confirmedTasks = data.tasks.filter(
    (task) => task.status === 'accepted',
  )
  const pastDueTasks = confirmedTasks
    .filter((task) => new Date(task.dueAt).getTime() < nowTime)
    .sort(
      (left, right) =>
        new Date(right.dueAt).getTime() - new Date(left.dueAt).getTime(),
    )
  const upcomingTasks = confirmedTasks
    .filter((task) => new Date(task.dueAt).getTime() >= nowTime)
    .sort(
      (left, right) =>
        new Date(left.dueAt).getTime() - new Date(right.dueAt).getTime(),
    )
  const recentMemories = [...data.memories].sort(
    (left, right) =>
      new Date(right.updatedAt).getTime() -
      new Date(left.updatedAt).getTime(),
  )
  const recentConversations = [...data.conversations]
    .sort(
      (left, right) =>
        new Date(right.lastActivityAt).getTime() -
        new Date(left.lastActivityAt).getTime(),
    )
    .slice(0, 4)

  const overdueTask = pastDueTasks[0]
  const nextTask = upcomingTasks[0]
  let focusLabel = 'Daily docket'
  let focusTitle =
    String(data.conversations.length + data.memories.length) + ' saved items'
  let focusMeta = 'Nothing needs immediate attention.'
  let focusView: Exclude<WorkspaceView, 'now'> | undefined
  let focusActionLabel = ''

  if (overdueTask) {
    focusLabel = 'Past due · ' + relativeTime(overdueTask.dueAt)
    focusTitle = overdueTask.title
    focusMeta = 'Scheduled for ' + formatDateTime(overdueTask.dueAt)
    focusView = 'tasks'
    focusActionLabel = 'Open reminder'
  } else if (proposedTasks.length) {
    focusLabel = 'Needs a decision'
    focusTitle =
      String(proposedTasks.length) +
      ' suggested ' +
      (proposedTasks.length === 1 ? 'task' : 'tasks')
    focusMeta = proposedTasks[0].title
    focusView = 'tasks'
    focusActionLabel = 'Review tasks'
  } else if (nextTask) {
    focusLabel = 'Next reminder'
    focusTitle = nextTask.title
    focusMeta =
      formatDateTime(nextTask.dueAt) + ' · ' + relativeTime(nextTask.dueAt)
    focusView = 'tasks'
    focusActionLabel = 'Open reminder'
  } else if (activeWatches.length) {
    focusLabel = 'Active watch'
    focusTitle = activeWatches[0].query
    focusMeta = activeWatches[0].condition
    focusView = 'watches'
    focusActionLabel = 'View watch'
  }

  const daybookEntries: DaybookEntry[] = [
    ...proposedTasks.slice(0, 1).map(
      (task): DaybookEntry => ({
        id: 'proposal-' + task.id,
        view: 'tasks',
        label: 'Needs review',
        title: task.title,
        body: task.schedule,
        timestamp: task.createdAt,
      }),
    ),
    ...upcomingTasks.slice(0, 1).map(
      (task): DaybookEntry => ({
        id: 'task-' + task.id,
        view: 'tasks',
        label: 'Scheduled reminder',
        title: task.title,
        body: task.schedule,
        timestamp: task.dueAt,
      }),
    ),
    ...recentMemories.slice(0, 1).map(
      (memory): DaybookEntry => ({
        id: 'memory-' + memory.id,
        view: 'memories',
        label: 'Context updated',
        title: memory.title,
        body: memory.summary,
        timestamp: memory.updatedAt,
      }),
    ),
    ...activeWatches.slice(0, 1).map(
      (watch): DaybookEntry => ({
        id: 'watch-' + watch.id,
        view: 'watches',
        label: watch.nextCheckAt ? 'Next watch check' : 'Active watch',
        title: watch.query,
        body: watch.condition,
        timestamp: watch.nextCheckAt ?? watch.createdAt,
      }),
    ),
  ].sort(
    (left, right) =>
      new Date(left.timestamp).getTime() -
      new Date(right.timestamp).getTime(),
  )

  const daybookSummary =
    String(pastDueTasks.length).padStart(2, '0') +
    ' past due · ' +
    String(proposedTasks.length).padStart(2, '0') +
    ' need review · ' +
    String(activeWatches.length).padStart(2, '0') +
    ' active watches'

  const longDay = new Intl.DateTimeFormat(undefined, {
    weekday: 'long',
    month: 'long',
    day: 'numeric',
  }).format(currentTime)

  return (
    <div className="home home--daybook">
      <section className="home-docket" aria-labelledby="home-title">
        <span className="home-docket__index" aria-hidden="true">
          01
        </span>
        <div className="home-docket__heading">
          <p className="section-label">{focusLabel}</p>
          <h1 id="home-title">{focusTitle}</h1>
          <p>{focusMeta}</p>
        </div>
        {focusView ? (
          <button
            className="home-command home-command--primary"
            type="button"
            onClick={() => onNavigate(focusView)}
          >
            {focusActionLabel}
            <span aria-hidden="true">→</span>
          </button>
        ) : null}
      </section>

      <section className="home-daybook" aria-labelledby="home-daybook-title">
        <header className="home-daybook__date">
          <div>
            <p className="section-label">Today</p>
            <h2 id="home-daybook-title">{longDay}</h2>
          </div>
          <p>{daybookSummary}</p>
        </header>

        <div className="home-daybook__planner">
          {daybookEntries.length > 0 ? (
            daybookEntries.map((entry) => (
              <DaybookRow
                key={entry.id}
                entry={entry}
                currentTime={currentTime}
                onNavigate={onNavigate}
              />
            ))
          ) : (
            <p className="home-daybook__clear">No scheduled context today.</p>
          )}
          <div className="home-daybook__add">
            <span className="section-label">Later</span>
            <button className="home-command" type="button" onClick={onAddMemory}>
              <span aria-hidden="true">＋</span>
              Add something to remember
            </button>
          </div>
        </div>
      </section>

      <section className="home-margins" aria-labelledby="home-margins-title">
        <header className="home-margins__head">
          <div>
            <p className="section-label">Yesterday's margins</p>
            <h2 id="home-margins-title">Loose context</h2>
          </div>
          <span>{recentConversations.length} recent conversations</span>
        </header>
        <div className="home-margins__list">
          {recentConversations.length > 0 ? (
            recentConversations.map((conversation) => (
              <button
                className="home-margin-row"
                type="button"
                key={conversation.id}
                onClick={() => onNavigate('conversations')}
              >
                <time dateTime={conversation.lastActivityAt}>
                  {relativeTime(conversation.lastActivityAt)}
                </time>
                <span>
                  <strong>{conversation.title}</strong>
                  <span>{shorten(conversation.summary, 150)}</span>
                </span>
                <span aria-hidden="true">→</span>
              </button>
            ))
          ) : (
            <p className="home-margins__empty">No loose context yet.</p>
          )}
        </div>
      </section>
    </div>
  )
}

function ConversationsView({ data }: { data: WorkspaceData }) {
  const [query, setQuery] = useState('')
  const [selectedId, setSelectedId] = useState(
    data.conversations[0]?.id ?? '',
  )

  const filtered = useMemo(() => {
    const normalized = query.toLowerCase().trim()
    if (!normalized) {
      return data.conversations
    }
    return data.conversations.filter((conversation) =>
      [
        conversation.title,
        conversation.summary,
        ...conversation.transcript.map((line) => line.text),
      ]
        .join(' ')
        .toLowerCase()
        .includes(normalized),
    )
  }, [data.conversations, query])

  const selected =
    filtered.find((conversation) => conversation.id === selectedId) ??
    filtered[0]

  return (
    <>
      <PageIntro
        eyebrow="History"
        title="Conversations"
        description="Search and revisit what you and your assistant talked about."
      />
      <div className="browser-layout conversation-browser">
        <section className="browser-index" aria-label="Conversation list">
          <label className="search-field">
            <span>Search conversations</span>
            <input
              type="search"
              value={query}
              onChange={(event) => setQuery(event.target.value)}
              placeholder="Type to filter…"
            />
            <kbd>/</kbd>
          </label>
          <p className="result-count">
            {filtered.length}{' '}
            {filtered.length === 1 ? 'conversation' : 'conversations'}
          </p>
          <div className="index-list">
            {filtered.map((conversation) => (
              <button
                className={`index-row${
                  selected?.id === conversation.id ? ' is-selected' : ''
                }`}
                type="button"
                key={conversation.id}
                onClick={() => setSelectedId(conversation.id)}
              >
                <span className="index-row__date">
                  {formatDateTime(conversation.lastActivityAt)}
                </span>
                <strong>{conversation.title}</strong>
                <span>{conversation.summary}</span>
              </button>
            ))}
          </div>
          {filtered.length === 0 ? (
            <EmptyState
              title="No matching conversation"
              body="Try another word or phrase."
            />
          ) : null}
        </section>

        <section className="browser-detail transcript-detail">
          {selected ? (
            <>
              <header className="detail-header">
                <p className="section-label">
                  {formatDateTime(selected.startedAt)}
                </p>
                <h2>{selected.title}</h2>
                <div className="detail-meta">
                  <span>
                    <StatusMark active={selected.status === 'active'} />
                    {selected.status}
                  </span>
                  <span>{selected.transcript.length} turns</span>
                </div>
              </header>
              <div className="transcript">
                {selected.transcript.length > 0 ? (
                  selected.transcript.map((line) => (
                    <article
                      className={`transcript-line transcript-line--${line.speaker}`}
                      key={line.id}
                    >
                      <div className="transcript-line__speaker">
                        <span>
                          {line.speaker === 'user'
                            ? 'YOU'
                            : line.speaker === 'assistant'
                              ? 'REV'
                              : '—'}
                        </span>
                        <time dateTime={line.startedAt}>
                          {formatClock(new Date(line.startedAt))}
                        </time>
                      </div>
                      <p>{line.text}</p>
                    </article>
                  ))
                ) : (
                  <EmptyState
                    title="Nothing was saved"
                    body="There is no conversation history for this moment."
                  />
                )}
              </div>
            </>
          ) : (
            <EmptyState
              title="No conversations yet"
              body="Your conversations with the assistant will appear here."
            />
          )}
        </section>
      </div>
    </>
  )
}

function MemoriesView({
  data,
  onAdd,
}: {
  data: WorkspaceData
  onAdd: () => void
}) {
  const [query, setQuery] = useState('')
  const [filter, setFilter] = useState('all')
  const [selectedId, setSelectedId] = useState(data.memories[0]?.id ?? '')

  const filters = useMemo(() => {
    const topics = new Set(data.memories.flatMap((memory) => memory.topics))
    return ['all', ...Array.from(topics).slice(0, 5)]
  }, [data.memories])

  const filtered = useMemo(() => {
    const normalized = query.toLowerCase().trim()
    return data.memories.filter((memory) => {
      const matchesFilter =
        filter === 'all' ||
        memory.topics.includes(filter) ||
        memory.kind === filter
      const matchesQuery =
        !normalized ||
        [memory.title, memory.summary, memory.kind, ...memory.topics]
          .join(' ')
          .toLowerCase()
          .includes(normalized)
      return matchesFilter && matchesQuery
    })
  }, [data.memories, filter, query])

  const selected =
    filtered.find((memory) => memory.id === selectedId) ?? filtered[0]

  return (
    <>
      <PageIntro
        eyebrow="What your assistant knows"
        title="Memories"
        description="Personal details, preferences, people, and goals you want remembered."
        action={
          <button className="primary-action" type="button" onClick={onAdd}>
            <span aria-hidden="true">＋</span> Add memory
          </button>
        }
      />
      <div className="filter-bar">
        <label className="search-field search-field--wide">
          <span>Search memories</span>
          <input
            type="search"
            value={query}
            onChange={(event) => setQuery(event.target.value)}
            placeholder="Person, place, preference…"
          />
        </label>
        <div className="filter-set" aria-label="Filter memories">
          {filters.map((item) => (
            <button
              className={filter === item ? 'is-active' : ''}
              type="button"
              key={item}
              onClick={() => setFilter(item)}
            >
              {item}
            </button>
          ))}
        </div>
      </div>

      <div className="browser-layout memory-browser">
        <section className="memory-list" aria-label="Memory list">
          <p className="result-count">
            {filtered.length} active{' '}
            {filtered.length === 1 ? 'memory' : 'memories'}
          </p>
          {filtered.map((memory) => (
            <button
              className={`memory-row${
                selected?.id === memory.id ? ' is-selected' : ''
              }`}
              type="button"
              key={memory.id}
              onClick={() => setSelectedId(memory.id)}
            >
              <span className="memory-row__kind">{memory.kind}</span>
              <span className="memory-row__content">
                <strong>{memory.title}</strong>
                <span>{memory.summary}</span>
              </span>
              <time dateTime={memory.updatedAt}>
                {relativeTime(memory.updatedAt)}
              </time>
            </button>
          ))}
          {filtered.length === 0 ? (
            <EmptyState
              title="No matching memory"
              body="Try another search or add a new memory."
            />
          ) : null}
        </section>

        <aside className="memory-inspector" aria-label="Selected memory">
          {selected ? (
            <>
              <p className="section-label">Selected memory</p>
              <span className="memory-kind">{selected.kind}</span>
              <h2>{selected.title}</h2>
              <p className="memory-inspector__summary">{selected.summary}</p>
              <dl>
                <div>
                  <dt>Topics</dt>
                  <dd>{selected.topics.join(' / ')}</dd>
                </div>
                <div>
                  <dt>Created</dt>
                  <dd>{formatDate(selected.createdAt)}</dd>
                </div>
                <div>
                  <dt>Updated</dt>
                  <dd>{relativeTime(selected.updatedAt)}</dd>
                </div>
                <div>
                  <dt>Status</dt>
                  <dd>
                    <StatusMark active />
                    {selected.status}
                  </dd>
                </div>
              </dl>
            </>
          ) : (
            <EmptyState
              title="No memories yet"
              body="Add something you want your assistant to remember."
            />
          )}
        </aside>
      </div>
    </>
  )
}

type AutomationActionState = 'approve' | 'decline' | 'delete'

function AutomationActions({
  itemLabel,
  approveDisabledReason,
  deletePrompt,
  onApprove,
  onDecline,
  onDelete,
}: {
  itemLabel: string
  approveDisabledReason?: string
  deletePrompt: string
  onApprove?: () => Promise<void>
  onDecline?: () => Promise<void>
  onDelete: () => Promise<void>
}) {
  const [pending, setPending] = useState<AutomationActionState>()
  const [confirmingDelete, setConfirmingDelete] = useState(false)
  const [error, setError] = useState('')
  const busy = pending !== undefined

  const runAction = async (
    action: AutomationActionState,
    operation: () => Promise<void>,
  ) => {
    setPending(action)
    setError('')
    try {
      await operation()
      setConfirmingDelete(false)
    } catch (caught) {
      setError(
        caught instanceof Error
          ? caught.message
          : 'We could not update this item. Please try again.',
      )
    } finally {
      setPending(undefined)
    }
  }

  return (
    <div className="automation-actions" aria-live="polite">
      {confirmingDelete ? (
        <div className="automation-actions__confirm">
          <span>{deletePrompt}</span>
          <button
            className="automation-action"
            type="button"
            disabled={busy}
            onClick={() => setConfirmingDelete(false)}
          >
            Keep
          </button>
          <button
            className="automation-action automation-action--danger"
            type="button"
            disabled={busy}
            onClick={() => void runAction('delete', onDelete)}
          >
            {pending === 'delete' ? 'Deleting…' : 'Delete'}
          </button>
        </div>
      ) : (
        <div className="automation-actions__buttons">
          {onApprove ? (
            <button
              className="automation-action automation-action--primary"
              type="button"
              disabled={busy || Boolean(approveDisabledReason)}
              title={approveDisabledReason}
              aria-label={`Approve ${itemLabel}`}
              onClick={() => void runAction('approve', onApprove)}
            >
              {pending === 'approve' ? 'Approving…' : 'Approve'}
            </button>
          ) : null}
          {onDecline ? (
            <button
              className="automation-action"
              type="button"
              disabled={busy}
              aria-label={`Decline ${itemLabel}`}
              onClick={() => void runAction('decline', onDecline)}
            >
              {pending === 'decline' ? 'Declining…' : 'Decline'}
            </button>
          ) : null}
          <button
            className="automation-action automation-action--danger"
            type="button"
            disabled={busy}
            aria-label={`Delete ${itemLabel}`}
            onClick={() => {
              setError('')
              setConfirmingDelete(true)
            }}
          >
            Delete
          </button>
        </div>
      )}
      {approveDisabledReason && !confirmingDelete ? (
        <span className="automation-actions__hint">
          {approveDisabledReason}
        </span>
      ) : null}
      {error ? (
        <span className="automation-actions__error" role="alert">
          {error}
        </span>
      ) : null}
    </div>
  )
}

function WatchRow({
  watch,
  currentTime,
  onDelete,
  onResolve,
}: {
  watch: WatchItem
  currentTime: Date
  onDelete: (resourceId: string) => Promise<void>
  onResolve: (
    resourceId: string,
    decision: ProposalDecision,
  ) => Promise<void>
}) {
  const proposed = watch.status === 'proposed'
  const approveDisabledReason =
    proposed && new Date(watch.expiresAt).getTime() <= currentTime.getTime()
      ? 'This watch has expired.'
      : undefined

  return (
    <article className="watch-row">
      <div className="watch-row__index">
        <NavIcon view="watches" />
        <StatusMark active={watch.status === 'active'} />
      </div>
      <div className="watch-row__main">
        <div className="watch-row__heading">
          <div>
            <span className={`state-label state-label--${watch.status}`}>
              {watch.status}
            </span>
            <h2>{watch.query}</h2>
          </div>
          {watch.nextCheckAt ? (
            <span className="next-check">
              Next check
              <strong>{relativeTime(watch.nextCheckAt)}</strong>
            </span>
          ) : null}
        </div>
        <p className="watch-condition">
          <span>If</span> {watch.condition}
        </p>
        <dl className="watch-stats">
          <div>
            <dt>Cadence</dt>
            <dd>Every {formatInterval(watch.intervalMinutes)}</dd>
          </div>
          <div>
            <dt>Last checked</dt>
            <dd>
              {watch.lastCheckedAt ? relativeTime(watch.lastCheckedAt) : '—'}
            </dd>
          </div>
          <div>
            <dt>Sources seen</dt>
            <dd>{watch.seenCount}</dd>
          </div>
          <div>
            <dt>Ends</dt>
            <dd>{formatDate(watch.expiresAt)}</dd>
          </div>
        </dl>
        <div className="watch-row__actions">
          <AutomationActions
            itemLabel={watch.query}
            approveDisabledReason={approveDisabledReason}
            deletePrompt={
              watch.status === 'active'
                ? 'Stop and delete this watch?'
                : 'Delete this watch?'
            }
            onApprove={
              proposed
                ? () => onResolve(watch.id, 'accepted')
                : undefined
            }
            onDecline={
              proposed
                ? () => onResolve(watch.id, 'rejected')
                : undefined
            }
            onDelete={() => onDelete(watch.id)}
          />
        </div>
      </div>
    </article>
  )
}

function WatchesView({
  data,
  currentTime,
  onDelete,
  onResolve,
}: {
  data: WorkspaceData
  currentTime: Date
  onDelete: (resourceId: string) => Promise<void>
  onResolve: (
    resourceId: string,
    decision: ProposalDecision,
  ) => Promise<void>
}) {
  const watches = [...data.watches].sort((left, right) => {
    if (left.status === right.status) {
      return (
        new Date(right.createdAt).getTime() - new Date(left.createdAt).getTime()
      )
    }
    if (left.status === 'active') return -1
    if (right.status === 'active') return 1
    if (left.status === 'proposed') return -1
    return 1
  })
  const activeCount = watches.filter((watch) => watch.status === 'active').length

  return (
    <>
      <PageIntro
        eyebrow="Ongoing checks"
        title="Watches"
        description="Updates your assistant is keeping an eye on in the background."
        action={
          <div className="count-callout">
            <strong>{activeCount}</strong>
            <span>of 5 active</span>
          </div>
        }
      />
      <div className="watch-list">
        {watches.length > 0 ? (
          watches.map((watch) => (
            <WatchRow
              key={watch.id}
              watch={watch}
              currentTime={currentTime}
              onDelete={onDelete}
              onResolve={onResolve}
            />
          ))
        ) : (
          <EmptyState
            title="Nothing is being watched"
            body="Items you ask your assistant to monitor will appear here."
          />
        )}
      </div>
    </>
  )
}

function ProposedTask({
  task,
  currentTime,
  onDelete,
  onResolve,
}: {
  task: TaskItem
  currentTime: Date
  onDelete: (resourceId: string) => Promise<void>
  onResolve: (
    resourceId: string,
    decision: ProposalDecision,
  ) => Promise<void>
}) {
  const approveDisabledReason =
    new Date(task.dueAt).getTime() <= currentTime.getTime()
      ? 'This reminder time has passed.'
      : undefined

  return (
    <article className="proposed-task">
      <div>
        <p className="section-label">
          Needs review
        </p>
        <h3>{task.title}</h3>
        <p>{task.schedule}</p>
      </div>
      <div className="proposed-task__side">
        <div className="proposed-task__time">
          <span>Proposed</span>
          <strong>{relativeTime(task.createdAt)}</strong>
        </div>
        <AutomationActions
          itemLabel={task.title}
          approveDisabledReason={approveDisabledReason}
          deletePrompt="Delete this reminder suggestion?"
          onApprove={() => onResolve(task.id, 'accepted')}
          onDecline={() => onResolve(task.id, 'rejected')}
          onDelete={() => onDelete(task.id)}
        />
      </div>
    </article>
  )
}

function TasksView({
  data,
  currentTime,
  onDelete,
  onResolve,
}: {
  data: WorkspaceData
  currentTime: Date
  onDelete: (resourceId: string) => Promise<void>
  onResolve: (
    resourceId: string,
    decision: ProposalDecision,
  ) => Promise<void>
}) {
  const proposed = data.tasks
    .filter((task) => task.status === 'proposed')
    .sort(
      (left, right) =>
        new Date(right.createdAt).getTime() -
        new Date(left.createdAt).getTime(),
    )
  const confirmed = data.tasks
    .filter((task) => task.status === 'accepted')
    .sort(
      (left, right) =>
        new Date(left.dueAt).getTime() - new Date(right.dueAt).getTime(),
    )
  const pastDueCount = confirmed.filter(
    (task) => new Date(task.dueAt).getTime() <= currentTime.getTime(),
  ).length

  return (
    <div className="tasks-page">
      <header className="tasks-head">
        <span className="task-section-index" aria-hidden="true">
          01
        </span>
        <div className="tasks-head__title">
          <p className="section-label">Plans & reminders</p>
          <h1>Tasks</h1>
        </div>
        <dl className="task-summary" aria-label="Task totals">
          <div>
            <dt>Confirmed</dt>
            <dd>{String(confirmed.length).padStart(2, '0')}</dd>
          </div>
          <div>
            <dt>Needs review</dt>
            <dd>{String(proposed.length).padStart(2, '0')}</dd>
          </div>
          <div>
            <dt>Past due</dt>
            <dd>{String(pastDueCount).padStart(2, '0')}</dd>
          </div>
        </dl>
      </header>

      {proposed.length > 0 ? (
        <section className="task-review" aria-labelledby="task-review-title">
          <div className="task-subhead">
            <div>
              <p className="section-label">Needs a decision</p>
              <h2 id="task-review-title">Review queue</h2>
            </div>
            <span>{proposed.length}</span>
          </div>
          <div className="proposed-list">
            {proposed.map((task) => (
              <ProposedTask
                key={task.id}
                task={task}
                currentTime={currentTime}
                onDelete={onDelete}
                onResolve={onResolve}
              />
            ))}
          </div>
        </section>
      ) : null}

      <section className="task-schedule" aria-labelledby="task-schedule-title">
        <header className="task-subhead task-subhead--schedule">
          <div className="task-subhead__title">
            <span className="task-section-index" aria-hidden="true">
              02
            </span>
            <div>
              <p className="section-label">Confirmed schedule</p>
              <h2 id="task-schedule-title">Reminders</h2>
            </div>
          </div>
          <span>{confirmed.length} total</span>
        </header>
        {confirmed.length > 0 ? (
          <div className="task-ledger">
            {confirmed.map((task, index) => {
              const pastDue =
                new Date(task.dueAt).getTime() <= currentTime.getTime()

              return (
                <article
                  className={`task-ledger-row${pastDue ? ' is-past-due' : ''}`}
                  key={task.id}
                >
                  <span className="task-ledger-row__index" aria-hidden="true">
                    {String(index + 1).padStart(2, '0')}
                  </span>
                  <time dateTime={task.dueAt}>
                    <strong>{formatDateTime(task.dueAt)}</strong>
                    <span>{relativeTime(task.dueAt)}</span>
                  </time>
                  <div className="task-ledger-row__content">
                    <p className="section-label">
                      {pastDue ? 'Past due' : 'Scheduled'}
                    </p>
                    <h3>{task.title}</h3>
                  </div>
                  <div className="task-ledger-row__actions">
                    <AutomationActions
                      itemLabel={task.title}
                      deletePrompt="Cancel and delete this reminder?"
                      onDelete={() => onDelete(task.id)}
                    />
                  </div>
                </article>
              )
            })}
          </div>
        ) : (
          <p className="task-empty-line">No confirmed reminders.</p>
        )}
      </section>
    </div>
  )
}

function MemoryComposer({
  open,
  onClose,
  onSave,
}: {
  open: boolean
  onClose: () => void
  onSave: (input: NewMemoryInput) => Promise<void>
}) {
  const [title, setTitle] = useState('')
  const [summary, setSummary] = useState('')
  const [kind, setKind] = useState<MemoryKind>('fact')
  const [topic, setTopic] = useState('personal')
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')

  useEffect(() => {
    if (!open) {
      return
    }
    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        onClose()
      }
    }
    window.addEventListener('keydown', handleKeyDown)
    return () => window.removeEventListener('keydown', handleKeyDown)
  }, [onClose, open])

  if (!open) {
    return null
  }

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    setSaving(true)
    setError('')
    try {
      await onSave({ title, summary, kind, topic })
      setTitle('')
      setSummary('')
      setKind('fact')
      setTopic('personal')
      onClose()
    } catch {
      setError('We could not save this memory. Please try again.')
    } finally {
      setSaving(false)
    }
  }

  return (
    <div
      className="composer-backdrop"
      role="presentation"
      onMouseDown={(event) => {
        if (event.target === event.currentTarget) {
          onClose()
        }
      }}
    >
      <section
        className="memory-composer"
        role="dialog"
        aria-modal="true"
        aria-labelledby="memory-composer-title"
      >
        <header>
          <div>
            <p className="section-label">New memory</p>
            <h2 id="memory-composer-title">Add a memory</h2>
          </div>
          <button
            className="icon-action"
            type="button"
            onClick={onClose}
            aria-label="Close memory form"
          >
            ×
          </button>
        </header>
        <form onSubmit={submit}>
          <label className="field">
            <span>Title</span>
            <input
              autoFocus
              value={title}
              onChange={(event) => setTitle(event.target.value)}
              maxLength={120}
              placeholder="A short label"
              required
            />
          </label>
          <label className="field">
            <span>What should the glasses remember?</span>
            <textarea
              value={summary}
              onChange={(event) => setSummary(event.target.value)}
              maxLength={500}
              rows={6}
              placeholder="Write the detail in a way that will be useful later."
              required
            />
            <small>{summary.length} / 500</small>
          </label>
          <div className="field-pair">
            <label className="field">
              <span>Kind</span>
              <select
                value={kind}
                onChange={(event) =>
                  setKind(event.target.value as MemoryKind)
                }
              >
                {memoryKinds.map((item) => (
                  <option key={item} value={item}>
                    {item}
                  </option>
                ))}
              </select>
            </label>
            <label className="field">
              <span>Topic</span>
              <select
                value={topic}
                onChange={(event) => setTopic(event.target.value)}
              >
                {memoryTopics.map((item) => (
                  <option key={item} value={item}>
                    {item}
                  </option>
                ))}
              </select>
            </label>
          </div>
          {error ? (
            <p className="form-error" role="alert">
              {error}
            </p>
          ) : null}
          <footer>
            <p>Your assistant can use this in future conversations.</p>
            <button className="primary-action" type="submit" disabled={saving}>
              {saving ? 'Saving…' : 'Save memory'}
            </button>
          </footer>
        </form>
      </section>
    </div>
  )
}

export function Workspace({
  data,
  email,
  glassesStatus,
  dataError,
  isDemo,
  onCreateMemory,
  onDeleteAutomation,
  onResolveAutomation,
  onSignOut,
}: WorkspaceProps) {
  const [view, setView] = useState<WorkspaceView>(initialView)
  const [composerOpen, setComposerOpen] = useState(false)
  const [now, setNow] = useState(() => new Date())

  useEffect(() => {
    const timer = window.setInterval(() => setNow(new Date()), 30_000)
    return () => window.clearInterval(timer)
  }, [])

  useEffect(() => {
    const handleHashChange = () => {
      const next = window.location.hash.replace(/^#/u, '')
      if (isWorkspaceView(next)) {
        setView(next)
      }
    }
    window.addEventListener('hashchange', handleHashChange)
    return () => window.removeEventListener('hashchange', handleHashChange)
  }, [])

  const navigate = (nextView: WorkspaceView) => {
    setView(nextView)
    window.history.replaceState(
      null,
      '',
      `${window.location.pathname}${window.location.search}#${nextView}`,
    )
    window.scrollTo({ top: 0, behavior: 'smooth' })
  }

  const navCount = (item: WorkspaceView): number | undefined => {
    switch (item) {
      case 'tasks':
        return (
          data.tasks.filter((task) => task.status === 'proposed').length ||
          undefined
        )
      default:
        return undefined
    }
  }

  const connected = connectedFromStatus(glassesStatus)
  const deviceStatus = friendlyDeviceStatus(glassesStatus)
  const accountLabel = isDemo ? 'Preview mode' : shorten(email, 24)
  const accountInitial = isDemo
    ? 'P'
    : (email.trim().charAt(0).toUpperCase() || 'A')
  const viewNumber = String(
    navItems.findIndex((item) => item.id === view) + 1,
  ).padStart(2, '0')

  return (
    <div className="workspace">
      <aside className="sidebar">
        <div className="brand-block">
          <button
            className="wordmark"
            type="button"
            onClick={() => navigate('now')}
            aria-label="Go to Home"
          >
            rev/eyes
          </button>
          <span>wearable assistant</span>
        </div>
        <nav aria-label="Main navigation">
          <p className="nav-heading">Menu</p>
          {navItems.map((item) => {
            const count = navCount(item.id)
            return (
              <button
                type="button"
                key={item.id}
                className={view === item.id ? 'is-active' : ''}
                aria-current={view === item.id ? 'page' : undefined}
                onClick={() => navigate(item.id)}
              >
                <NavIcon view={item.id} />
                <span className="nav-label">{item.label}</span>
                {count !== undefined ? (
                  <span className="nav-count">{count}</span>
                ) : null}
              </button>
            )
          })}
        </nav>
        <div className="sidebar-foot">
          <div className="device-status">
            <StatusMark active={connected} />
            <div>
              <strong>Even G2</strong>
              <span>{deviceStatus}</span>
            </div>
          </div>
          <button
            className="account-button"
            type="button"
            onClick={onSignOut}
          >
            <span className="account-avatar">{accountInitial}</span>
            <span className="account-copy">
              <strong>{accountLabel}</strong>
              <small>{isDemo ? 'Exit preview' : 'Sign out'}</small>
            </span>
          </button>
        </div>
      </aside>

      <main className="workspace-main">
        <header className="topbar">
          <div className="topbar__location">
            <span className="topbar__wordmark">rev/eyes</span>
            <span className="topbar__folio-index" aria-hidden="true">
              {viewNumber}
            </span>
            <strong>{viewTitles[view]}</strong>
          </div>
          <div className="topbar__right">
            {isDemo ? <span className="demo-label">Preview</span> : null}
            <time className="topbar__clock" dateTime={now.toISOString()}>
              <span className="topbar__date">{formatDay(now)}</span>
              <strong className="topbar__time">{formatClock(now)}</strong>
            </time>
            <span
              className={`connection-label${
                connected ? ' connection-label--online' : ''
              }`}
              aria-label={`Even G2 ${deviceStatus}`}
            >
              <span className="connection-label__mark" aria-hidden="true" />
              <span>{deviceStatus}</span>
            </span>
          </div>
        </header>

        {dataError ? (
          <div className="data-notice" role="status">
            <span aria-hidden="true">i</span>
            <p>Some information may be out of date. We’ll keep trying.</p>
          </div>
        ) : null}

        <div className={`page${view === 'now' ? ' page--home' : ''}`} key={view}>
          {view === 'now' ? (
            <NowView
              data={data}
              onNavigate={navigate}
              onAddMemory={() => setComposerOpen(true)}
              currentTime={now}
            />
          ) : null}
          {view === 'conversations' ? (
            <ConversationsView data={data} />
          ) : null}
          {view === 'memories' ? (
            <MemoriesView
              data={data}
              onAdd={() => setComposerOpen(true)}
            />
          ) : null}
          {view === 'watches' ? (
            <WatchesView
              data={data}
              currentTime={now}
              onDelete={(resourceId) =>
                onDeleteAutomation('watch', resourceId)
              }
              onResolve={(resourceId, decision) =>
                onResolveAutomation('watch', resourceId, decision)
              }
            />
          ) : null}
          {view === 'tasks' ? (
            <TasksView
              data={data}
              currentTime={now}
              onDelete={(resourceId) =>
                onDeleteAutomation('reminder', resourceId)
              }
              onResolve={(resourceId, decision) =>
                onResolveAutomation('reminder', resourceId, decision)
              }
            />
          ) : null}
        </div>
      </main>

      <MemoryComposer
        open={composerOpen}
        onClose={() => setComposerOpen(false)}
        onSave={onCreateMemory}
      />
    </div>
  )
}
