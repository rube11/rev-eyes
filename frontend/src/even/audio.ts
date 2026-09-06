export type AudioCaptureState = "idle" | "starting" | "running"

export type AudioDeviceControls = {
  start: () => Promise<boolean>
  stop: () => Promise<boolean>
}

// AudioCaptureController serializes the device's asynchronous start/stop edge.
// Policy such as socket availability and sleep state stays with the runtime.
export class AudioCaptureController {
  private readonly device: AudioDeviceControls
  private currentState: AudioCaptureState = "idle"
  private generation = 0
  private startPromise: Promise<boolean> | undefined
  private stopPromise: Promise<boolean> | undefined
  private disposed = false

  constructor(device: AudioDeviceControls) {
    this.device = device
  }

  get state(): AudioCaptureState {
    return this.currentState
  }

  get running(): boolean {
    return this.currentState === "running"
  }

  start(isStillAllowed: () => boolean = () => true): Promise<boolean> {
    if (this.disposed) {
      return Promise.resolve(false)
    }
    if (this.currentState === "running") {
      return Promise.resolve(true)
    }
    if (this.currentState === "starting" && this.startPromise) {
      return this.startPromise
    }

    const priorStart = this.startPromise
    const priorStop = this.stopPromise
    const generation = ++this.generation
    this.currentState = "starting"
    const attempt =
      priorStart || priorStop
        ? this.startAfterPriorTransitions(
            generation,
            isStillAllowed,
            priorStart,
            priorStop,
          )
        : this.startAttempt(generation, isStillAllowed)
    const tracked = attempt.finally(() => {
      if (this.startPromise === tracked) {
        this.startPromise = undefined
      }
    })
    this.startPromise = tracked
    return tracked
  }

  stop(): Promise<boolean> {
    this.generation += 1
    this.currentState = "idle"
    const priorStop = this.stopPromise
    const attempt = (priorStop ?? Promise.resolve(true))
      .catch(() => false)
      .then(() => this.device.stop().catch(() => false))
    const tracked = attempt.finally(() => {
      if (this.stopPromise === tracked) {
        this.stopPromise = undefined
      }
    })
    this.stopPromise = tracked
    return tracked
  }

  async dispose(): Promise<boolean> {
    if (this.disposed) {
      return true
    }
    this.disposed = true
    return this.stop()
  }

  private async startAttempt(
    generation: number,
    isStillAllowed: () => boolean,
  ): Promise<boolean> {
    const started = await this.device.start().catch(() => false)
    const allowed = (() => {
      try {
        return isStillAllowed()
      } catch {
        return false
      }
    })()
    if (
      !started ||
      this.disposed ||
      generation !== this.generation ||
      !allowed
    ) {
      if (started) {
        await this.device.stop().catch(() => false)
      }
      if (generation === this.generation) {
        this.currentState = "idle"
      }
      return false
    }

    this.currentState = "running"
    return true
  }

  private async startAfterPriorTransitions(
    generation: number,
    isStillAllowed: () => boolean,
    priorStart: Promise<boolean> | undefined,
    priorStop: Promise<boolean> | undefined,
  ): Promise<boolean> {
    await Promise.allSettled([
      priorStart ?? Promise.resolve(false),
      priorStop ?? Promise.resolve(),
    ])
    if (this.disposed || generation !== this.generation) {
      return false
    }
    return this.startAttempt(generation, isStillAllowed)
  }
}
