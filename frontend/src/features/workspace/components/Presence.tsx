import './Presence.css'

export type PresenceState = {
  label: string
  detail: string
  active: boolean
}

export function Presence({
  variant,
  state,
  deviceName = 'Even G2',
  reconnecting = false,
  onReconnect,
}: {
  variant: 'hero' | 'compact'
  state: PresenceState
  deviceName?: string
  reconnecting?: boolean
  onReconnect?: () => void
}) {
  const mark = (
    <span
      className={`presence__mark${state.active ? ' presence__mark--active' : ''}`}
      aria-hidden="true"
    />
  )

  if (variant === 'compact') {
    return (
      <div className="presence presence--compact" role="status">
        {mark}
        <span className="presence__copy">
          <strong>{deviceName}</strong>
          <span>{state.label}</span>
        </span>
      </div>
    )
  }

  return (
    <div className="presence presence--hero" role="status" aria-live="polite">
      <div className="presence__head">
        {mark}
        <span className="presence__device">{deviceName}</span>
        <span className="presence__label">{state.label}</span>
      </div>
      <p className="presence__detail">{state.detail}</p>
      {!state.active && onReconnect ? (
        <button
          className="text-action presence__reconnect"
          type="button"
          disabled={reconnecting}
          onClick={onReconnect}
        >
          {reconnecting ? 'Reconnecting…' : 'Reconnect glasses'}
        </button>
      ) : null}
    </div>
  )
}
