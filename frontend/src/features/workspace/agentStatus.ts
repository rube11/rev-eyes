export type AgentThread = { id: string; title: string; status: string; updatedAt?: string }
export type AgentConnection = { id: string; provider: string; unavailable: boolean; threads: AgentThread[]; hasMore: boolean; cursor?: number; canSend: boolean; canOpen: boolean }
export type AgentSnapshot = {
  phase: 'loading' | 'ready' | 'error'
  connections: AgentConnection[]
  checkedAt?: string
}

function record(value: unknown): Record<string, unknown> {
  if (!value || typeof value !== 'object' || Array.isArray(value)) throw new Error('Invalid agent response')
  return value as Record<string, unknown>
}
function label(value: unknown, fallback: string): string {
  return typeof value === 'string' && value.trim() ? value.slice(0, 240) : fallback
}

// Provider strings are display-only, never links, HTML, commands or authority.
export function parseAgentConnections(value: unknown): AgentConnection[] {
  const payload = record(value)
  if (!Array.isArray(payload.connections) || payload.limit !== 20) throw new Error('Invalid agent response')
  return payload.connections.map((raw) => {
    const entry = record(raw)
    const caps = record(entry.capabilities)
    const base = { id: label(entry.connection_id, 'connection'), provider: label(entry.provider, 'Agent'), threads: [] as AgentThread[], hasMore: false, canSend: caps.send === true, canOpen: caps.open === true }
    if (entry.error || caps.list !== true) return { ...base, unavailable: true }
    try {
      const data = record(entry.untrusted_provider_data)
      if (!Array.isArray(data.threads) || data.threads.length > 20) throw new Error('Invalid thread page')
      const threads = data.threads.map((rawThread, index) => {
        const thread = record(rawThread)
        if (typeof thread.threadId !== 'string' || !/^[a-zA-Z0-9_.:-]{1,256}$/u.test(thread.threadId)) throw new Error('Invalid thread ID')
        return { id: thread.threadId, title: label(thread.title, `Thread ${index + 1}`), status: label(thread.status, 'Status not reported'), updatedAt: typeof thread.updatedAt === 'string' && !Number.isNaN(Date.parse(thread.updatedAt)) ? thread.updatedAt : undefined }
      })
      return { ...base, unavailable: false, threads, hasMore: typeof data.nextCursor === 'number' && Number.isSafeInteger(data.nextCursor) && data.nextCursor >= 0, cursor: typeof data.nextCursor === 'number' ? data.nextCursor : undefined }
    } catch { return { ...base, unavailable: true } }
  })
}

// One request at a time; no polling/retries. Disposed/older requests cannot publish.
export class AgentStatusStore {
  private snapshot: AgentSnapshot = { phase: 'loading', connections: [] }
  private listeners = new Set<() => void>()
  private controller?: AbortController
  private disposed = false
  private load: (signal: AbortSignal, page?: { connectionId: string; cursor: number }) => Promise<unknown>
  private timeoutMs: number
  constructor(load: (signal: AbortSignal, page?: { connectionId: string; cursor: number }) => Promise<unknown>, timeoutMs = 8_000) { this.load = load; this.timeoutMs = timeoutMs }
  getSnapshot = () => this.snapshot
  subscribe = (listener: () => void) => { this.listeners.add(listener); return () => { this.listeners.delete(listener) } }
  private publish(snapshot: AgentSnapshot) { this.snapshot = snapshot; this.listeners.forEach((listener) => listener()) }
  start() { this.disposed = false; void this.refresh() }
  refresh = async () => {
    if (this.disposed || this.controller) return
    const controller = new AbortController()
    this.controller = controller
    this.publish({ phase: 'loading', connections: [] })
    const timeout = setTimeout(() => controller.abort(), this.timeoutMs)
    try {
      const connections = parseAgentConnections(await this.load(controller.signal))
      if (!this.disposed && this.controller === controller && !controller.signal.aborted) this.publish({ phase: 'ready', connections, checkedAt: new Date().toISOString() })
      else if (!this.disposed && this.controller === controller) this.publish({ phase: 'error', connections: [] })
    } catch {
      if (!this.disposed && this.controller === controller) this.publish({ phase: 'error', connections: [] })
    } finally { clearTimeout(timeout); if (this.controller === controller) this.controller = undefined }
  }
  loadMore = async (connectionId: string) => {
    const previous = this.snapshot
    const current = previous.connections.find((entry) => entry.id === connectionId)
    if (this.disposed || this.controller || current?.cursor === undefined || !current.hasMore) return
    const controller = new AbortController()
    this.controller = controller
    const timeout = setTimeout(() => controller.abort(), this.timeoutMs)
    try {
      const page = parseAgentConnections(await this.load(controller.signal, { connectionId, cursor: current.cursor }))
      const next = page.find((entry) => entry.id === connectionId)
      if (!next || next.unavailable || next.cursor === current.cursor) throw new Error('Invalid page')
      if (this.disposed || this.controller !== controller || controller.signal.aborted) return
      const threads = [...new Map([...current.threads, ...next.threads].map((thread) => [thread.id, thread])).values()]
      this.publish({ ...previous, connections: previous.connections.map((entry) => entry.id === connectionId ? { ...next, threads } : entry) })
    } catch {
      if (!this.disposed && this.controller === controller) throw new Error('Couldn’t load more threads. Try again.')
    } finally { clearTimeout(timeout); if (this.controller === controller) this.controller = undefined }
  }
  dispose() { this.disposed = true; this.controller?.abort(); this.controller = undefined }
}
