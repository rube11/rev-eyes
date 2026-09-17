import { RefreshIcon } from '../components/icons'
import type { PresenceState } from '../components/Presence'
import { formatDateTime } from '../format'
import './Topbar.css'

export function Topbar({
  title,
  syncing,
  lastSyncedAt,
  onRetry,
  assistant,
  isDemo,
  reconnecting,
  onReconnectGlasses,
  accountNote,
  accountInitial,
  accountAction,
  onSignOut,
}: {
  title: string
  syncing: boolean
  lastSyncedAt?: string
  onRetry?: () => void
  assistant: PresenceState
  isDemo: boolean
  reconnecting: boolean
  onReconnectGlasses?: () => void
  accountNote: string
  accountInitial: string
  accountAction: string
  onSignOut: () => void
}) {
  const connected = assistant.active
  const canReconnect = !isDemo && Boolean(onReconnectGlasses)

  return (
    <header className="topbar">
      <div className="topbar__location">
        <span className="wordmark topbar__wordmark" aria-hidden="true">
          rev<span className="wordmark__slash">/</span>eyes
        </span>
        <span className="topbar__title eyebrow">{title}</span>
      </div>
      <div className="topbar__right">
        {onRetry ? (
          <button
            className="text-action topbar__refresh"
            type="button"
            disabled={syncing}
            onClick={onRetry}
            title={lastSyncedAt ? `Last updated ${formatDateTime(lastSyncedAt)}` : 'Reload saved items'}
          >
            <RefreshIcon className={syncing ? 'is-spinning' : undefined} />
            <span>{syncing ? 'Refreshing…' : 'Refresh'}</span>
          </button>
        ) : null}
        <details className="topbar-menu">
          <summary
            className={`topbar__connection${connected ? ' is-online' : ''}`}
            aria-label={`${isDemo ? 'Preview' : 'Even G2'} ${assistant.label}. Connection details`}
          >
            <span className="topbar__connection-mark" aria-hidden="true" />
            <span className="topbar__connection-label">{assistant.label}</span>
          </summary>
          <div className="topbar-menu__panel">
            <strong>{isDemo ? 'Preview' : 'Even G2'}</strong>
            <p>{assistant.detail}</p>
            {canReconnect ? (
              <button
                className="primary-action primary-action--quiet topbar-menu__action"
                type="button"
                disabled={reconnecting}
                onClick={onReconnectGlasses}
              >
                {reconnecting ? 'Reconnecting…' : 'Reconnect glasses'}
              </button>
            ) : null}
          </div>
        </details>
        {canReconnect && !connected ? (
          <button
            className="topbar__retry"
            type="button"
            aria-label="Reconnect glasses"
            disabled={reconnecting}
            onClick={onReconnectGlasses}
          >
            {reconnecting ? 'Reconnecting…' : 'Reconnect'}
          </button>
        ) : null}
        <details className="topbar-menu">
          <summary className="topbar__avatar" aria-label="Account menu">{accountInitial}</summary>
          <div className="topbar-menu__panel">
            <p>{accountNote}</p>
            <button className="primary-action primary-action--quiet topbar-menu__action" type="button" onClick={onSignOut}>
              {accountAction}
            </button>
          </div>
        </details>
      </div>
    </header>
  )
}
