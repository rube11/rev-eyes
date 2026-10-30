import { useEffect, useMemo, useRef, useState, useSyncExternalStore } from 'react'
import { env } from '../../../shared/config/env'
import { AgentStatusStore } from '../agentStatus'
import type { AgentSnapshot } from '../agentStatus'
import { openAgentThread, sendAgentMessage } from '../agentClient'
import type { AgentTarget, AgentThreadDetail } from '../agentClient'
import { PageIntro } from '../components/PageIntro'
import { RefreshIcon } from '../components/icons'
import './AgentsView.css'

type PanelProps = { snapshot: AgentSnapshot; onRefresh: () => void; onSelect?: (target: AgentTarget) => void; selected?: AgentTarget; onMore?: (id: string) => void }
export function AgentStatusPanel({ snapshot, onRefresh, onSelect, selected, onMore }: PanelProps) {
  const loading = snapshot.phase === 'loading'
  return <>
    <PageIntro title="Agents" lede="Pick a thread. Keep the work moving from your glasses."
      action={<button className="primary-action primary-action--quiet" type="button" onClick={onRefresh} disabled={loading}><RefreshIcon />{loading ? 'Checking…' : 'Refresh threads'}</button>} />
    <section className="agents-provider" aria-labelledby="t3-title" aria-busy={loading}>
      <header className="agents-provider__header"><div><p className="eyebrow">Coding workspace</p><h2 id="t3-title">T3 Code</h2></div><span className="pill">Your threads</span></header>
      <div role="status" aria-live="polite">
        {loading ? <p className="agents-message">Checking your configured connection…</p> : null}
        {snapshot.phase === 'error' ? <div className="agents-message"><h3>Couldn’t load agent status</h3><p>Check your connection or sign in again, then refresh.</p></div> : null}
        {snapshot.phase === 'ready' && snapshot.connections.length === 0 ? <div className="agents-message"><h3>Not configured for this account</h3><p>Connect your T3 environment to load its threads. Connection setup is managed on the server for now.</p></div> : null}
      </div>
      {snapshot.phase === 'ready' ? snapshot.connections.map((connection) => <section className="agents-connection" key={connection.id} aria-label={`${connection.provider} status`}>
        <div className="agents-connection__status"><strong>{connection.provider}</strong><span className="pill">{connection.unavailable ? 'Unavailable' : connection.canSend ? 'Messaging enabled' : 'Read-only preview'}</span></div>
        {connection.unavailable ? <p className="agents-message">The provider may be offline or its authorization may have expired. Refresh to check again.</p> : <>
          {!connection.threads.length ? <p className="agents-message">No threads returned on this page. This is not a complete inventory.</p> : <ul className="agent-threads">{connection.threads.map((thread) => <li key={thread.id}>
            <button type="button" className={`agent-thread${selected?.connectionId === connection.id && selected.threadId === thread.id ? ' agent-thread--selected' : ''}`} disabled={!onSelect || (!connection.canSend && !connection.canOpen)} onClick={() => onSelect?.({ connectionId: connection.id, threadId: thread.id, title: thread.title })} aria-pressed={selected?.connectionId === connection.id && selected.threadId === thread.id}>
              <div><h3>{thread.title}</h3><p>{thread.updatedAt ? `Updated ${new Date(thread.updatedAt).toLocaleString()}` : 'T3 thread'}</p></div><span className="agent-thread__status">{thread.status.replaceAll('_', ' ')}</span>
            </button>
          </li>)}</ul>}
          {connection.hasMore ? <div className="agents-page-note"><button type="button" className="primary-action primary-action--quiet" onClick={() => onMore?.(connection.id)} disabled={!onMore}>Load more threads</button></div> : null}
        </>}
      </section>) : null}
      {snapshot.checkedAt ? <p className="agents-checked">Last checked {new Date(snapshot.checkedAt).toLocaleTimeString()}</p> : null}
    </section>
    <section className="agents-provider agents-provider--future" aria-labelledby="dot-title"><header className="agents-provider__header"><h2 id="dot-title">ChatGPT Dots</h2><span className="pill">Connection research</span></header><p>Not connected. We’re checking supported ways to reach your existing dot.</p></section>
  </>
}

