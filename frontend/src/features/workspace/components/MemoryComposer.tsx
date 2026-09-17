import { useEffect, useState } from 'react'
import type { FormEvent } from 'react'
import { memoryKinds, memoryTopics } from '../memoryOptions'
import type { MemoryKind, NewMemoryInput } from '../workspaceTypes'
import { CloseIcon } from './icons'
import { Pills } from './Pills'
import './MemoryComposer.css'

const kindOptions = memoryKinds.map((kind) => ({ value: kind, label: kind }))
const topicOptions = memoryTopics.map((topic) => ({ value: topic, label: topic }))

export function MemoryComposer({
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
        <header className="memory-composer__head">
          <div>
            <p className="eyebrow">New memory</p>
            <h2 id="memory-composer-title">What should Eyes remember?</h2>
          </div>
          <button
            className="icon-action"
            type="button"
            onClick={onClose}
            aria-label="Close memory form"
          >
            <CloseIcon />
          </button>
        </header>
        <form className="memory-composer__form" onSubmit={submit}>
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
              rows={5}
              placeholder="The thing itself, in a sentence or two."
              required
            />
            <small>{summary.length} / 500</small>
          </label>
          <div className="memory-composer__group">
            <span className="field-label">Kind</span>
            <Pills label="Memory kind" options={kindOptions} value={kind} size="sm"
              onChange={(value) => setKind(value as MemoryKind)} />
          </div>
          <div className="memory-composer__group">
            <span className="field-label">Topic</span>
            <Pills label="Memory topic" options={topicOptions} value={topic} size="sm"
              onChange={setTopic} />
          </div>
          {error ? (
            <p className="form-error" role="alert">{error}</p>
          ) : null}
          <footer className="memory-composer__foot">
            <span className="memory-composer__note">Saved to your memories and available to Eyes right away.</span>
            <button className="primary-action" type="submit" disabled={saving}>
              {saving ? 'Saving…' : 'Save memory'}
            </button>
          </footer>
        </form>
      </section>
    </div>
  )
}
