import { useEffect, useMemo, useState } from 'react'
import type { FormEvent } from 'react'

import { HomeView } from './HomeView'
import { ConversationLog } from './ConversationLog'
import type { SendChatMessage } from './ChatComposer'
import { workspaceResources } from './workspaceTypes'
import { getAssistantStatus } from './assistantStatus'
import type { WorkspaceErrors } from './workspaceLoad'
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
  onSendChat?: SendChatMessage
  userId?: string
  data: WorkspaceData
  email: string
  glassesStatus: string
  reconnecting?: boolean
  onReconnectGlasses?: () => void
  resourceErrors?: WorkspaceErrors
  lastSyncedAt?: string
  syncing?: boolean
  onRetry?: () => void
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
  { id: 'conversations', label: 'Chats' },
  { id: 'memories', label: 'Memories' },
  { id: 'watches', label: 'Watches' },
  { id: 'tasks', label: 'Tasks' },
]

const viewTitles: Record<WorkspaceView, string> = {
  now: 'Home',
  conversations: 'Chats',
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
      <h3>{title}</h3>
      <p>{body}</p>
    </div>
  )
}

function PageIntro({
  title, action,
}: {
  title: string
  action?: React.ReactNode
}) {
  return (
    <header className="page-intro">
      <h1>{title}</h1>
      {action}
    </header>
  )
}

function ItemDetails({
  title, meta, group, children,
}: {
  title: string
  meta?: React.ReactNode
  group?: string
  children: React.ReactNode
}) {
  return (
    <details className="compact-item" name={group}>
      <summary>
        <span className="compact-item__title">{title}</span>
        {meta ? <span className="compact-item__meta">{meta}</span> : null}
        <span className="compact-item__toggle" aria-hidden="true">+</span>
      </summary>
      <div className="compact-item__body">{children}</div>
    </details>
  )
}


