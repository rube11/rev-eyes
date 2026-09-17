import type { WorkspaceErrors } from '../workspaceLoad'
import type { TaskItem, WorkspaceData, WorkspaceView } from '../workspaceTypes'
import { getDayOverview } from '../dayOverview'
import { Presence } from '../components/Presence'
import type { PresenceState } from '../components/Presence'
import { ArrowIcon, NavIcon, PlusIcon } from '../components/icons'
import { formatDateTime, formatTime } from '../format'
import './HomeView.css'

type Props = {
  data: WorkspaceData
  currentTime: Date
  resourceErrors: WorkspaceErrors
  assistant: PresenceState
  isDemo: boolean
  reconnecting: boolean
  onReconnectGlasses?: () => void
  onNavigate: (view: WorkspaceView) => void
  onAddMemory: () => void
}

function ReminderList({ items, onOpen }: { items: TaskItem[]; onOpen: () => void }) {
  return <ol className="day-agenda">
    {items.map((task) => <li key={task.id}>
      <button type="button" onClick={onOpen}>
        <time className="tnum" dateTime={task.dueAt}>{formatTime(task.dueAt)}</time>
        <span>{task.title}</span>
        <ArrowIcon />
      </button>
    </li>)}
  </ol>
}

export function HomeView({
  data, currentTime, resourceErrors, assistant, isDemo, reconnecting,
  onReconnectGlasses, onNavigate, onAddMemory,
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
    ...(day.taskReviews ? [{ view: 'tasks' as const, count: day.taskReviews, label: `suggested ${day.taskReviews === 1 ? 'reminder' : 'reminders'} to review` }] : []),
    ...(day.watchReviews ? [{ view: 'watches' as const, count: day.watchReviews, label: `suggested ${day.watchReviews === 1 ? 'watch' : 'watches'} to review` }] : []),
  ]

  return (
    <div className="home">
      <header className="home-hero">
        <div className="home-hero__copy">
          <p className="eyebrow">
            {new Intl.DateTimeFormat(undefined, { weekday: 'long', month: 'long', day: 'numeric' }).format(currentTime)}
          </p>
          <h1>Today.</h1>
          <button className="primary-action primary-action--quiet home-hero__add" type="button" onClick={onAddMemory}>
            <PlusIcon />Add a memory
          </button>
        </div>
        <Presence
          variant="hero"
          state={assistant}
          deviceName={isDemo ? 'Preview' : 'Even G2'}
          reconnecting={reconnecting}
          onReconnect={isDemo ? undefined : onReconnectGlasses}
        />
      </header>

      {attention.length ? (
        <section className="home-attention" aria-label="Needs attention">
          {attention.map(({ view, count, label }) => (
            <button key={view} type="button" onClick={() => onNavigate(view)}>
              <span className="home-attention__count tnum">{count}</span>
              <span className="home-attention__label">{label}</span>
              <ArrowIcon />
            </button>
          ))}
        </section>
      ) : null}

      <section className="day-overview" aria-label="Today's overview">
        <header>
          <span className="eyebrow">Day brief</span>
          <span className="day-overview__period">{day.period}</span>
        </header>
        <h2 className="day-overview__heading">{day.heading}</h2>
        {day.description ? <p className="day-overview__description">{day.description}</p> : null}
        {day.upcoming.length ? <ReminderList items={day.upcoming.slice(0, 3)} onOpen={() => onNavigate('tasks')} /> : null}
        {day.earlier.length ? <details className="day-earlier">
          <summary>Earlier today <span className="tnum">{day.earlier.length}</span></summary>
          <p>These reminder times have passed.</p>
          <ReminderList items={day.earlier} onOpen={() => onNavigate('tasks')} />
        </details> : null}
        <footer>
          <button className="text-action" type="button" onClick={() => onNavigate('tasks')}>
            {day.upcoming.length > 3 ? 'View all reminders' : 'View reminders'} <ArrowIcon />
          </button>
          <button className="text-action" type="button" onClick={() => onNavigate('conversations')}>
            Open chats <ArrowIcon />
          </button>
        </footer>
      </section>

      {day.memoriesToday.length ? (
        <section className="day-memories" aria-label="Memories saved today">
          <header>
            <h2 className="eyebrow">Remembered today</h2>
            {resourceErrors.memories === 'stale' ? <span>Out of date</span> : null}
          </header>
          {day.memoriesToday.slice(0, 2).map((memory) => (
            <button key={memory.id} type="button" onClick={() => onNavigate('memories')}>
              <span className="day-memories__kind">{memory.kind}</span>
              <span className="day-memories__title">{memory.title}</span>
              <ArrowIcon />
            </button>
          ))}
          {day.memoriesToday.length > 2 ? <button className="day-memories__more" type="button"
            onClick={() => onNavigate('memories')}>View memories · {day.memoriesToday.length} saved today <ArrowIcon /></button> : null}
        </section>
      ) : null}

      {next ? (
        <button className="home-followup" type="button" onClick={() => onNavigate('tasks')}>
          <span className="home-followup__icon" aria-hidden="true"><NavIcon view="tasks" /></span>
          <span className="home-followup__copy">
            <small>Next reminder{resourceErrors.tasks === 'stale' ? ' · Out of date' : ''}</small>
            <strong>{next.title}</strong>
            <time className="tnum" dateTime={next.dueAt}>{formatDateTime(next.dueAt)}</time>
          </span>
          <ArrowIcon />
        </button>
      ) : watch ? (
        <button className="home-followup" type="button" onClick={() => onNavigate('watches')}>
          <span className="home-followup__icon" aria-hidden="true"><NavIcon view="watches" /></span>
          <span className="home-followup__copy">
            <small>On your watchlist{resourceErrors.watches === 'stale' ? ' · Out of date' : ''}</small>
            <strong>{watch.query}</strong>
          </span>
          <ArrowIcon />
        </button>
      ) : null}

      <nav className="home-index" aria-label="Your workspace">
        {shortcuts.map(({ view, label, count }) => (
          <button key={view} type="button" onClick={() => onNavigate(view)}>
            <span className="eyebrow">{label}</span>
            <span className={resourceErrors[view] === 'unavailable' ? 'home-index__status' : 'home-index__count tnum'}>
              {resourceErrors[view] === 'unavailable' ? 'Unavailable' : count}
            </span>
            {resourceErrors[view] === 'stale' ? <span className="home-index__status">Out of date</span> : null}
          </button>
        ))}
      </nav>
    </div>
  )
}
