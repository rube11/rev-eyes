import { useEffect, useMemo, useState } from 'react'
import type { FormEvent, KeyboardEvent } from 'react'
import { EmptyState } from '../components/EmptyState'
import { PageIntro } from '../components/PageIntro'
import { Pills } from '../components/Pills'
import { ArrowIcon, ChevronIcon, PlusIcon, SearchIcon } from '../components/icons'
import { formatDate, formatDateTime, relativeTime } from '../format'
import {
  familyLabel, familyOf, isActiveMemory, isStaleMemory, profileEntries, recentEntries, shelfLabels, shelfOrder,
} from '../memoryModel'
import type { ConversationItem, MemoryEdit, MemoryItem, MemoryLayer, WorkspaceData } from '../workspaceTypes'
import '../components/AutomationActions.css'
import './MemoriesView.css'

type EditMemory = (memoryId: string, edit: MemoryEdit) => Promise<void>

function sentence(text: string): string {
  const trimmed = text.replace(/\s+/gu, ' ').trim()
  return /[.!?…]$/u.test(trimmed) ? trimmed : `${trimmed}.`
}

function MemoryActions({ memory, stale, onEdit, onStartEdit }: {
  memory: MemoryItem
  stale: boolean
  onEdit: EditMemory
  onStartEdit: () => void
}) {
  const [pending, setPending] = useState<string>()
  const [confirming, setConfirming] = useState(false)
  const [error, setError] = useState('')
  const busy = pending !== undefined

  const run = async (label: string, edit: MemoryEdit) => {
    setPending(label)
    setError('')
    try {
      await onEdit(memory.id, edit)
      setConfirming(false)
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : 'We could not update this memory. Please try again.')
    } finally {
      setPending(undefined)
    }
  }

  if (memory.status === 'forgotten') {
    return (
      <div className="automation-actions automation-actions--inline automation-actions--sm" aria-live="polite">
        <div className="automation-actions__buttons">
          <button className="automation-action automation-action--primary" type="button" disabled={busy}
            aria-label={`Restore ${memory.title}`} onClick={() => void run('restore', { action: 'restore' })}>
            {pending === 'restore' ? 'Restoring…' : 'Restore'}
          </button>
        </div>
        {error ? <span className="automation-actions__error" role="alert">{error}</span> : null}
      </div>
    )
  }

  return (
    <div className="automation-actions automation-actions--inline automation-actions--sm" aria-live="polite">
      {confirming ? (
        <div className="automation-actions__confirm">
          <span>Forget this? Eyes stops using it right away. You can restore it for 30 days.</span>
          <button className="automation-action" type="button" disabled={busy} onClick={() => setConfirming(false)}>Keep</button>
          <button className="automation-action automation-action--danger" type="button" disabled={busy}
            onClick={() => void run('forget', { action: 'forget' })}>
            {pending === 'forget' ? 'Forgetting…' : 'Forget'}
          </button>
        </div>
      ) : (
        <div className="automation-actions__buttons">
          {stale ? (
            <button className="automation-action automation-action--primary" type="button" disabled={busy}
              aria-label={`Confirm ${memory.title} is still true`}
              onClick={() => void run('keep', { action: 'update', title: memory.title, summary: memory.summary })}>
              {pending === 'keep' ? 'Keeping…' : 'Still true'}
            </button>
          ) : null}
          <button className="automation-action" type="button" disabled={busy}
            aria-label={memory.pinned ? `Unpin ${memory.title} from your profile` : `Pin ${memory.title} to your profile`}
            onClick={() => void run('pin', { action: memory.pinned ? 'unpin' : 'pin' })}>
            {pending === 'pin' ? 'Saving…' : memory.pinned ? 'Unpin' : 'Pin to profile'}
          </button>
          <button className="automation-action" type="button" disabled={busy}
            aria-label={`Edit ${memory.title}`} onClick={onStartEdit}>
            Edit
          </button>
          <button className="automation-action automation-action--danger automation-action--quiet" type="button" disabled={busy}
            aria-label={`Forget ${memory.title}`} onClick={() => { setError(''); setConfirming(true) }}>
            Forget
          </button>
        </div>
      )}
      {error ? <span className="automation-actions__error" role="alert">{error}</span> : null}
    </div>
  )
}

