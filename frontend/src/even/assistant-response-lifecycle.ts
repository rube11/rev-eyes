const DISPLAY_BASE_MS = 2_000
const DISPLAY_PER_WORD_MS = 1_000 / 3
const DISPLAY_MIN_MS = 5_000
const DISPLAY_MAX_MS = 14_000
const CONVERSATION_GRACE_MS = 8_000

type TimerHandle = unknown
type ScheduleTimer = (callback: () => void, delayMs: number) => TimerHandle
type CancelTimer = (handle: TimerHandle) => void

type AssistantResponseLifecycleOptions = {
  cancelTimer?: CancelTimer
  conversationGraceMs?: number
  onConversationExpired: () => void
  onDisplayExpired: () => void
  scheduleTimer?: ScheduleTimer
}

function defaultScheduleTimer(
  callback: () => void,
  delayMs: number,
): TimerHandle {
  return setTimeout(callback, delayMs)
}

function defaultCancelTimer(handle: TimerHandle): void {
  clearTimeout(handle as ReturnType<typeof setTimeout>)
}

function responseWordCount(text: string): number {
  return text.match(/[\p{L}\p{N}]+(?:['’][\p{L}\p{N}]+)*/gu)?.length ?? 0
}

export function responseDisplayMilliseconds(text: string): number {
  const calculated =
    DISPLAY_BASE_MS + responseWordCount(text) * DISPLAY_PER_WORD_MS
  return Math.round(
    Math.min(DISPLAY_MAX_MS, Math.max(DISPLAY_MIN_MS, calculated)),
  )
}

// Owns only response-card and follow-up deadlines. Presentation and audio
// policy stay in runtime.ts so timer callbacks cannot mutate either directly.
export class AssistantResponseLifecycle {
  private readonly cancelTimer: CancelTimer
  private readonly conversationGraceMs: number
  private readonly onConversationExpired: () => void
  private readonly onDisplayExpired: () => void
  private readonly scheduleTimer: ScheduleTimer
  private displayTimer: TimerHandle | undefined
  private conversationTimer: TimerHandle | undefined
  private generation = 0
  private conversationActive = false

  constructor(options: AssistantResponseLifecycleOptions) {
    this.cancelTimer = options.cancelTimer ?? defaultCancelTimer
    this.conversationGraceMs =
      options.conversationGraceMs ?? CONVERSATION_GRACE_MS
    this.onConversationExpired = options.onConversationExpired
    this.onDisplayExpired = options.onDisplayExpired
    this.scheduleTimer = options.scheduleTimer ?? defaultScheduleTimer
  }

  get active(): boolean {
    return this.conversationActive
  }

  begin(text: string): number {
    this.cancel()
    const generation = this.generation
    const displayMs = responseDisplayMilliseconds(text)
    this.conversationActive = true
    this.displayTimer = this.scheduleTimer(() => {
      if (generation !== this.generation || !this.conversationActive) {
        return
      }
      this.displayTimer = undefined
      this.onDisplayExpired()
    }, displayMs)
    this.conversationTimer = this.scheduleTimer(() => {
      if (generation !== this.generation || !this.conversationActive) {
        return
      }
      this.conversationTimer = undefined
      this.conversationActive = false
      this.onConversationExpired()
    }, displayMs + this.conversationGraceMs)
    return displayMs
  }

  cancel(): void {
    this.generation += 1
    this.conversationActive = false
    if (this.displayTimer !== undefined) {
      this.cancelTimer(this.displayTimer)
      this.displayTimer = undefined
    }
    if (this.conversationTimer !== undefined) {
      this.cancelTimer(this.conversationTimer)
      this.conversationTimer = undefined
    }
  }
}