type AgentsViewProps = { accessToken?: string; target?: AgentTarget; onTargetChange?: (target?: AgentTarget) => void }
export function AgentsView(props: AgentsViewProps) { return <ConnectedAgents key={props.accessToken} {...props} /> }
function ConnectedAgents({ accessToken, target, onTargetChange }: AgentsViewProps) {
  const store = useMemo(() => new AgentStatusStore(async (signal, page) => {
    if (!accessToken) throw new Error('Sign-in required')
    const url = new URL(`${env.apiBaseUrl}/workspace/agents`)
    if (page) { url.searchParams.set('connection_id', page.connectionId); url.searchParams.set('cursor', String(page.cursor)) }
    const response = await fetch(url, { headers: { Authorization: `Bearer ${accessToken}` }, signal, cache: 'no-store' })
    if (!response.ok) throw new Error('Agent status unavailable')
    return response.json()
  }), [accessToken])
  const snapshot = useSyncExternalStore(store.subscribe, store.getSnapshot)
  const [selected, setSelected] = useState<AgentTarget | undefined>(target)
  const [detail, setDetail] = useState<AgentThreadDetail>()
  const [draft, setDraft] = useState('')
  const [notice, setNotice] = useState('')
  const [error, setError] = useState('')
  const [reading, setReading] = useState(false)
  const [sending, setSending] = useState(false)
  const [more, setMore] = useState(false)
  const busy = useRef(false)
  const pendingSend = useRef<{ signature: string; key: string } | undefined>(undefined)
  const readRequest = useRef<AbortController | undefined>(undefined)
  const conversationPanel = useRef<HTMLElement | null>(null)
  const mounted = useRef(true)
  const generation = useRef(0)
  useEffect(() => { mounted.current = true; store.start(); return () => { mounted.current = false; store.dispose(); readRequest.current?.abort() } }, [store])
  useEffect(() => { if (selected) conversationPanel.current?.scrollIntoView({ behavior: 'smooth', block: 'start' }) }, [selected])
  const connection = snapshot.connections.find((entry) => entry.id === selected?.connectionId && !entry.unavailable)
  const available = snapshot.phase === 'ready' && connection?.threads.some((thread) => thread.id === selected?.threadId)
  const read = async (next: AgentTarget, after?: number) => {
    readRequest.current?.abort()
    if (!accessToken) return
    const controller = new AbortController()
    readRequest.current = controller
    const timeout = setTimeout(() => controller.abort(), 8_000)
    setReading(true); setError('')
    try { const data = await openAgentThread(accessToken, next, controller.signal, after); if (mounted.current && readRequest.current === controller && !controller.signal.aborted) setDetail((previous) => after === undefined ? data : { ...data, messages: [...new Map([...(previous?.messages ?? []), ...data.messages].map((message) => [message.id, message])).values()] }) }
    catch { if (mounted.current && readRequest.current === controller) setError('Couldn’t read the thread. Check T3 and refresh.') }
    finally { clearTimeout(timeout); if (mounted.current && readRequest.current === controller) setReading(false) }
  }
  const select = (next: AgentTarget) => {
    if (busy.current) return
    readRequest.current?.abort(); readRequest.current = undefined; setReading(false)
    generation.current++; setSelected(next); setDetail(undefined); setDraft(''); setNotice(''); setError('')
    const provider = snapshot.connections.find((entry) => entry.id === next.connectionId)
    if (provider?.canSend) onTargetChange?.(next)
    if (provider?.canOpen) void read(next)
  }
  const send = async () => {
    if (!accessToken || !selected || !available || !connection?.canSend || !draft.trim() || busy.current) return
    busy.current = true; setSending(true); setError(''); setNotice('')
    const current = generation.current
    try {
      const signature = JSON.stringify([selected.connectionId, selected.threadId, draft.trim()])
      if (pendingSend.current?.signature !== signature) pendingSend.current = { signature, key: crypto.randomUUID() }
      await sendAgentMessage(accessToken, selected, draft.trim(), pendingSend.current.key)
      pendingSend.current = undefined
      if (mounted.current && generation.current === current) { setDraft(''); setNotice('Message queued in T3. Refresh the conversation to see its reply.'); void read(selected) }
    } catch (caught) { if (mounted.current && generation.current === current) setError(caught instanceof Error ? caught.message : 'Delivery is uncertain. Check the thread before sending again.') }
    finally { busy.current = false; if (mounted.current) setSending(false) }
  }
  return <>
    <AgentStatusPanel snapshot={snapshot} selected={selected} onSelect={sending ? undefined : select} onRefresh={() => { if (!busy.current) void store.refresh() }} onMore={more ? undefined : (id) => { setMore(true); void store.loadMore(id).catch((caught: Error) => setError(caught.message)).finally(() => setMore(false)) }} />
    {selected ? <section ref={conversationPanel} className="agents-provider agent-conversation" aria-label="Selected T3 thread">
      <header className="agents-provider__header"><div><p className="eyebrow">Selected thread</p><h2>{selected.title}</h2></div><button type="button" className="primary-action primary-action--quiet" disabled={sending} onClick={() => { generation.current++; readRequest.current?.abort(); readRequest.current = undefined; setSelected(undefined); if (target?.connectionId === selected.connectionId && target.threadId === selected.threadId) onTargetChange?.() }}>Close</button></header>
      <div className="agent-conversation__controls"><button type="button" className="primary-action" disabled={!available || !connection?.canSend || !onTargetChange || sending} onClick={() => onTargetChange?.(selected)}>{target?.connectionId === selected.connectionId && target.threadId === selected.threadId ? 'Selected on glasses' : 'Use on glasses'}</button><p>Tap your glasses to speak to this thread. Hold to return to Eyes.</p><button type="button" className="primary-action primary-action--quiet" disabled={!available || !connection?.canOpen || reading || sending} onClick={() => void read(selected)}>{reading ? 'Loading…' : 'Refresh conversation'}</button>{target ? <button type="button" className="primary-action primary-action--quiet" onClick={() => onTargetChange?.()}>Return glasses to Eyes</button> : null}</div>
      <div className="agent-conversation__messages" aria-live="polite">{detail?.messages.map((message) => <article key={message.id} className={`agent-message agent-message--${message.role}`}><p className="eyebrow">{message.role === 'user' ? 'You' : 'T3'}</p><p>{message.text}</p></article>)}{detail?.hasMore && detail.nextPosition !== undefined ? <button type="button" className="primary-action primary-action--quiet" disabled={reading || sending} onClick={() => void read(selected, detail.nextPosition)}>Load next messages</button> : null}{detail?.truncated ? <p>Long messages are shortened here. Open T3 for their full text.</p> : null}</div>
      <form className="agent-composer" onSubmit={(event) => { event.preventDefault(); void send() }}><label htmlFor="agent-draft">Message this thread</label><textarea id="agent-draft" rows={3} maxLength={4000} value={draft} disabled={sending || !available || !connection?.canSend} onChange={(event) => setDraft(event.target.value)} placeholder="What should we work on next?" /><button className="primary-action" type="submit" disabled={sending || !draft.trim() || !available || !connection?.canSend}>{sending ? 'Sending…' : 'Send to T3'}</button></form>
      {notice ? <p className="agents-message" role="status">{notice}</p> : null}
    </section> : null}
    {error ? <p className="form-error" role="alert">{error}</p> : null}
  </>
}