function MemoryEditor({ memory, onEdit, onDone }: {
  memory: MemoryItem
  onEdit: EditMemory
  onDone: () => void
}) {
  const [title, setTitle] = useState(memory.title)
  const [summary, setSummary] = useState(memory.summary)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    setSaving(true)
    setError('')
    try {
      await onEdit(memory.id, { action: 'update', title, summary })
      onDone()
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : 'We could not save this change. Please try again.')
    } finally {
      setSaving(false)
    }
  }

  return (
    <form className="memory-edit" onSubmit={submit}>
      <label className="field">
        <span>Title</span>
        <input autoFocus value={title} maxLength={120} required onChange={(event) => setTitle(event.target.value)} />
      </label>
      <label className="field">
        <span>Details</span>
        <textarea value={summary} maxLength={500} rows={4} required onChange={(event) => setSummary(event.target.value)} />
        <small>{summary.length} / 500</small>
      </label>
      {error ? <p className="form-error" role="alert">{error}</p> : null}
      <div className="memory-edit__actions">
        <button className="primary-action" type="submit" disabled={saving || !title.trim() || !summary.trim()}>
          {saving ? 'Saving…' : 'Save correction'}
        </button>
        <button className="text-action" type="button" disabled={saving} onClick={onDone}>Cancel</button>
      </div>
    </form>
  )
}

function MemoryCard({ memory, open, filter, now, source, onToggle, onFilter, onEdit, onOpenConversation }: {
  memory: MemoryItem
  open: boolean
  filter: string
  now: number
  source?: ConversationItem
  onToggle: () => void
  onFilter: (topic: string) => void
  onEdit: EditMemory
  onOpenConversation: (conversationId: string) => void
}) {
  const [editing, setEditing] = useState(false)
  const bodyId = `memory-${memory.id}`
  const forgotten = memory.status === 'forgotten'
  const stale = isStaleMemory(memory, now)
  const handleKeyDown = (event: KeyboardEvent<HTMLElement>) => {
    if (event.key === 'Escape' && open && !editing) {
      event.stopPropagation()
      onToggle()
      event.currentTarget.querySelector<HTMLButtonElement>('.memory-card__head')?.focus()
    }
  }

  const className = [
    'memory-card',
    open ? 'is-open' : '',
    forgotten ? 'memory-card--forgotten' : '',
    memory.pinned ? 'memory-card--pinned' : '',
  ].filter(Boolean).join(' ')

  return (
    <article id={`memory-card-${memory.id}`} className={className} onKeyDown={handleKeyDown}>
      <button className="memory-card__head" type="button" aria-expanded={open} aria-controls={bodyId}
        onClick={() => { setEditing(false); onToggle() }}>
        <span className="memory-card__kind eyebrow">
          {memory.kind}
          <span className="memory-card__when tnum">· {forgotten ? `forgotten ${relativeTime(memory.inactiveAt ?? memory.updatedAt, now)}` : relativeTime(memory.observedAt, now)}</span>
          {memory.pinned ? <span className="memory-card__flag">Pinned</span> : null}
          {memory.layer === 'recent' && memory.expiresAt ? (
            <span className="memory-card__flag memory-card__flag--fading">fades {relativeTime(memory.expiresAt, now)}</span>
          ) : null}
          {stale ? <span className="memory-card__flag memory-card__flag--stale">Still true?</span> : null}
        </span>
        <h3 className="memory-card__title">{memory.title}</h3>
        <ChevronIcon className="memory-card__chevron" />
      </button>
      {editing ? (
        <MemoryEditor memory={memory} onEdit={onEdit} onDone={() => setEditing(false)} />
      ) : (
        <p className="memory-card__summary">{memory.summary}</p>
      )}
      {memory.topics.length && !editing ? (
        <div className="memory-card__topics" aria-label="Topics">
          {memory.topics.map((topic) => (
            <button key={topic} type="button" className={`pill pill--outline memory-card__topic${filter === topic ? ' is-selected' : ''}`}
              aria-pressed={filter === topic} onClick={() => onFilter(filter === topic ? 'all' : topic)}>
              {topic}
            </button>
          ))}
        </div>
      ) : null}
      <div className="memory-card__more" id={bodyId} inert={!open}>
        <div className="memory-card__body">
          <p className="memory-card__source">
            {source ? (
              <>
                Heard {relativeTime(memory.observedAt, now)} in{' '}
                <button type="button" className="memory-card__source-link" onClick={() => onOpenConversation(source.id)}>
                  {source.title}<ArrowIcon size={12} />
                </button>
              </>
            ) : memory.sourceUtteranceId ? (
              <>Heard {relativeTime(memory.observedAt, now)} in a conversation that is no longer listed.</>
            ) : (
              <>Added by you {relativeTime(memory.createdAt, now)}.</>
            )}
          </p>
          <dl className="memory-card__facts">
            <div><dt>Profile</dt><dd>{memory.pinned ? 'Core, pinned by you' : shelfLabels[memory.layer]}</dd></div>
            {familyOf(memory) ? <div><dt>About</dt><dd>{familyLabel(familyOf(memory)!)}</dd></div> : null}
            <div><dt>Updated</dt><dd>{formatDateTime(memory.updatedAt)}</dd></div>
            <div><dt>First saved</dt><dd>{formatDate(memory.createdAt)}</dd></div>
            {memory.expiresAt ? <div><dt>Expires</dt><dd>{formatDateTime(memory.expiresAt)}</dd></div> : null}
          </dl>
          {!editing ? (
            <MemoryActions memory={memory} stale={stale} onEdit={onEdit} onStartEdit={() => setEditing(true)} />
          ) : null}
        </div>
      </div>
    </article>
  )
}

