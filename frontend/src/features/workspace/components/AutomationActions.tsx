import { useState } from 'react'
import './AutomationActions.css'

type AutomationActionState = 'approve' | 'decline' | 'delete'

export function AutomationActions({
  itemLabel,
  approveDisabledReason,
  deletePrompt,
  layout = 'stack',
  size = 'md',
  onApprove,
  onDecline,
  onDelete,
}: {
  itemLabel: string
  approveDisabledReason?: string
  deletePrompt: string
  layout?: 'stack' | 'inline'
  size?: 'md' | 'sm'
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

  const className = [
    'automation-actions',
    `automation-actions--${layout}`,
    `automation-actions--${size}`,
  ].join(' ')

  return (
    <div className={className} aria-live="polite">
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
            className={`automation-action automation-action--danger${onApprove ? '' : ' automation-action--quiet'}`}
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
        <span className="automation-actions__hint">{approveDisabledReason}</span>
      ) : null}
      {error ? (
        <span className="automation-actions__error" role="alert">{error}</span>
      ) : null}
    </div>
  )
}
