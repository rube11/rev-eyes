type Cleanup = () => void | Promise<void>

// A new runtime must wait for pending setup and cleanup of the previous one.
export class ConnectionSession {
  private generation = 0
  private tail: Promise<unknown> = Promise.resolve()
  private cleanup: Cleanup | undefined

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
    await cleanup?.()
    this.cleanup = undefined
  }
}