export function MemoriesView({ data, currentTime, initialExpandedId, onAdd, onEdit, onOpenConversation }: {
  data: WorkspaceData
  currentTime: Date
  initialExpandedId?: string
  onAdd: () => void
  onEdit: EditMemory
  onOpenConversation: (conversationId: string) => void
}) {
  const now = currentTime.getTime()
  const [query, setQuery] = useState('')
  const [filter, setFilter] = useState('all')
  const [expandedId, setExpandedId] = useState<string | undefined>(initialExpandedId)

  useEffect(() => {
    if (!initialExpandedId) return
    document.getElementById(`memory-card-${initialExpandedId}`)?.scrollIntoView({ block: 'center' })
  }, [initialExpandedId])

  const conversations = useMemo(
    () => new Map(data.conversations.map((conversation) => [conversation.id, conversation])),
    [data.conversations],
  )
  const active = useMemo(() => data.memories.filter((memory) => isActiveMemory(memory, now)), [data.memories, now])
  const forgotten = useMemo(() => data.memories.filter((memory) => memory.status === 'forgotten'), [data.memories])
  const profile = useMemo(() => profileEntries(data.memories, now), [data.memories, now])
  const recent = useMemo(() => recentEntries(data.memories, now), [data.memories, now])
  const staleCount = active.filter((memory) => isStaleMemory(memory, now)).length

  const familyOptions = useMemo(() => {
    const counts = new Map<string, number>()
    for (const memory of active) {
      const family = familyOf(memory)
      if (family) counts.set(family, (counts.get(family) ?? 0) + 1)
    }
    return [...counts.entries()]
      .sort(([a, countA], [b, countB]) => countB - countA || a.localeCompare(b))
      .map(([family, count]) => ({ value: `family:${family}`, label: familyLabel(family), count }))
  }, [active])

  const topicOptions = useMemo(() => {
    const counts = new Map<string, number>()
    for (const memory of active) {
      for (const topic of memory.topics) counts.set(topic, (counts.get(topic) ?? 0) + 1)
    }
    return [
      { value: 'all', label: 'All', count: active.length },
      ...[...counts.entries()].sort(([a], [b]) => a.localeCompare(b))
        .map(([topic, count]) => ({ value: topic, label: topic, count })),
    ]
  }, [active])

  const matchesFilter = (memory: MemoryItem) =>
    filter === 'all'
      || (filter.startsWith('family:') ? familyOf(memory) === filter.slice('family:'.length) : memory.topics.includes(filter))
  const matches = (memory: MemoryItem) => {
    const normalized = query.toLowerCase().trim()
    return matchesFilter(memory) &&
      (!normalized || [memory.title, memory.summary, memory.kind, ...memory.topics]
        .join(' ').toLowerCase().includes(normalized))
  }
  const filtered = active.filter(matches)
  const filteredForgotten = forgotten.filter(matches)
  const filtering = Boolean(query.trim()) || filter !== 'all'
  const shelves = shelfOrder
    .map((layer) => ({ layer, items: filtered.filter((memory) => memory.layer === layer) }))
    .filter((shelf) => shelf.items.length)

  const focus = (memoryId: string) => {
    setExpandedId(memoryId)
    requestAnimationFrame(() => {
      document.getElementById(`memory-card-${memoryId}`)?.scrollIntoView({ block: 'center', behavior: 'smooth' })
    })
  }

  const card = (memory: MemoryItem) => (
    <li key={memory.id}>
      <MemoryCard
        memory={memory}
        open={expandedId === memory.id}
        filter={filter}
        now={now}
        source={memory.sourceConversationId ? conversations.get(memory.sourceConversationId) : undefined}
        onToggle={() => setExpandedId((current) => current === memory.id ? undefined : memory.id)}
        onFilter={setFilter}
        onEdit={onEdit}
        onOpenConversation={onOpenConversation}
      />
    </li>
  )

  return (
    <>
      <PageIntro
        title="What Eyes knows"
        lede={active.length
          ? `${active.length} ${active.length === 1 ? 'thing' : 'things'} Eyes keeps in mind when it answers you${staleCount ? `, ${staleCount} worth a second look` : ''}.`
          : 'What Eyes keeps in mind when it answers you.'}
        action={
          <button className="primary-action" type="button" onClick={onAdd}>
            <PlusIcon />Add
          </button>
        }
      />

      {profile.length || recent.length ? (
        <section className="profile" aria-label="Your profile">
          <header className="profile__head">
            <p className="eyebrow">Profile · carried into every answer</p>
            <span className="profile__count tnum">{profile.length} of {active.length}</span>
          </header>
          {profile.length ? (
            <p className="profile__prose">
              {profile.map((memory) => (
                <span
                  key={memory.id}
                  role="button"
                  tabIndex={0}
                  className="profile__line"
                  aria-label={`Show memory: ${memory.title}`}
                  onClick={() => focus(memory.id)}
                  onKeyDown={(event) => {
                    if (event.key === 'Enter' || event.key === ' ') {
                      event.preventDefault()
                      focus(memory.id)
                    }
                  }}
                >
                  {sentence(memory.summary)}
                </span>
              ))}
            </p>
          ) : (
            <p className="profile__empty">Nothing is pinned to your profile yet. Pin a memory below and Eyes will carry it into every answer.</p>
          )}
          {recent.length ? (
            <div className="profile__recent">
              <p className="eyebrow">Recent · fades on its own</p>
              <ul>
                {recent.map((memory) => (
                  <li key={memory.id}>
                    <button type="button" onClick={() => focus(memory.id)}>{sentence(memory.summary)}</button>
                    <span className="tnum">fades {relativeTime(memory.expiresAt!, now)}</span>
                  </li>
                ))}
              </ul>
            </div>
          ) : null}
        </section>
      ) : null}

      <div className="memories-toolbar">
        <label className="search-field">
          <SearchIcon />
          <span className="visually-hidden">Search memories</span>
          <input type="search" value={query}
            onChange={(event) => setQuery(event.target.value)}
            placeholder="Search what Eyes knows" />
        </label>
        {familyOptions.length ? (
          <div className="memories-toolbar__row">
            <span className="memories-toolbar__label eyebrow">About</span>
            <Pills label="Filter memories by what they describe" options={familyOptions} value={filter} onChange={setFilter} scroll />
          </div>
        ) : null}
        {topicOptions.length > 1 ? (
          <div className="memories-toolbar__row">
            {familyOptions.length ? <span className="memories-toolbar__label eyebrow">Topics</span> : null}
            <Pills label="Filter memories by topic" options={topicOptions} value={filter} onChange={setFilter} scroll />
          </div>
        ) : null}
      </div>
      <p className="result-count">{filtered.length} {filtered.length === 1 ? 'memory' : 'memories'}</p>

      {shelves.length ? (
        <div className="shelves">
          {shelves.map(({ layer, items }) => (
            <section key={layer} className={`shelf shelf--${layer}`} aria-labelledby={`shelf-${layer}`}>
              <h2 className="section-title" id={`shelf-${layer}`}>
                <span className="eyebrow">{shelfLabels[layer]}</span>
                <span>{items.length}</span>
              </h2>
              <p className="shelf__note">{shelfNotes[layer]}</p>
              <ul className="memory-grid">{items.map(card)}</ul>
            </section>
          ))}
        </div>
      ) : (
        <EmptyState
          title={filtering ? 'No matches' : 'Nothing remembered yet'}
          body={filtering
            ? 'Try another search or topic.'
            : 'Tell Eyes something worth keeping, or add a memory here.'}
        />
      )}

      {filteredForgotten.length ? (
        <details className="shelf shelf--forgotten">
          <summary><span className="eyebrow">Forgotten recently</span><span className="tnum">{filteredForgotten.length}</span></summary>
          <p className="shelf__note">Eyes no longer uses these. Restore one within 30 days to bring it back.</p>
          <ul className="memory-grid">{filteredForgotten.map(card)}</ul>
        </details>
      ) : null}
    </>
  )
}

const shelfNotes: Record<MemoryLayer, string> = {
  core: 'Standing context. Eyes reads these before every answer.',
  recent: 'Time-boxed. These expire on their own once they stop being useful.',
  detail: 'Searchable background. Eyes looks these up when a question calls for them.',
}
