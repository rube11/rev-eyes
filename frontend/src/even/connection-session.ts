type Cleanup = () => void | Promise<void>

const DEFAULT_CLEANUP_TIMEOUT_MS = 2_000

// A new runtime waits for ordinary cleanup, but a lost native bridge cannot
// permanently prevent a replacement runtime from starting.
export class ConnectionSession {
  private generation = 0
  private tail: Promise<unknown> = Promise.resolve()
  private cleanup: Cleanup | undefined
  private readonly cleanupTimeoutMs: number

  constructor(cleanupTimeoutMs = DEFAULT_CLEANUP_TIMEOUT_MS) {
    this.cleanupTimeoutMs = cleanupTimeoutMs
  }

  start(initialize: () => Promise<Cleanup>): Promise<void> {
    const generation = ++this.generation
    const pending = this.tail.then(async () => {
      await this.clear()
      if (generation !== this.generation) return
      const cleanup = await initialize()
      this.cleanup = cleanup
      if (generation !== this.generation) await this.clear()
    })
    this.tail = pending.catch(() => undefined)
    return pending
  }

  stop(): Promise<void> {
    ++this.generation
    const pending = this.tail.then(() => this.clear())
    this.tail = pending.catch(() => undefined)
    return pending
  }

  private async clear(): Promise<void> {
    const cleanup = this.cleanup
    // Retain a failed cleanup so a subsequent start cannot silently bypass it.
    if (!cleanup) return
    const attempt = Promise.resolve().then(cleanup)
    let timedOut = false
    let timer: ReturnType<typeof setTimeout> | undefined
    try {
      await Promise.race([
        attempt,
        new Promise<void>((resolve) => {
          timer = setTimeout(() => {
            timedOut = true
            resolve()
          }, this.cleanupTimeoutMs)
        }),
      ])
    } finally {
      if (timer !== undefined) clearTimeout(timer)
    }
    if (timedOut) void attempt.catch(() => undefined)
    this.cleanup = undefined
  }
}
