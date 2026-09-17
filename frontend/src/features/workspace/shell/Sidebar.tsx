import { navItems } from '../navigation'
import type { WorkspaceView } from '../workspaceTypes'
import { NavIcon } from '../components/icons'
import { Presence } from '../components/Presence'
import type { PresenceState } from '../components/Presence'
import './Sidebar.css'

export function Sidebar({
  view,
  onNavigate,
  pendingCounts,
  assistant,
  isDemo,
  accountLabel,
  accountInitial,
  accountAction,
  onSignOut,
}: {
  view: WorkspaceView
  onNavigate: (view: WorkspaceView) => void
  pendingCounts: Partial<Record<WorkspaceView, number>>
  assistant: PresenceState
  isDemo: boolean
  accountLabel: string
  accountInitial: string
  accountAction: string
  onSignOut: () => void
}) {
  return (
    <aside className="sidebar">
      <div className="sidebar__brand">
        <button
          className="wordmark"
          type="button"
          onClick={() => onNavigate('now')}
          aria-label="Go to Home"
        >
          rev<span className="wordmark__slash">/</span>eyes
        </button>
      </div>
      <nav className="sidebar__nav" aria-label="Main navigation">
        {navItems.map((item) => {
          const count = pendingCounts[item.id]
          const active = view === item.id
          return (
            <button
              type="button"
              key={item.id}
              className={`sidebar__link${active ? ' is-active' : ''}`}
              aria-current={active ? 'page' : undefined}
              onClick={() => onNavigate(item.id)}
            >
              <NavIcon view={item.id} />
              <span className="sidebar__label">{item.label}</span>
              {count !== undefined ? (
                <span className="sidebar__count tnum" aria-label={`${count} to review`}>{count}</span>
              ) : null}
            </button>
          )
        })}
      </nav>
      <div className="sidebar__foot">
        <Presence variant="compact" state={assistant} deviceName={isDemo ? 'Preview' : 'Even G2'} />
        <button className="sidebar__account" type="button" onClick={onSignOut}>
          <span className="sidebar__avatar" aria-hidden="true">{accountInitial}</span>
          <span className="sidebar__account-copy">
            <strong>{accountLabel}</strong>
            <small>{accountAction}</small>
          </span>
        </button>
      </div>
    </aside>
  )
}
