import { AutomationActions } from '../components/AutomationActions'
import { EmptyState } from '../components/EmptyState'
import { PageIntro } from '../components/PageIntro'
import { formatShortDate, formatTime, groupByDay, relativeTime } from '../format'
import type { ProposalDecision, TaskItem, WorkspaceData } from '../workspaceTypes'
import './TasksView.css'

type Resolve = (resourceId: string, decision: ProposalDecision) => Promise<void>
type Remove = (resourceId: string) => Promise<void>

function TaskRow({ task, currentTime, showDate, onDelete, onResolve }: {
  task: TaskItem
  currentTime: Date
  showDate: boolean
  onDelete: Remove
  onResolve: Resolve
}) {
  const proposed = task.status === 'proposed'
  const pastDue = Date.parse(task.dueAt) <= currentTime.getTime()
  const tone = proposed ? 'proposed' : pastDue ? 'past' : 'scheduled'
  const note = proposed
    ? `Suggested ${relativeTime(task.createdAt, currentTime.getTime())}`
    : pastDue
      ? `Reminder time passed ${relativeTime(task.dueAt, currentTime.getTime())}`
      : relativeTime(task.dueAt, currentTime.getTime())

  return (
    <li className={`timeline-item timeline-item--${tone}`}>
      <time className="timeline-item__time tnum" dateTime={task.dueAt}>
        {formatTime(task.dueAt)}
        {showDate ? <small>{formatShortDate(task.dueAt)}</small> : null}
      </time>
      <span className="timeline-item__spine" aria-hidden="true" />
      <div className="timeline-item__body">
        <h3 className="timeline-item__title">{task.title}</h3>
        <p className="timeline-item__schedule">
          {task.schedule}
          <span className="timeline-item__note"> · {note}</span>
        </p>
        <div className="timeline-item__actions">
          <AutomationActions itemLabel={task.title} layout="inline" size="sm"
            approveDisabledReason={proposed && pastDue ? 'This reminder time has passed.' : undefined}
            deletePrompt={proposed ? 'Delete this reminder suggestion?' : 'Cancel and delete this reminder?'}
            onApprove={proposed ? () => onResolve(task.id, 'accepted') : undefined}
            onDecline={proposed ? () => onResolve(task.id, 'rejected') : undefined}
            onDelete={() => onDelete(task.id)} />
        </div>
      </div>
    </li>
  )
}

export function TasksView({ data, currentTime, onDelete, onResolve }: {
  data: WorkspaceData
  currentTime: Date
  onDelete: Remove
  onResolve: Resolve
}) {
  const proposed = data.tasks.filter((task) => task.status === 'proposed')
    .sort((a, b) => Date.parse(b.createdAt) - Date.parse(a.createdAt))
  const confirmed = data.tasks.filter((task) => task.status === 'accepted')
  const upcoming = confirmed.filter((task) => Date.parse(task.dueAt) > currentTime.getTime())
  const pastDue = confirmed.filter((task) => Date.parse(task.dueAt) <= currentTime.getTime())
    .sort((a, b) => Date.parse(b.dueAt) - Date.parse(a.dueAt))
  const days = groupByDay(upcoming, currentTime)
  const row = (task: TaskItem, showDate: boolean) => (
    <TaskRow key={task.id} task={task} currentTime={currentTime} showDate={showDate}
      onDelete={onDelete} onResolve={onResolve} />
  )

  return (
    <>
      <PageIntro
        title="Tasks"
        lede={upcoming.length
          ? `${upcoming.length} ${upcoming.length === 1 ? 'reminder' : 'reminders'} Eyes will surface on your glasses.`
          : 'Reminders Eyes will surface on your glasses when the time comes.'}
      />
      <div className="timeline-groups">
        {proposed.length ? (
          <section className="timeline-group timeline-group--review" aria-labelledby="task-review-title">
            <h2 className="section-title" id="task-review-title">
              <span className="eyebrow eyebrow--accent">Needs review</span>
              <span>{proposed.length}</span>
            </h2>
            <ol className="timeline">{proposed.map((task) => row(task, true))}</ol>
          </section>
        ) : null}
        {days.map((day) => (
          <section key={day.key} className="timeline-group" aria-label={day.label}>
            <h2 className="section-title timeline-group__day">
              {day.label}
              <span>{day.items.length}</span>
            </h2>
            <ol className="timeline">{day.items.map((task) => row(task, false))}</ol>
          </section>
        ))}
        {!upcoming.length && !proposed.length ? (
          <EmptyState title="Nothing scheduled" body="Ask Eyes to remind you about something and it will show up here." />
        ) : !upcoming.length ? (
          <p className="timeline-empty">No upcoming reminders.</p>
        ) : null}
        {pastDue.length ? (
          <details className="timeline-past">
            <summary><span className="eyebrow">Past due</span><span>{pastDue.length}</span></summary>
            <ol className="timeline">{pastDue.map((task) => row(task, true))}</ol>
          </details>
        ) : null}
      </div>
    </>
  )
}
