import { useEffect, useMemo, useRef, useState } from 'react'
import type { FormEvent } from 'react'

import type {
  MemoryKind,
  NewMemoryInput,
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

function greetingForTime(value: Date): string {
  const hour = value.getHours()
  if (hour < 12) {
    return 'Good morning'
  }
  if (hour < 18) {
    return 'Good afternoon'
  }
  return 'Good evening'
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

function SearchIcon() {
  return (
    <svg
      width="20"
      height="20"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.7"
      strokeLinecap="round"
      aria-hidden="true"
    >
      <circle cx="10.5" cy="10.5" r="6.5" />
      <path d="m15.5 15.5 5 5" />
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

type HomeEvent = {
  id: string
  view: Exclude<WorkspaceView, 'now'>
  label: string
  title: string
  body: string
  searchText: string
  timestamp: string
}

type HomeFilter =
  | 'all'
  | 'attention'
  | 'waiting'
  | 'memories'
  | 'conversations'

const homeFilters: Array<{ id: HomeFilter; label: string }> = [
  { id: 'all', label: 'All context' },
  { id: 'attention', label: 'Needs action' },
  { id: 'waiting', label: 'Watching' },
  { id: 'memories', label: 'Memories' },
  { id: 'conversations', label: 'Conversations' },
]

function HomeEventRow({
  event,
  onNavigate,
}: {
  event: HomeEvent
  onNavigate: (view: WorkspaceView) => void
}) {
  return (
    <button
      className="home-event"
      type="button"
      data-kind={event.view}
      onClick={() => onNavigate(event.view)}
    >
      <span className="home-event__icon">
        <NavIcon view={event.view} />
      </span>
      <span className="home-event__content">
        <span className="home-event__meta">
          <small>{event.label}</small>
          <time dateTime={event.timestamp} title={formatDateTime(event.timestamp)}>
            {relativeTime(event.timestamp)}
          </time>
        </span>
        <strong>{event.title}</strong>
        <span>{shorten(event.body, 120)}</span>
      </span>
      <span className="home-event__arrow" aria-hidden="true">
        ↗
      </span>
    </button>
  )
}

function NowView({
  data,
  glassesStatus,
  latestResponse,
  onNavigate,
  onAddMemory,
  currentTime,
}: {
  data: WorkspaceData
  glassesStatus: string
  latestResponse: string
  onNavigate: (view: WorkspaceView) => void
  onAddMemory: () => void
  currentTime: Date
}) {
  const [recallQuery, setRecallQuery] = useState('')
  const [recallFilter, setRecallFilter] = useState<HomeFilter>('all')
  const recallInputRef = useRef<HTMLInputElement>(null)
  const connected = connectedFromStatus(glassesStatus)
  const activeWatches = data.watches.filter((watch) => watch.status === 'active')
  const proposedTasks = data.tasks.filter((task) => task.status === 'proposed')
  const upcomingTasks = data.tasks
    .filter((task) => task.status === 'accepted')
    .sort(
      (left, right) =>
        new Date(left.dueAt).getTime() - new Date(right.dueAt).getTime(),
    )
  const latestConversation = data.conversations[0]
  const friendlyStatus = friendlyDeviceStatus(glassesStatus)
  const hasLatestResponse = Boolean(
    latestResponse ||
      latestConversation?.transcript.some(
        (line) => line.speaker === 'assistant',
      ),
  )
  const displayResponse =
    latestResponse ||
    latestConversation?.transcript
      .filter((line) => line.speaker === 'assistant')
      .at(-1)?.text ||
    'Your assistant is synced and ready to help.'

  useEffect(() => {
    const focusRecall = (event: KeyboardEvent) => {
      const target = event.target as HTMLElement | null
      const isEditing = target?.matches(
        'input, textarea, select, [contenteditable="true"]',
      )
      if (event.key === '/' && !isEditing) {
        event.preventDefault()
        recallInputRef.current?.focus()
      }
    }

    window.addEventListener('keydown', focusRecall)
    return () => window.removeEventListener('keydown', focusRecall)
  }, [])

  const events: HomeEvent[] = [
    ...data.conversations.map(
      (conversation): HomeEvent => ({
        id: `conversation-${conversation.id}`,
        view: 'conversations',
        label: 'Conversation',
        title: conversation.title,
        body: conversation.summary,
        searchText: conversation.transcript.map((line) => line.text).join(' '),
        timestamp: conversation.lastActivityAt,
      }),
    ),
    ...data.memories.map(
      (memory): HomeEvent => ({
        id: `memory-${memory.id}`,
        view: 'memories',
        label: 'Memory updated',
        title: memory.title,
        body: memory.summary,
        searchText: [memory.kind, ...memory.topics].join(' '),
        timestamp: memory.updatedAt,
      }),
    ),
    ...upcomingTasks.map(
      (task): HomeEvent => ({
        id: `task-${task.id}`,
        view: 'tasks',
        label: 'Upcoming reminder',
        title: task.title,
        body: task.schedule,
        searchText: task.schedule,
        timestamp: task.dueAt,
      }),
    ),
    ...proposedTasks.map(
      (task): HomeEvent => ({
        id: `proposal-${task.id}`,
        view: 'tasks',
        label: 'Task suggested',
        title: task.title,
        body: task.schedule,
        searchText: task.schedule,
        timestamp: task.createdAt,
      }),
    ),
    ...activeWatches
      .filter((watch) => watch.nextCheckAt)
      .map(
        (watch): HomeEvent => ({
          id: `watch-${watch.id}`,
          view: 'watches',
          label: 'Next watch check',
          title: watch.query,
          body: watch.condition,
          searchText: watch.condition,
          timestamp: watch.nextCheckAt ?? watch.createdAt,
        }),
      ),
  ]
  const normalizedQuery = recallQuery.trim().toLowerCase()
  const filteredEvents = events.filter((event) => {
    const matchesQuery =
      !normalizedQuery ||
      [event.label, event.title, event.body, event.searchText]
        .join(' ')
        .toLowerCase()
        .includes(normalizedQuery)
    const matchesFilter =
      recallFilter === 'all' ||
      (recallFilter === 'attention' && event.view === 'tasks') ||
      (recallFilter === 'waiting' && event.view === 'watches') ||
      (recallFilter === 'memories' && event.view === 'memories') ||
      (recallFilter === 'conversations' && event.view === 'conversations')
    return matchesQuery && matchesFilter
  })
  const recallActive = normalizedQuery.length > 0 || recallFilter !== 'all'
  const nowTime = currentTime.getTime()
  const visibleEvents = [...filteredEvents]
    .sort(
      (left, right) =>
        Math.abs(new Date(left.timestamp).getTime() - nowTime) -
          Math.abs(new Date(right.timestamp).getTime() - nowTime) ||
        new Date(right.timestamp).getTime() -
          new Date(left.timestamp).getTime(),
    )
    .slice(0, recallActive ? 8 : 6)
  const filterCounts: Record<HomeFilter, number> = {
    all: events.length,
    attention: events.filter((event) => event.view === 'tasks').length,
    waiting: events.filter((event) => event.view === 'watches').length,
    memories: events.filter((event) => event.view === 'memories').length,
    conversations: events.filter((event) => event.view === 'conversations')
      .length,
  }
  const resetRecall = () => {
    setRecallQuery('')
    setRecallFilter('all')
  }
  const attentionSummary = proposedTasks.length
    ? `${proposedTasks.length} suggested ${
        proposedTasks.length === 1 ? 'task needs' : 'tasks need'
      } your review.`
    : upcomingTasks[0]
      ? `Your next reminder is ${relativeTime(upcomingTasks[0].dueAt)}.`
      : activeWatches.length
        ? `${activeWatches.length} ${
            activeWatches.length === 1 ? 'watch is' : 'watches are'
          } running quietly in the background.`
        : 'Nothing needs your attention right now.'

  return (
    <div className="home">
      <section className="home-focus" aria-labelledby="home-title">
        <div className="home-focus__top">
          <span>{formatDay(currentTime)}</span>
          <span className="home-focus__device">
            <StatusMark active={connected} />
            Even G2 / {friendlyStatus}
          </span>
        </div>
        <div className="home-focus__body">
          <div className="home-focus__heading">
            <p className="section-label">Now</p>
            <h1 id="home-title">{greetingForTime(currentTime)}.</h1>
            <p>{attentionSummary}</p>
          </div>
          <div className="home-focus__handoff">
            <span>
              {hasLatestResponse ? 'Last from your assistant' : 'Assistant state'}
            </span>
            <p>{shorten(displayResponse, 210)}</p>
          </div>
        </div>

        <div className="home-recall">
          <form
            className="home-recall__field"
            role="search"
            onSubmit={(event) => event.preventDefault()}
          >
            <SearchIcon />
            <input
              ref={recallInputRef}
              type="search"
              value={recallQuery}
              onChange={(event) => setRecallQuery(event.target.value)}
              onKeyDown={(event) => {
                if (event.key === 'Escape') {
                  setRecallQuery('')
                  event.currentTarget.blur()
                }
              }}
              aria-label="Search your history"
              placeholder="Search people, decisions, reminders, anything…"
            />
            {recallQuery ? (
              <button
                type="button"
                onClick={() => setRecallQuery('')}
                aria-label="Clear recall search"
              >
                ×
              </button>
            ) : (
              <kbd aria-hidden="true">/</kbd>
            )}
          </form>
          <div
            className="home-lenses"
            role="group"
            aria-label="Filter recall results"
          >
            {homeFilters.map((filter) => (
              <button
                type="button"
                key={filter.id}
                className={recallFilter === filter.id ? 'is-active' : ''}
                aria-pressed={recallFilter === filter.id}
                onClick={() => setRecallFilter(filter.id)}
              >
                <span>{filter.label}</span>
                <small>{filterCounts[filter.id]}</small>
              </button>
            ))}
          </div>
        </div>
      </section>

      <div className="home-workbench">
        <section className="home-stream" aria-labelledby="home-stream-title">
          <header className="home-section-head">
            <div>
              <p className="section-label">
                {recallActive ? 'Recall results' : 'Recent & upcoming'}
              </p>
              <h2 id="home-stream-title">
                {recallActive ? 'What matches' : 'Around now'}
              </h2>
            </div>
            <div className="home-section-head__meta">
              <span aria-live="polite">
                {visibleEvents.length} of {filteredEvents.length}
              </span>
              {recallActive ? (
                <button type="button" onClick={resetRecall}>
                  Reset
                </button>
              ) : null}
            </div>
          </header>
          <div className="home-event-list">
            {visibleEvents.map((event) => (
              <HomeEventRow
                key={event.id}
                event={event}
                onNavigate={onNavigate}
              />
            ))}
            {filteredEvents.length === 0 ? (
              <div className="home-no-results">
                <p className="section-label">No match yet</p>
                <h3>Try a broader word.</h3>
                <p>
                  Search a person, topic, place, or decision—or return to all
                  of your context.
                </p>
                <button type="button" onClick={resetRecall}>
                  Show all context
                </button>
              </div>
            ) : null}
          </div>
        </section>

        <aside className="home-actions" aria-labelledby="home-actions-title">
          <header className="home-section-head">
            <div>
              <p className="section-label">Ready for you</p>
              <h2 id="home-actions-title">Next actions</h2>
            </div>
          </header>
          <div className="home-action-stack">
            {proposedTasks.length > 0 ? (
              <button
                className="home-action"
                type="button"
                onClick={() => onNavigate('tasks')}
              >
                <span className="home-action__copy">
                  <small>Decision</small>
                  <strong>
                    Review {proposedTasks.length} suggested{' '}
                    {proposedTasks.length === 1 ? 'task' : 'tasks'}
                  </strong>
                  <span>{shorten(proposedTasks[0].title, 72)}</span>
                </span>
                <i aria-hidden="true">→</i>
              </button>
            ) : null}
            {upcomingTasks[0] ? (
              <button
                className="home-action"
                type="button"
                onClick={() => onNavigate('tasks')}
              >
                <span className="home-action__copy">
                  <small>Reminder · {formatDateTime(upcomingTasks[0].dueAt)}</small>
                  <strong>Open your next reminder</strong>
                  <span>{shorten(upcomingTasks[0].title, 72)}</span>
                </span>
                <i aria-hidden="true">→</i>
              </button>
            ) : null}
            {activeWatches.length > 0 ? (
              <button
                className="home-action"
                type="button"
                onClick={() => onNavigate('watches')}
              >
                <span className="home-action__copy">
                  <small>
                    {activeWatches.length}{' '}
                    {activeWatches.length === 1 ? 'active watch' : 'active watches'}
                  </small>
                  <strong>See what you’re waiting on</strong>
                  <span>{shorten(activeWatches[0].query, 72)}</span>
                </span>
                <i aria-hidden="true">→</i>
              </button>
            ) : null}
            <button
              className="home-action"
              type="button"
              onClick={onAddMemory}
            >
              <span className="home-action__copy">
                <small>Memory</small>
                <strong>Remember something new</strong>
                <span>Add a fact, preference, person, or plan.</span>
              </span>
              <i aria-hidden="true">＋</i>
            </button>
          </div>
        </aside>
      </div>
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

function WatchRow({ watch }: { watch: WatchItem }) {
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
      </div>
    </article>
  )
}

function WatchesView({ data }: { data: WorkspaceData }) {
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
            <WatchRow key={watch.id} watch={watch} />
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

function ProposedTask({ task }: { task: TaskItem }) {
  return (
    <article className="proposed-task">
      <div>
        <p className="section-label">
          <StatusMark active />
          Needs review
        </p>
        <h3>{task.title}</h3>
        <p>{task.schedule}</p>
      </div>
      <div className="proposed-task__time">
        <span>Proposed</span>
        <strong>{relativeTime(task.createdAt)}</strong>
      </div>
    </article>
  )
}

function TasksView({ data }: { data: WorkspaceData }) {
  const proposed = data.tasks
    .filter((task) => task.status === 'proposed')
    .sort(
      (left, right) =>
        new Date(right.createdAt).getTime() -
        new Date(left.createdAt).getTime(),
    )
  const upcoming = data.tasks
    .filter((task) => task.status === 'accepted')
    .sort(
      (left, right) =>
        new Date(left.dueAt).getTime() - new Date(right.dueAt).getTime(),
    )
  const rejectedCount = data.tasks.filter(
    (task) => task.status === 'rejected',
  ).length

  return (
    <>
      <PageIntro
        eyebrow="Plans and reminders"
        title="Tasks"
        description="Review suggested actions and see what is already scheduled."
      />

      <section className="task-section">
        <div className="section-heading section-heading--bordered">
          <div>
            <p className="section-label">Needs a decision</p>
            <h2>Suggested</h2>
          </div>
          <span className="section-count">{proposed.length}</span>
        </div>
        {proposed.length > 0 ? (
          <div className="proposed-list">
            {proposed.map((task) => (
              <ProposedTask key={task.id} task={task} />
            ))}
          </div>
        ) : (
          <EmptyState
            title="No suggestions to review"
            body="Helpful actions suggested during conversations will appear here."
          />
        )}
      </section>

      <section className="task-section task-section--upcoming">
        <div className="section-heading section-heading--bordered">
          <div>
            <p className="section-label">Confirmed</p>
            <h2>Upcoming</h2>
          </div>
          <span className="section-count">{upcoming.length}</span>
        </div>
        {upcoming.length > 0 ? (
          <div className="timeline">
            {upcoming.map((task, index) => (
              <article className="timeline-row" key={task.id}>
                <div className="timeline-row__rail" aria-hidden="true">
                  <span>{String(index + 1).padStart(2, '0')}</span>
                  <i />
                </div>
                <time dateTime={task.dueAt}>
                  <strong>{formatDateTime(task.dueAt)}</strong>
                  <span>{relativeTime(task.dueAt)}</span>
                </time>
                <div>
                  <h3>{task.title}</h3>
                  <p>{task.schedule}</p>
                </div>
                <span className="state-label state-label--accepted">
                  scheduled
                </span>
              </article>
            ))}
          </div>
        ) : (
          <EmptyState
            title="Nothing upcoming"
            body="Scheduled reminders will appear here in date order."
          />
        )}
      </section>

      {rejectedCount > 0 ? (
        <p className="archive-note">{rejectedCount} rejected proposals hidden</p>
      ) : null}
    </>
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
