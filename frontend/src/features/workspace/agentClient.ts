import { env } from '../../shared/config/env'
import { parseAgentConnections } from './agentStatus'
import type { AgentConnection } from './agentStatus'

export type AgentTarget = { connectionId: string; threadId: string; title: string }
export type AgentMessage = { id: string; role: 'user' | 'assistant'; text: string }
export type AgentThreadDetail = { messages: AgentMessage[]; hasMore: boolean; nextPosition?: number; truncated: boolean; hasEarlier?: boolean }

export async function loadAgentConnections(accessToken: string, signal?: AbortSignal, page?: { connectionId: string; cursor: number }): Promise<AgentConnection[]> {
  const url = new URL(`${env.apiBaseUrl}/workspace/agents`)
  if (page) { url.searchParams.set('connection_id', page.connectionId); url.searchParams.set('cursor', String(page.cursor)) }
  const response = await fetch(url, { headers: { Authorization: `Bearer ${accessToken}` }, cache: 'no-store', signal })
  if (!response.ok) throw new Error('Couldn’t load your T3 threads.')
  return parseAgentConnections(await response.json())
}

async function action(accessToken: string, name: string, target: AgentTarget, input: Record<string, string | number | boolean> = {}, signal?: AbortSignal): Promise<unknown> {
  const response = await fetch(`${env.apiBaseUrl}/workspace/agents/${name}`, {
    method: 'POST', headers: { Authorization: `Bearer ${accessToken}`, 'Content-Type': 'application/json' },
    body: JSON.stringify({ connection_id: target.connectionId, thread_id: target.threadId, ...input }), signal,
  })
  if (!response.ok) {
    const payload = await response.json().catch(() => ({})) as { error?: string }
    throw new Error(payload.error === 'delivery_unknown'
      ? 'Couldn’t confirm delivery. Check T3 before retrying.'
      : 'Couldn’t reach T3. Check Dubs and try again.')
  }
  return response.json()
}

export async function openAgentThread(accessToken: string, target: AgentTarget, signal?: AbortSignal, afterPosition?: number, fromStart = false): Promise<AgentThreadDetail> {
  const value = await action(accessToken, 'thread', target, fromStart ? { from_start: true } : afterPosition === undefined ? {} : { after_position: afterPosition }, signal) as { thread?: { threadId?: string }; items?: unknown[]; hasMore?: boolean; nextPosition?: number; hasEarlier?: boolean }
  if (!value || typeof value !== 'object' || value.thread?.threadId !== target.threadId || (!Array.isArray(value.items) || value.items.length > 20)) throw new Error('Couldn’t read this thread.')
  const messages = value.items.flatMap((item, messagesIndex): AgentMessage[] => {
    if (!item || typeof item !== 'object') return []
    const row = item as Record<string, unknown>
    if (typeof row.text !== 'string' || (row.type !== 'user_message' && row.type !== 'assistant_message')) return []
    return [{ id: typeof row.itemId === 'string' ? row.itemId : `message-${messagesIndex}`, role: row.type === 'user_message' ? 'user' : 'assistant', text: row.text.slice(0, 2000) }]
  })
  return { messages, hasEarlier: value.hasEarlier === true, hasMore: value.hasMore === true, nextPosition: typeof value.nextPosition === 'number' && Number.isSafeInteger(value.nextPosition) && value.nextPosition >= 0 ? value.nextPosition : undefined, truncated: value.items.some((item) => Boolean(item && typeof item === 'object' && 'textTruncated' in item && item.textTruncated)) }
}

export async function sendAgentMessage(accessToken: string, target: AgentTarget, text: string, requestId: string): Promise<void> {
  // If the connection drops after dispatch, never retry with a new key.
  const controller = new AbortController()
  const timeout = setTimeout(() => controller.abort(), 10_000)
  try { await action(accessToken, 'messages', target, { text, request_id: requestId }, controller.signal) }
  catch (error) {
    if (controller.signal.aborted || error instanceof TypeError || error instanceof SyntaxError) throw new Error('Couldn’t confirm delivery. Check T3 before retrying.', { cause: error })
    throw error
  } finally { clearTimeout(timeout) }
}
