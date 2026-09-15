import type { WorkspaceErrors } from './workspaceLoad'
import type { TaskItem, WorkspaceData, WorkspaceView } from './workspaceTypes'
import { getDayOverview } from './dayOverview'

type Props = {
  data: WorkspaceData
  currentTime: Date
  resourceErrors: WorkspaceErrors
  onNavigate: (view: WorkspaceView) => void
  onAddMemory: () => void
}

function Arrow() {
  return <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor"
    strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
    <path d="M5 12h14m-5-5 5 5-5 5" />
  </svg>
}

function ReminderList({ items, onOpen }: { items: TaskItem[]; onOpen: () => void }) {
  return <ol className="day-agenda">
    {items.map((task) => <li key={task.id}>
      <button type="button" onClick={onOpen}>
        <time dateTime={task.dueAt}>{new Intl.DateTimeFormat(undefined, {
          hour: 'numeric', minute: '2-digit',
        }).format(new Date(task.dueAt))}</time>
        <span>{task.title}</span>
        <Arrow />
      </button>
    </li>)}
  </ol>
}

export function HomeView({
  data, currentTime, resourceErrors, onNavigate, onAddMemory,
}: Props) {
  const day = getDayOverview(data, currentTime, resourceErrors)
  const next = day.upcoming.length ? undefined : day.nextAfterToday
  const watch = day.activeWatches[0]
  const shortcuts = [
    { view: 'memories', label: 'Memories', count: data.memories.filter((item) => item.status === 'active').length },
    { view: 'watches', label: 'Watches', count: day.activeWatches.length },
    { view: 'tasks', label: 'Reminders', count: day.futureReminderCount },
  ] as const
  const attention = [
    ...(day.taskReviews ? [{ view: 'tasks' as const, label: `Review ${day.taskReviews} suggested ${day.taskReviews === 1 ? 'reminder' : 'reminders'}` }] : []),
    ...(day.watchReviews ? [{ view: 'watches' as const, label: `Review ${day.watchReviews} suggested ${day.watchReviews === 1 ? 'watch' : 'watches'}` }] : []),
  ]

  return (
    <div className="home-brief">
      <header className="brief-hero">
        <p className="brief-eyebrow">
          {new Intl.DateTimeFormat(undefined, { weekday: 'short', month: 'short', day: 'numeric' }).format(currentTime)}
        </p>
        <h1>Today.</h1>
        <div className="brief-hero__actions">
          <button className="primary-action" type="button" onClick={onAddMemory}>
            <span aria-hidden="true">+</span> Add a memory
          </button>
        </div>
      </header>

      {attention.length ? (
        <section className="brief-attention" aria-label="Needs attention">
          {attention.map(({ view, label }) => (
            <button key={label} type="button" onClick={() => onNavigate(view)}><span>{label}</span><Arrow /></button>
          ))}
        </section>
      ) : null}

      <section className="day-overview" aria-label="Today's overview">
        <header>
          <span className="brief-eyebrow">Day brief</span>
          <span className="day-overview__period">{day.period}</span>
        </header>
        <h2 className="day-overview__heading">{day.heading}</h2>
        {day.description ? <p className="day-overview__description">{day.description}</p> : null}
        {day.upcoming.length ? <ReminderList items={day.upcoming.slice(0, 3)} onOpen={() => onNavigate('tasks')} /> : null}
        {day.earlier.length ? <details className="day-earlier">
          <summary>Earlier today <span>{day.earlier.length}</span></summary>
          <p>These reminder times have passed.</p>
          <ReminderList items={day.earlier} onOpen={() => onNavigate('tasks')} />
        </details> : null}
        <footer>
          <button className="primary-action" type="button" onClick={() => onNavigate('tasks')}>
            {day.upcoming.length > 3 ? 'View all reminders' : 'View reminders'}
          </button>
          <button className="brief-text-button" type="button" onClick={() => onNavigate('conversations')}>Open chats <Arrow /></button>
        </footer>
      </section>

      {day.memoriesToday.length ? (
        <section className="day-memories" aria-label="Memories saved today">
          <header>
            <h2 className="brief-eyebrow">Remembered today</h2>
            {resourceErrors.memories === 'stale' ? <span>Out of date</span> : null}
          </header>
          {day.memoriesToday.slice(0, 2).map((memory) => (
            <button key={memory.id} type="button" onClick={() => onNavigate('memories')}>
              <span>{memory.title}</span><Arrow />
            </button>
          ))}
          {day.memoriesToday.length > 2 ? <button className="day-memories__more" type="button"
            onClick={() => onNavigate('memories')}>View memories · {day.memoriesToday.length} saved today <Arrow /></button> : null}
        </section>
      ) : null}

      {next ? (
        <button className="brief-followup" type="button" onClick={() => onNavigate('tasks')}>
          <span className="brief-followup__icon" aria-hidden="true">↗</span>
          <span><small>Next reminder{resourceErrors.tasks === 'stale' ? ' · Out of date' : ''}</small><strong>{next.title}</strong>
            <time dateTime={next.dueAt}>{new Intl.DateTimeFormat(undefined, {
              month: 'short', day: 'numeric', hour: 'numeric', minute: '2-digit',
            }).format(new Date(next.dueAt))}</time></span>
          <Arrow />
        </button>
      ) : watch ? (
        <button className="brief-followup" type="button" onClick={() => onNavigate('watches')}>
          <span className="brief-followup__icon" aria-hidden="true">
            <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5">
              <path d="M2 12s3-6 10-6 10 6 10 6-3 6-10 6S2 12 2 12Z" /><circle cx="12" cy="12" r="2.5" />
            </svg>
          </span>
          <span><small>On your watchlist{resourceErrors.watches === 'stale' ? ' · Out of date' : ''}</small><strong>{watch.query}</strong></span>
          <Arrow />
        </button>
      ) : null}

      <nav className="brief-shortcuts" aria-label="Your workspace">
        {shortcuts.map(({ view, label, count }) => (
          <button key={view} type="button" onClick={() => onNavigate(view)}>
            <span>{label}</span>
            <span className={resourceErrors[view] ? 'brief-shortcuts__status' : 'brief-shortcuts__count'}>
              {resourceErrors[view] === 'unavailable' ? 'Unavailable' : count}
              {resourceErrors[view] === 'stale' ? ' · Out of date' : ''}
            </span>
          </button>
        ))}
      </nav>
    </div>
  )
}