function MemoriesView({ data, onAdd }: {
  data: WorkspaceData
  onAdd: () => void
}) {
  const [query, setQuery] = useState('')
  const [filter, setFilter] = useState('all')
  const filters = useMemo(
    () => [...new Set(data.memories.flatMap((memory) => memory.topics))].sort(),
    [data.memories],
  )
  const filtered = useMemo(() => {
    const normalized = query.toLowerCase().trim()
    return data.memories.filter((memory) =>
      (filter === 'all' || memory.topics.includes(filter)) &&
      (!normalized || [memory.title, memory.summary, memory.kind, ...memory.topics]
        .join(' ').toLowerCase().includes(normalized)),
    )
  }, [data.memories, filter, query])

  return (
    <>
      <PageIntro title="Memories" action={
        <button className="primary-action" type="button" aria-label="Add memory" onClick={onAdd}>+ Add</button>
      } />
      <div className="compact-toolbar">
        <label className="search-field">
          <span>Search memories</span>
          <input type="search" value={query}
            onChange={(event) => setQuery(event.target.value)}
            placeholder="Search memories" />
        </label>
        <select className="topic-filter" aria-label="Filter memories by topic"
          value={filter} onChange={(event) => setFilter(event.target.value)}>
          <option value="all">All topics</option>
          {filters.map((topic) => <option key={topic} value={topic}>{topic}</option>)}
        </select>
      </div>
      <p className="result-count">{filtered.length} {filtered.length === 1 ? 'memory' : 'memories'}</p>
      <div className="compact-list">
        {filtered.map((memory) => (
          <ItemDetails key={memory.id} title={memory.title} group="memories">
            <p className="item-copy">{memory.summary}</p>
            <dl className="item-facts">
              <div><dt>Kind</dt><dd>{memory.kind}</dd></div>
              <div><dt>Topics</dt><dd>{memory.topics.join(', ') || 'None'}</dd></div>
              <div><dt>Created</dt><dd>{formatDate(memory.createdAt)}</dd></div>
              <div><dt>Updated</dt><dd>{formatDateTime(memory.updatedAt)}</dd></div>
              <div><dt>Status</dt><dd>{memory.status}</dd></div>
              {memory.expiresAt ? <div><dt>Expires</dt><dd>{formatDateTime(memory.expiresAt)}</dd></div> : null}
            </dl>
          </ItemDetails>
        ))}
      </div>
      {!filtered.length ? <EmptyState title={query || filter !== 'all' ? 'No matches' : 'No memories yet'}
        body={query || filter !== 'all' ? 'Try another search or topic.' : 'Add a memory to get started.'} /> : null}
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

function WatchRow({ watch, currentTime, onDelete, onResolve }: {
  watch: WatchItem
  currentTime: Date
  onDelete: (resourceId: string) => Promise<void>
  onResolve: (resourceId: string, decision: ProposalDecision) => Promise<void>
}) {
  const proposed = watch.status === 'proposed'
  const approveDisabledReason =
    proposed && Date.parse(watch.expiresAt) <= currentTime.getTime()
      ? 'This watch has expired.' : undefined

  return (
    <ItemDetails title={watch.query} group="watches"
      meta={proposed ? 'Needs review' : watch.status}>
      <p className="item-copy">{watch.condition}</p>
      <dl className="item-facts">
        <div><dt>Checks</dt><dd>Every {formatInterval(watch.intervalMinutes)}</dd></div>
        <div><dt>Last checked</dt><dd>{watch.lastCheckedAt ? formatDateTime(watch.lastCheckedAt) : 'Not yet'}</dd></div>
        {watch.nextCheckAt ? <div><dt>Next check</dt><dd>{formatDateTime(watch.nextCheckAt)}</dd></div> : null}
        <div><dt>Sources seen</dt><dd>{watch.seenCount}</dd></div>
        <div><dt>Ends</dt><dd>{formatDateTime(watch.expiresAt)}</dd></div>
      </dl>
      <AutomationActions itemLabel={watch.query}
        approveDisabledReason={approveDisabledReason}
        deletePrompt={watch.status === 'active' ? 'Stop and delete this watch?' : 'Delete this watch?'}
        onApprove={proposed ? () => onResolve(watch.id, 'accepted') : undefined}
        onDecline={proposed ? () => onResolve(watch.id, 'rejected') : undefined}
        onDelete={() => onDelete(watch.id)} />
    </ItemDetails>
  )
}

function WatchesView({ data, currentTime, onDelete, onResolve }: {
  data: WorkspaceData
  currentTime: Date
  onDelete: (resourceId: string) => Promise<void>
  onResolve: (resourceId: string, decision: ProposalDecision) => Promise<void>
}) {
  const rank = { proposed: 0, active: 1, expired: 2, rejected: 3 }
  const watches = [...data.watches].sort((a, b) =>
    rank[a.status] - rank[b.status] || Date.parse(b.createdAt) - Date.parse(a.createdAt),
  )
  const activeCount = watches.filter((watch) => watch.status === 'active').length
  return (
    <>
      <PageIntro title="Watches" action={<span className="item-meta">{activeCount} / 5 active</span>} />
      <div className="compact-list">
        {watches.map((watch) => <WatchRow key={watch.id} watch={watch}
          currentTime={currentTime} onDelete={onDelete} onResolve={onResolve} />)}
      </div>
      {!watches.length ? <EmptyState title="No watches yet" body="Ask Eyes to watch something for you." /> : null}
    </>
  )
}

function TaskRow({ task, currentTime, onDelete, onResolve }: {
  task: TaskItem
  currentTime: Date
  onDelete: (resourceId: string) => Promise<void>
  onResolve: (resourceId: string, decision: ProposalDecision) => Promise<void>
}) {
  const proposed = task.status === 'proposed'
  const pastDue = Date.parse(task.dueAt) <= currentTime.getTime()
  return (
    <ItemDetails title={task.title} meta={formatDateTime(task.dueAt)} group="task-items">
      <p className="item-copy">{task.schedule}</p>
      <p className="item-meta">
        {proposed ? 'Needs approval' : pastDue ? 'Reminder time has passed' : 'Scheduled'}
        {' · '}{relativeTime(task.dueAt)}
      </p>
      <AutomationActions itemLabel={task.title}
        approveDisabledReason={proposed && pastDue ? 'This reminder time has passed.' : undefined}
        deletePrompt={proposed ? 'Delete this reminder suggestion?' : 'Cancel and delete this reminder?'}
        onApprove={proposed ? () => onResolve(task.id, 'accepted') : undefined}
        onDecline={proposed ? () => onResolve(task.id, 'rejected') : undefined}
        onDelete={() => onDelete(task.id)} />
    </ItemDetails>
  )
}

function TasksView({ data, currentTime, onDelete, onResolve }: {
  data: WorkspaceData
  currentTime: Date
  onDelete: (resourceId: string) => Promise<void>
  onResolve: (resourceId: string, decision: ProposalDecision) => Promise<void>
}) {
  const proposed = data.tasks.filter((task) => task.status === 'proposed')
    .sort((a, b) => Date.parse(b.createdAt) - Date.parse(a.createdAt))
  const confirmed = data.tasks.filter((task) => task.status === 'accepted')
    .sort((a, b) => Date.parse(a.dueAt) - Date.parse(b.dueAt))
  const upcoming = confirmed.filter((task) => Date.parse(task.dueAt) > currentTime.getTime())
  const pastDue = confirmed.filter((task) => Date.parse(task.dueAt) <= currentTime.getTime())
  const row = (task: TaskItem) => <TaskRow key={task.id} task={task}
    currentTime={currentTime} onDelete={onDelete} onResolve={onResolve} />

  return (
    <>
      <PageIntro title="Tasks" />
      <div className="compact-groups">
        {proposed.length ? (
          <section aria-labelledby="task-review-title">
            <h2 className="compact-group-title" id="task-review-title">Needs review <span>{proposed.length}</span></h2>
            <div className="compact-list">{proposed.map(row)}</div>
          </section>
        ) : null}
        <section aria-labelledby="task-upcoming-title">
          <h2 className="compact-group-title" id="task-upcoming-title">Upcoming <span>{upcoming.length}</span></h2>
          <div className="compact-list">{upcoming.map(row)}</div>
          {!upcoming.length ? <p className="item-meta compact-empty">No upcoming reminders.</p> : null}
        </section>
        {pastDue.length ? (
          <details className="past-reminders">
            <summary>Past due <span>{pastDue.length}</span></summary>
            <div className="compact-list">{pastDue.map(row)}</div>
          </details>
        ) : null}
      </div>
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
            <span>Details</span>
            <textarea
              value={summary}
              onChange={(event) => setSummary(event.target.value)}
              maxLength={500}
              rows={6}
              placeholder="What should Eyes remember?"
              required
            />
            <small>{summary.length} / 500</small>
          </label>
          <details className="composer-options">
            <summary>More options</summary>
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
          </details>
          {error ? (
            <p className="form-error" role="alert">
              {error}
            </p>
          ) : null}
          <footer>
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
  onSendChat,
  userId,
  data,
  email,
  glassesStatus,
  reconnecting = false,
  onReconnectGlasses,
  resourceErrors = {},
  lastSyncedAt,
  syncing = false,
  onRetry,
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
      case 'watches':
        return data.watches.filter((watch) => watch.status === 'proposed').length || undefined
      case 'tasks':
        return (
          data.tasks.filter((task) => task.status === 'proposed').length ||
          undefined
        )
      default:
        return undefined
    }
  }

  const assistant = getAssistantStatus(glassesStatus, isDemo)
  const connected = assistant.active
  const deviceStatus = assistant.label
  const failedResources = workspaceResources.filter((resource) => resourceErrors[resource])
  const unavailable = view !== 'now' && resourceErrors[view] === 'unavailable'
  const accountLabel = isDemo ? 'Preview mode' : shorten(email, 24)
  const accountInitial = isDemo
    ? 'P'
    : (email.trim().charAt(0).toUpperCase() || 'A')

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
        </div>
        <nav aria-label="Main navigation">
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
            <strong>{viewTitles[view]}</strong>
          </div>
          <div className="topbar__right">
            {onRetry ? <button className="refresh-action" type="button" disabled={syncing} onClick={onRetry} title={lastSyncedAt ? `Last updated ${formatDateTime(lastSyncedAt)}` : 'Reload saved items'}>{syncing ? 'Refreshing…' : 'Refresh'}</button> : null}
            <details className="connection-menu">
              <summary
                className={`connection-label${connected ? ' connection-label--online' : ''}`}
                aria-label={`Even G2 ${deviceStatus}. Connection details`}
              >
                <span className="connection-label__mark" aria-hidden="true" />
                <span>{deviceStatus}</span>
              </summary>
              <div>
                <strong>{isDemo ? 'Preview' : 'Even G2'}</strong>
                <p>{assistant.detail}</p>
                {!isDemo && onReconnectGlasses ? <button className="connection-reconnect" type="button"
                  disabled={reconnecting} onClick={onReconnectGlasses}>
                  {reconnecting ? 'Reconnecting…' : 'Reconnect glasses'}
                </button> : null}
              </div>
            </details>
            {!isDemo && !connected && onReconnectGlasses ? <button className="connection-retry" type="button"
              aria-label="Reconnect glasses" disabled={reconnecting} onClick={onReconnectGlasses}>
              {reconnecting ? 'Reconnecting…' : 'Reconnect'}
            </button> : null}
            <details className="account-menu">
              <summary aria-label="Account menu">{accountInitial}</summary>
              <div><p>{isDemo ? 'Sample data · changes are not saved' : email}</p><button type="button" onClick={onSignOut}>{isDemo ? 'Exit preview' : 'Sign out'}</button></div>
            </details>
          </div>
        </header>

        {failedResources.length > 0 ? (
          <div className="data-notice" role="status">
            <div>
              <strong>Some sections couldn’t refresh.</strong>
              <p>{failedResources.map((resource) => `${viewTitles[resource]}: ${resourceErrors[resource] === 'stale' ? 'showing last loaded data' : 'unavailable'}`).join(' · ')}. We’ll keep retrying; other sections remain available.</p>
            </div>
            <button type="button" className="refresh-action" disabled={syncing} onClick={onRetry}>{syncing ? 'Retrying…' : 'Try again'}</button>
          </div>
        ) : null}

        <div className={`page${view === 'now' ? ' page--home' : ''}`} key={view}>
          {unavailable ? <EmptyState title={`${viewTitles[view]} couldn’t load`} body="This section is unavailable right now. Use Try again above to reload it. Other sections remain available." /> : null}
          {view === 'now' ? (
            <HomeView
              data={data}
              resourceErrors={resourceErrors}
              onNavigate={navigate}
              onAddMemory={() => setComposerOpen(true)}
              currentTime={now}
            />
          ) : null}
          {!unavailable && view === 'conversations' ? (
            <ConversationLog conversations={data.conversations} userId={userId} onSend={onSendChat} />
          ) : null}
          {!unavailable && view === 'memories' ? (
            <MemoriesView
              data={data}
              onAdd={() => setComposerOpen(true)}
            />
          ) : null}
          {!unavailable && view === 'watches' ? (
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
          {!unavailable && view === 'tasks' ? (
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
