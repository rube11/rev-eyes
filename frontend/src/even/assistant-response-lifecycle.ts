type TimerHandle = unknown
type Options = {
  conversationWindowMs?: number
  onConversationExpired: () => void
  scheduleTimer?: (callback: () => void, delayMs: number) => TimerHandle
  cancelTimer?: (handle: TimerHandle) => void
}

// This deadline controls microphone capture only. Reading has no deadline.
export class AssistantResponseLifecycle {
  private timer: TimerHandle | undefined
  private generation = 0
  private conversationActive = false
  private readonly scheduleTimer: NonNullable<Options["scheduleTimer"]>
  private readonly cancelTimer: NonNullable<Options["cancelTimer"]>
  private readonly options: Options

  constructor(options: Options) {
    this.options = options
    this.scheduleTimer = options.scheduleTimer ?? ((callback, delay) => setTimeout(callback, delay))
    this.cancelTimer = options.cancelTimer ?? (handle => clearTimeout(handle as ReturnType<typeof setTimeout>))
  }

  get active(): boolean { return this.conversationActive }

  begin(): void {
    this.cancel()
    const generation = this.generation
    this.conversationActive = true
    this.timer = this.scheduleTimer(() => {
      if (generation !== this.generation || !this.conversationActive) return
      this.timer = undefined
      this.conversationActive = false
      this.options.onConversationExpired()
    }, this.options.conversationWindowMs ?? 30_000)
  }

  cancel(): void {
    this.generation += 1
    this.conversationActive = false
    if (this.timer !== undefined) {
      this.cancelTimer(this.timer)
      this.timer = undefined
    }
  }
}
