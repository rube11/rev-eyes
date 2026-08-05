import {
  AudioCaptureController,
  type AudioCaptureState,
  type AudioDeviceControls,
} from "./audio.js"

export type LocalCaptureControls = {
  cancel(): void
  dispose(): void
  start(): Promise<boolean>
  stop(): void
}

type AudioCaptureSessionOptions = {
  device: AudioDeviceControls
  local?: LocalCaptureControls
  localRequired: boolean
}

function describeError(error: unknown): string {
  return error instanceof Error ? error.message : String(error)
}

// AudioCaptureSession coordinates the hardware and local-transcriber edges.
// It does not own PCM, candidate windows, transport, or presentation state.
export class AudioCaptureSession {
  private readonly capture: AudioCaptureController
  private readonly local: LocalCaptureControls | undefined
  private readonly localRequired: boolean
  private lifecycleGeneration = 0

  constructor(options: AudioCaptureSessionOptions) {
    this.capture = new AudioCaptureController(options.device)
    this.local = options.local
    this.localRequired = options.localRequired
  }

  get state(): AudioCaptureState {
    return this.capture.state
  }

  get running(): boolean {
    return this.capture.running
  }

  async start(
    isStillAllowed: () => boolean = () => true,
  ): Promise<boolean> {
    const generation = ++this.lifecycleGeneration
    const deviceStarted = await this.capture.start(isStillAllowed)
    if (!deviceStarted || generation !== this.lifecycleGeneration) {
      return false
    }

    if (!this.localRequired) {
      void this.local?.start().catch((error: unknown) => {
        console.warn(
          `[Moonshine shadow] could not start without affecting legacy capture: ${describeError(error)}`,
        )
      })
      return true
    }

    const localStarted = this.local
      ? await this.local.start().catch((error: unknown) => {
          console.warn(
            `[Moonshine shadow] local capture failed to start: ${describeError(error)}`,
          )
          return false
        })
      : false
    if (
      generation !== this.lifecycleGeneration ||
      !this.capture.running
    ) {
      return false
    }
    if (!localStarted || !this.allowed(isStillAllowed)) {
      await this.stopFailedStart()
      return false
    }
    return true
  }

  async stop(finalizeLocal = true): Promise<boolean> {
    this.lifecycleGeneration += 1
    try {
      if (finalizeLocal) {
        this.local?.stop()
      } else {
        this.local?.cancel()
      }
    } catch (error) {
      console.warn(
        `[Moonshine shadow] local capture could not stop cleanly: ${describeError(error)}`,
      )
    }
    return this.stopDevice("stop")
  }

  dispose(): void {
    this.lifecycleGeneration += 1
    try {
      this.local?.dispose()
    } catch (error) {
      console.warn(
        `[Moonshine shadow] local capture disposal failed: ${describeError(error)}`,
      )
    }
    void this.capture.dispose().then((stopped) => {
      if (!stopped) {
        this.reportUnconfirmedStop("disposal")
      }
    })
  }

  private allowed(isStillAllowed: () => boolean): boolean {
    try {
      return isStillAllowed()
    } catch {
      return false
    }
  }

  private async stopFailedStart(): Promise<void> {
    this.lifecycleGeneration += 1
    try {
      this.local?.cancel()
    } catch {
      // The device still must be stopped if local cancellation fails.
    }
    await this.stopDevice("failed start")
  }

  private async stopDevice(reason: string): Promise<boolean> {
    const stopped = await this.capture.stop()
    if (!stopped) {
      this.reportUnconfirmedStop(reason)
    }
    return stopped
  }

  private reportUnconfirmedStop(reason: string): void {
    console.warn(
      `[Candidate audio] glasses microphone stop was not confirmed (${reason})`,
    )
  }
}
