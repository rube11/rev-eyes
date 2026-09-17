import './DataNotice.css'

export function DataNotice({
  failures,
  syncing,
  onRetry,
}: {
  failures: { label: string; state: 'stale' | 'unavailable' }[]
  syncing: boolean
  onRetry?: () => void
}) {
  return (
    <div className="data-notice" role="status">
      <div className="data-notice__copy">
        <strong>Some sections couldn’t refresh.</strong>
        <p>
          {failures.map(({ label, state }) => `${label}: ${state === 'stale' ? 'showing last loaded data' : 'unavailable'}`).join(' · ')}.
          {' '}We’ll keep retrying; other sections remain available.
        </p>
      </div>
      {onRetry ? (
        <button type="button" className="text-action data-notice__retry" disabled={syncing} onClick={onRetry}>
          {syncing ? 'Retrying…' : 'Try again'}
        </button>
      ) : null}
    </div>
  )
}
