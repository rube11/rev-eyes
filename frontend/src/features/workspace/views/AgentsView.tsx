import { useEffect, useMemo, useSyncExternalStore } from 'react'
import { env } from '../../../shared/config/env'
import { AgentStatusStore } from '../agentStatus'
import type { AgentSnapshot } from '../agentStatus'
import { PageIntro } from '../components/PageIntro'
import { RefreshIcon } from '../components/icons'
import './AgentsView.css'

export function AgentStatusPanel({ snapshot, onRefresh }: { snapshot: AgentSnapshot; onRefresh: () => void }) {
  const loading = snapshot.phase === 'loading'
  return <>
    <PageIntro title="Agents" lede="A quiet view of your coding threads. Your glasses pipeline stays the same."
      action={<button className="primary-action primary-action--quiet" type="button" onClick={onRefresh} disabled={loading}><RefreshIcon />{loading ? 'Checking…' : 'Refresh agents'}</button>} />
    <div className="agents-boundary"><span className="pill pill--accent">Read-only preview</span><p>List and status only. Sending, starting, interrupting, approvals and opening thread contents are not available.</p></div>
    <section className="agents-provider" aria-labelledby="t3-title" aria-busy={loading}>
      <header className="agents-provider__header"><div><p className="eyebrow">Coding workspace</p><h2 id="t3-title">T3 Code</h2></div><span className="pill">First 20 threads</span></header>
      <div role="status" aria-live="polite">
        {loading ? <p className="agents-message">Checking your configured connection…</p> : null}
        {snapshot.phase === 'error' ? <div className="agents-message"><h3>Couldn’t load agent status</h3><p>Check your connection or sign in again, then refresh. No action was taken.</p></div> : null}
        {snapshot.phase === 'ready' && snapshot.connections.length === 0 ? <div className="agents-message"><h3>Not configured for this account</h3><p>No T3 connection is configured for you. This integration is off by default; it does not pair with your laptop yet.</p></div> : null}
      </div>
      {snapshot.phase === 'ready' ? snapshot.connections.map((connection, index) => <section className="agents-connection" key={`${connection.id}-${index}`} aria-label={`${connection.provider} status`}>
        <div className="agents-connection__status"><strong>{connection.provider}</strong><span className={`pill${connection.unavailable ? '' : ' pill--accent'}`}>{connection.unavailable ? 'Unavailable' : 'Status received'}</span></div>
        {connection.unavailable ? <p className="agents-message">The provider may be offline, authorization may have expired, or this version may be incompatible. Refresh to check again; no action was taken.</p> : <>
          {!connection.threads.length ? <p className="agents-message">No threads returned on this page. This is not a complete inventory.</p> : <ul className="agent-threads">{connection.threads.map((thread, threadIndex) => <li className="agent-thread" key={`${thread.id}-${threadIndex}`}><div><h3>{thread.title}</h3><p>{thread.id}</p></div><span className="agent-thread__status">{thread.status}</span></li>)}</ul>}
          <p className="agents-page-note">{connection.hasMore ? 'More threads are available. ' : ''}Showing at most the first 20 threads across projects. Pagination is not available yet; a missing thread may still exist.</p>
        </>}
      </section>) : null}
      {snapshot.checkedAt ? <p className="agents-checked">Last checked {new Date(snapshot.checkedAt).toLocaleTimeString()} · Manual refresh only</p> : null}
    </section>
    <section className="agents-provider agents-provider--future" aria-labelledby="dot-title"><header className="agents-provider__header"><h2 id="dot-title">Dots</h2><span className="pill">Not supported</span></header><p>No dot connection or messaging API is implemented. This placeholder is not connected.</p></section>
    <details className="agents-setup"><summary>How will my laptop connect?</summary><p>Secure laptop pairing is a future step: an authenticated, revocable connection initiated by your laptop. Hosted Eyes cannot reach your laptop’s localhost.</p><p>Today, only an operator-configured, previously authorized HTTPS T3 MCP connection is supported. Keep credentials on the server. Do not expose a local T3 port or paste tokens here.</p></details>
  </>
}

export function AgentsView({ accessToken }: { accessToken?: string }) {
  const store = useMemo(() => new AgentStatusStore(async (signal) => {
    if (!accessToken) throw new Error('Sign-in required')
    const response = await fetch(`${env.apiBaseUrl}/workspace/agents`, { headers: { Authorization: `Bearer ${accessToken}` }, signal, cache: 'no-store' })
    if (!response.ok) throw new Error('Agent status unavailable')
    return response.json()
  }), [accessToken])
  const snapshot = useSyncExternalStore(store.subscribe, store.getSnapshot)
  useEffect(() => { store.start(); return () => store.dispose() }, [store])
  return <AgentStatusPanel snapshot={snapshot} onRefresh={() => { void store.refresh() }} />
}
