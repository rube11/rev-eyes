import { useEffect, useMemo, useRef, useState, useSyncExternalStore } from 'react'
import type { ReactNode } from 'react'
import { env } from '../../../shared/config/env'
import { AgentStatusStore, agentStatusLabel } from '../agentStatus'
import type { AgentSnapshot } from '../agentStatus'
import { openAgentThread, sendAgentMessage } from '../agentClient'
import type { AgentTarget, AgentThreadDetail } from '../agentClient'
import { AgentSendKeys } from '../agentSendKeys'
import { PageIntro } from '../components/PageIntro'
import { RefreshIcon } from '../components/icons'
import './AgentsView.css'

function sameThread(left?: AgentTarget, right?: AgentTarget) {
  return Boolean(left && right && left.connectionId === right.connectionId && left.threadId === right.threadId)
}

type PanelProps = {
  snapshot: AgentSnapshot
  onRefresh: () => void
  onSelect?: (target: AgentTarget) => void
  selected?: AgentTarget
  onMore?: (id: string) => void
  children?: ReactNode
}

export function AgentStatusPanel({ snapshot, onRefresh, onSelect, selected, onMore, children }: PanelProps) {
  const loading = snapshot.phase === 'loading'
  return <>
    <PageIntro title="Agents" lede="Choose a thread. Tap your glasses to speak."
      action={<button className="primary-action primary-action--quiet" type="button" onClick={onRefresh} disabled={loading}>
        <RefreshIcon />{loading ? 'Loading…' : 'Refresh'}
      </button>} />
    {children}
    <section className="agents-provider" aria-labelledby="t3-title" aria-busy={loading}>
      <header className="agents-provider__header"><h2 id="t3-title">T3 Code</h2></header>
      <div role="status" aria-live="polite">
        {loading ? <p className="agents-message">Loading threads…</p> : null}
        {snapshot.phase === 'error' ? <p className="agents-message">Couldn’t load threads. Check Dubs and refresh.</p> : null}
        {snapshot.phase === 'ready' && snapshot.connections.length === 0 ? <p className="agents-message">T3 isn’t connected to this account.</p> : null}
      </div>
      {snapshot.phase === 'ready' ? snapshot.connections.map((connection) => <section key={connection.id} aria-label={`${connection.provider} threads`}>
        <div className="agents-connection__status">
          <strong>{connection.provider.toLowerCase() === 't3' ? 'Your threads' : connection.provider}</strong>
          <span className="pill">{connection.unavailable ? 'Offline' : connection.canSend ? 'Connected' : 'View only'}</span>
        </div>
        {connection.unavailable ? <p className="agents-message">Couldn’t reach T3. Check Dubs and refresh.</p> : <>
          {connection.threads.length === 0 ? <p className="agents-message">No threads on this page.</p> : <ul className="agent-threads">
            {connection.threads.map((thread) => {
              const next = { connectionId: connection.id, threadId: thread.id, title: thread.title }
              return <li key={thread.id}>
                <button type="button" className={`agent-thread${sameThread(selected, next) ? ' agent-thread--selected' : ''}`}
                  disabled={!onSelect || (!connection.canSend && !connection.canOpen)}
                  onClick={() => onSelect?.(next)} aria-pressed={sameThread(selected, next)}>
                  <div><h3>{thread.title}</h3>{thread.updatedAt ? <p>{new Date(thread.updatedAt).toLocaleString()}</p> : null}</div>
                  <span className="agent-thread__status">{agentStatusLabel(thread.status)}</span>
                </button>
              </li>
            })}
          </ul>}
          {connection.hasMore ? <div className="agents-page-note">
            <button type="button" className="primary-action primary-action--quiet" onClick={() => onMore?.(connection.id)} disabled={!onMore}>More threads</button>
          </div> : null}
        </>}
      </section>) : null}
    </section>
    <p className="agents-footnote">Dots isn’t connected yet.</p>
  </>
}

type AgentsViewProps = {
  accessToken?: string
  target?: AgentTarget
  onTargetChange?: (target?: AgentTarget) => void
}

