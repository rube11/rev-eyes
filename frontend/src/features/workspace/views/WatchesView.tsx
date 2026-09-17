import { AutomationActions } from '../components/AutomationActions'
import { EmptyState } from '../components/EmptyState'
import { PageIntro } from '../components/PageIntro'
import { formatDateTime, formatInterval, formatShortDate, formatTime } from '../format'
import type { ProposalDecision, WatchItem, WorkspaceData } from '../workspaceTypes'
import './WatchesView.css'

const WATCH_LIMIT = 5

function CapacityMeter({ active }: { active: number }) {
  return (
    <div className="watch-capacity" role="img" aria-label={`${active} of ${WATCH_LIMIT} watches active`}>
      <span className="watch-capacity__ticks" aria-hidden="true">
        {Array.from({ length: WATCH_LIMIT }, (_, index) => (
          <i key={index} className={index < active ? 'is-on' : ''} />
        ))}
      </span>
      <span className="watch-capacity__label tnum">{active} / {WATCH_LIMIT} active</span>
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
  const active = watch.status === 'active'
  const approveDisabledReason =
    proposed && Date.parse(watch.expiresAt) <= currentTime.getTime()
      ? 'This watch has expired.' : undefined

  return (
    <article className={`watch watch--${watch.status}`}>
      <span className="watch__rail" aria-hidden="true" />
      <div className="watch__body">
        <h3 className="watch__query">{watch.query}</h3>
        <p className="watch__condition">{watch.condition}</p>
        <dl className="watch__readout tnum">
          <div><dt className="visually-hidden">Checks</dt><dd>Every {formatInterval(watch.intervalMinutes)}</dd></div>
          {active && watch.nextCheckAt ? (
            <div><dt className="visually-hidden">Next check</dt><dd>Next {formatTime(watch.nextCheckAt)}</dd></div>
          ) : null}
          <div>
            <dt className="visually-hidden">Last checked</dt>
            <dd>{watch.lastCheckedAt ? `Last ${formatDateTime(watch.lastCheckedAt)}` : 'Not checked yet'}</dd>
          </div>
          <div className="watch__seen">
            <dt className="visually-hidden">Sources seen</dt>
            <dd><strong>{watch.seenCount}</strong> seen</dd>
          </div>
          <div><dt className="visually-hidden">Ends</dt><dd>{proposed || active ? 'Ends' : 'Ended'} {formatShortDate(watch.expiresAt)}</dd></div>
        </dl>
      </div>
      <div className="watch__actions">
        {proposed ? <span className="pill pill--accent watch__flag"><span className="pill__dot" aria-hidden="true" />Needs review</span> : null}
        <AutomationActions itemLabel={watch.query} layout="inline" size="sm"
          approveDisabledReason={approveDisabledReason}
          deletePrompt={active ? 'Stop and delete this watch?' : 'Delete this watch?'}
          onApprove={proposed ? () => onResolve(watch.id, 'accepted') : undefined}
          onDecline={proposed ? () => onResolve(watch.id, 'rejected') : undefined}
          onDelete={() => onDelete(watch.id)} />
      </div>
    </article>
  )
}

export function WatchesView({ data, currentTime, onDelete, onResolve }: {
  data: WorkspaceData
  currentTime: Date
  onDelete: (resourceId: string) => Promise<void>
  onResolve: (resourceId: string, decision: ProposalDecision) => Promise<void>
}) {
  const byCreated = (a: WatchItem, b: WatchItem) => Date.parse(b.createdAt) - Date.parse(a.createdAt)
  const proposed = data.watches.filter((watch) => watch.status === 'proposed').sort(byCreated)
  const active = data.watches.filter((watch) => watch.status === 'active').sort(byCreated)
  const ended = data.watches.filter((watch) => watch.status === 'expired' || watch.status === 'rejected').sort(byCreated)
  const row = (watch: WatchItem) => (
    <WatchRow key={watch.id} watch={watch} currentTime={currentTime} onDelete={onDelete} onResolve={onResolve} />
  )

  return (
    <>
      <PageIntro
        title="Watches"
        lede="Eyes checks these on a schedule and tells you when something changes."
        action={<CapacityMeter active={active.length} />}
      />
      {!data.watches.length ? (
        <EmptyState title="Nothing being watched" body="Ask Eyes to keep an eye on something, like a price, a listing, or a result." />
      ) : (
        <div className="watch-groups">
          {proposed.length ? (
            <section className="watch-group watch-group--review" aria-labelledby="watch-review-title">
              <h2 className="section-title" id="watch-review-title">
                <span className="eyebrow eyebrow--accent">Needs review</span>
                <span>{proposed.length}</span>
              </h2>
              <div className="watch-list">{proposed.map(row)}</div>
            </section>
          ) : null}
          <section className="watch-group" aria-labelledby="watch-active-title">
            <h2 className="section-title" id="watch-active-title">
              <span className="eyebrow">Active</span>
              <span>{active.length}</span>
            </h2>
            {active.length ? <div className="watch-list">{active.map(row)}</div>
              : <p className="watch-group__empty">No active watches. Approve a suggestion or ask Eyes to watch something.</p>}
          </section>
          {ended.length ? (
            <details className="watch-group watch-group--ended">
              <summary><span className="eyebrow">Ended</span><span>{ended.length}</span></summary>
              <div className="watch-list">{ended.map(row)}</div>
            </details>
          ) : null}
        </div>
      )}
    </>
  )
}