// The parent keys this view by account. A routine token refresh keeps the draft
// and retry keys; switching accounts still unmounts and clears the whole view.
export function AgentsView(props: AgentsViewProps) { return <ConnectedAgents {...props} /> }

function ConnectedAgents({ accessToken, target, onTargetChange }: AgentsViewProps) {
  const store = useMemo(() => new AgentStatusStore(async (signal, page) => {
    if (!accessToken) throw new Error('Sign-in required')
    const url = new URL(`${env.apiBaseUrl}/workspace/agents`)
    if (page) { url.searchParams.set('connection_id', page.connectionId); url.searchParams.set('cursor', String(page.cursor)) }
    const response = await fetch(url, { headers: { Authorization: `Bearer ${accessToken}` }, signal, cache: 'no-store' })
    if (!response.ok) throw new Error('Couldn’t load threads')
    return response.json()
  }), [accessToken])
  const snapshot = useSyncExternalStore(store.subscribe, store.getSnapshot)
  const [selected, setSelected] = useState<AgentTarget | undefined>(target)
  const [detail, setDetail] = useState<AgentThreadDetail>()
  const [draft, setDraft] = useState('')
  const [notice, setNotice] = useState('')
  const [error, setError] = useState('')
  const [listError, setListError] = useState('')
  const [reading, setReading] = useState(false)
  const [sending, setSending] = useState(false)
  const [more, setMore] = useState(false)
  const busy = useRef(false)
  const sendKeys = useRef(new AgentSendKeys())
  const readRequest = useRef<AbortController | undefined>(undefined)
  const conversationPanel = useRef<HTMLElement | null>(null)
  const mounted = useRef(true)
  const generation = useRef(0)

  useEffect(() => { store.start(); return () => store.dispose() }, [store])
  useEffect(() => {
    mounted.current = true
    return () => { mounted.current = false; readRequest.current?.abort() }
  }, [])
  useEffect(() => { if (selected) conversationPanel.current?.scrollIntoView({ behavior: 'smooth', block: 'start' }) }, [selected])

  const connection = snapshot.connections.find((entry) => entry.id === selected?.connectionId && !entry.unavailable)
  // Refreshing the first list page must not disable a thread chosen on a later page.
  const available = snapshot.phase === 'ready' && Boolean(connection)

  const read = async (next: AgentTarget, after?: number, fromStart = false) => {
    readRequest.current?.abort()
    if (!accessToken) return
    const controller = new AbortController()
    readRequest.current = controller
    const timeout = setTimeout(() => controller.abort(), 8_000)
    setReading(true)
    setError('')
    try {
      const data = await openAgentThread(accessToken, next, controller.signal, after, fromStart)
      if (mounted.current && readRequest.current === controller && !controller.signal.aborted) {
        setDetail((previous) => after === undefined ? data : {
          ...data,
          messages: [...new Map([...(previous?.messages ?? []), ...data.messages].map((message) => [message.id, message])).values()],
          truncated: data.truncated || previous?.truncated === true,
        })
      }
    } catch {
      if (mounted.current && readRequest.current === controller) setError('Couldn’t read this thread. Try again.')
    } finally {
      clearTimeout(timeout)
      if (mounted.current && readRequest.current === controller) setReading(false)
    }
  }

  const select = (next: AgentTarget) => {
    if (busy.current) return
    if (sameThread(next, selected)) {
      if (!detail && !reading && connection?.canOpen) void read(next)
      return
    }
    readRequest.current?.abort()
    readRequest.current = undefined
    generation.current++
    setSelected(next)
    setDetail(undefined)
    setDraft('')
    setNotice('')
    setError('')
    setReading(false)
    const provider = snapshot.connections.find((entry) => entry.id === next.connectionId)
    if (provider?.canSend) onTargetChange?.(next)
    if (provider?.canOpen) void read(next)
  }

  const close = () => {
    generation.current++
    readRequest.current?.abort()
    readRequest.current = undefined
    setSelected(undefined)
    if (sameThread(target, selected)) onTargetChange?.()
  }

  const send = async () => {
    if (!accessToken || !selected || !available || !connection?.canSend || !draft.trim() || busy.current) return
    busy.current = true
    setSending(true)
    setError('')
    setNotice('')
    const current = generation.current
    const attempt = sendKeys.current.prepare(selected, draft.trim())
    try {
      await sendAgentMessage(accessToken, selected, draft.trim(), attempt.key)
      sendKeys.current.complete(attempt.signature)
      if (mounted.current && generation.current === current) {
        setDraft('')
        setNotice('Sent to T3. Refresh for a reply.')
        void read(selected)
      }
    } catch (caught) {
      if (mounted.current && generation.current === current) setError(caught instanceof Error ? caught.message : 'Couldn’t confirm delivery. Check T3 before retrying.')
    } finally {
      busy.current = false
      if (mounted.current) setSending(false)
    }
  }

  const loadMore = (id: string) => {
    setMore(true)
    setListError('')
    void store.loadMore(id).catch(() => { if (mounted.current) setListError('Couldn’t load more threads. Try again.') })
      .finally(() => { if (mounted.current) setMore(false) })
  }

  return <AgentStatusPanel snapshot={snapshot} selected={selected} onSelect={sending ? undefined : select}
    onRefresh={() => { if (!busy.current) { setListError(''); void store.refresh() } }} onMore={more ? undefined : loadMore}>
    {selected ? <section ref={conversationPanel} className="agents-provider agent-conversation" aria-label="Selected thread">
      <header className="agents-provider__header">
        <h2>{selected.title}</h2>
        <button type="button" className="primary-action primary-action--quiet" disabled={sending} onClick={close}>Close</button>
      </header>
      <div className="agent-conversation__controls">
        {sameThread(target, selected) ? <span className="pill pill--accent">On glasses</span> : <button type="button" className="primary-action"
          disabled={!available || !connection?.canSend || !onTargetChange || sending} onClick={() => onTargetChange?.(selected)}>Use on glasses</button>}
        <button type="button" className="primary-action primary-action--quiet" disabled={!available || !connection?.canOpen || reading || sending}
          onClick={() => void read(selected)}>{reading ? 'Loading…' : 'Refresh reply'}</button>
        {target ? <button type="button" className="primary-action primary-action--quiet" onClick={() => onTargetChange?.()}>Back to Eyes</button> : null}
        <p>Tap your glasses to speak.</p>
      </div>
      <div className="agent-conversation__messages">
        {detail?.messages.map((message) => <article key={message.id} className={`agent-message agent-message--${message.role}`}>
          <p className="eyebrow">{message.role === 'user' ? 'You' : 'T3'}</p><p>{message.text}</p>
        </article>)}
        {detail && detail.messages.length === 0 && !reading ? <p>No messages in this view.</p> : null}
        {detail?.hasEarlier ? <button type="button" className="primary-action primary-action--quiet" disabled={reading || sending}
          onClick={() => void read(selected, undefined, true)}>Earlier messages</button> : null}
        {detail?.hasMore && detail.nextPosition !== undefined ? <button type="button" className="primary-action primary-action--quiet" disabled={reading || sending}
          onClick={() => void read(selected, detail.nextPosition)}>More messages</button> : null}
        {detail?.truncated ? <p>Open T3 for the full text of long messages.</p> : null}
      </div>
      <form className="agent-composer" onSubmit={(event) => { event.preventDefault(); void send() }}>
        <label htmlFor="agent-draft">Message this thread</label>
        <textarea id="agent-draft" rows={3} maxLength={4000} value={draft} disabled={sending || !available || !connection?.canSend}
          onChange={(event) => setDraft(event.target.value)} placeholder="What’s next?" />
        <button className="primary-action" type="submit" disabled={sending || !draft.trim() || !available || !connection?.canSend}>{sending ? 'Sending…' : 'Send'}</button>
        {notice ? <p role="status">{notice}</p> : null}
        {error ? <p className="form-error" role="alert">{error}</p> : null}
      </form>
    </section> : null}
    {listError ? <p className="form-error" role="alert">{listError}</p> : null}
  </AgentStatusPanel>
}
