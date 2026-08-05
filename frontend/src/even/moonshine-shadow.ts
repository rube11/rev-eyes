import type { Transcriber } from "@moonshine-ai/moonshine-js"

import {
  CandidateAudioWindow,
  type CandidateAudio,
  type CandidateFinalizedEvent,
  type CandidateTriggerEvent,
} from "./candidate-audio-window"
import { G2PcmMediaStream } from "./g2-pcm-media-stream"
import {
  MoonshineDiagnostics,
  type MoonshineRunDiagnostics,
} from "./moonshine-diagnostics"
import type { MoonshineDiagnosticEvent } from "./moonshine-diagnostic-event"
import {
  createMoonshineInferenceLifecycle,
  disposeMoonshineTranscriber,
  transcriberAudioContext,
  type MoonshineInferenceLifecycle,
} from "./moonshine-runtime"
import { withTimeout } from "./promise-timeout"

const VAD_STOP_DRAIN_MS = 1_500
const START_TIMEOUT_MS = 5_000

export type { CandidateAudio } from "./candidate-audio-window"

type PreparedMoonshine = {
  audio: G2PcmMediaStream
  inference: MoonshineInferenceLifecycle
  transcriber: Transcriber
}

type MoonshineShadowOptions = {
  candidateAudioEnabled?: boolean
  debugTranscripts: boolean
  onCandidate?: (candidate: CandidateAudio) => boolean
  onCandidateFinalized?: (event: CandidateFinalizedEvent) => void
  onDiagnostic?: (event: MoonshineDiagnosticEvent) => void
  onRunComplete?: (result: MoonshineRunResult) => void
  onVoiceReplyStarted?: () => void
}

export type MoonshineRunResult = {
  candidateSubmitted: boolean
}

function describeError(error: unknown): string {
  return error instanceof Error ? error.message : String(error)
}

export class MoonshineShadowTranscriber {
  private readonly diagnostics: MoonshineDiagnostics
  private readonly onCandidateFinalized:
    | ((event: CandidateFinalizedEvent) => void)
    | undefined
  private readonly onRunComplete: ((result: MoonshineRunResult) => void) | undefined
  private readonly onVoiceReplyStarted: (() => void) | undefined
  private readonly candidates: CandidateAudioWindow
  private prepared: PreparedMoonshine | undefined
  private preparation: Promise<void> | undefined
  private activation: Promise<boolean> | undefined
  private shouldRun = false
  private running = false
  private speechActive = false
  private voiceReplyArmed = false
  private disabled = false
  private disposed = false
  private runGeneration = 0
  private nextRunID = 0
  private runDiagnostics: MoonshineRunDiagnostics | undefined
  private stopTimer: ReturnType<typeof setTimeout> | undefined

  constructor(options: MoonshineShadowOptions) {
    this.diagnostics = new MoonshineDiagnostics(
      options.debugTranscripts,
      options.onDiagnostic,
    )
    this.onCandidateFinalized = options.onCandidateFinalized
    this.onRunComplete = options.onRunComplete
    this.onVoiceReplyStarted = options.onVoiceReplyStarted
    this.candidates = new CandidateAudioWindow({
      enabled: options.candidateAudioEnabled === true,
      onCandidate: options.onCandidate,
      onCandidateError: (error) => {
        console.warn(
          `[Moonshine candidate] upload callback failed: ${describeError(error)}`,
        )
      },
      onFinalized: (event) => this.recordCandidateFinalized(event),
      onTrigger: (event) => this.recordCandidateTrigger(event),
    })
  }

  isReady(): boolean {
    return !this.disabled && !this.disposed && this.prepared !== undefined
  }

  prepare(): Promise<void> {
    if (this.disabled || this.disposed) {
      return Promise.resolve()
    }
    this.preparation ??= this.initialize().catch((error: unknown) => {
      this.disabled = true
      console.warn(
        `[Moonshine shadow] unavailable: ${describeError(error)}`,
      )
    })
    return this.preparation
  }

  async start(): Promise<boolean> {
    if (this.disabled || this.disposed) {
      return false
    }
    if (this.stopTimer !== undefined) {
      clearTimeout(this.stopTimer)
      this.stopTimer = undefined
      this.finishStop("stopped before restart")
    }
    this.shouldRun = true
    await this.prepare()
    if (
      this.disabled ||
      this.disposed ||
      !this.shouldRun ||
      !this.prepared
    ) {
      return false
    }
    if (this.running) {
      return this.activation ?? true
    }
    return this.activate()
  }

  stop(): void {
    const wasRequested = this.shouldRun
    this.shouldRun = false
    this.voiceReplyArmed = false
    if (this.stopTimer !== undefined) {
      this.logLifecycle("already draining")
      return
    }
    this.runGeneration += 1
    if (!this.prepared || !this.running) {
      this.logLifecycle("stop ignored: not running")
      if (wasRequested) {
        this.captureAndClearPcm(undefined)
        this.notifyRunComplete(undefined)
      }
      return
    }
    this.logLifecycle("draining")
    this.stopTimer = setTimeout(() => {
      this.stopTimer = undefined
      this.finishStop("stopped")
    }, VAD_STOP_DRAIN_MS)
  }

  cancel(): void {
    if (this.disposed) {
      return
    }
    const hadRun = this.shouldRun || this.running
    this.shouldRun = false
    this.speechActive = false
    this.voiceReplyArmed = false
    this.runGeneration += 1
    if (this.stopTimer !== undefined) {
      clearTimeout(this.stopTimer)
      this.stopTimer = undefined
    }
    const run = this.runDiagnostics
    this.runDiagnostics = undefined
    this.captureAndClearPcm(run)
    const prepared = this.prepared
    prepared?.inference.endSession()
    if (!prepared || !this.running) {
      this.running = false
      if (hadRun) {
        this.notifyRunComplete(undefined)
      }
      return
    }
    this.running = false
    void this.stopPrepared(prepared).then(() => {
      this.logRunSummary(run, "cancelled", prepared)
      this.notifyRunComplete(undefined)
    })
  }

  push(pcm: Uint8Array): void {
    if (!this.shouldRun || !this.running || !this.prepared) {
      return
    }
    try {
      this.candidates.push(pcm)
      if (this.runDiagnostics) {
        this.runDiagnostics.pcmFrames += 1
        this.runDiagnostics.pcmBytes += pcm.byteLength
      }
      this.prepared.audio.push(pcm)
    } catch (error) {
      console.warn(
        `[Moonshine shadow] rejected a PCM frame: ${describeError(error)}`,
      )
    }
  }

  armForcedCandidate(): boolean {
    if (
      !this.shouldRun ||
      !this.running ||
      !this.runDiagnostics
    ) {
      return false
    }
    const armed = this.candidates.armForcedCandidate()
    if (armed) {
      this.logLifecycle("forced candidate armed")
    }
    return armed
  }

  setVoiceReplyArmed(armed: boolean): boolean {
    this.voiceReplyArmed =
      armed &&
      this.shouldRun &&
      this.running &&
      !this.disabled &&
      !this.disposed
    return this.voiceReplyArmed
  }

  finalizePendingCandidate(): boolean {
    return this.candidates.finalizePending("manual_stop")
  }

  discardPendingCandidate(): void {
    this.candidates.discardPending()
  }

  dispose(): void {
    if (this.disposed) {
      return
    }
    this.disposed = true
    this.shouldRun = false
    this.speechActive = false
    this.voiceReplyArmed = false
    this.runGeneration += 1
    if (this.stopTimer !== undefined) {
      clearTimeout(this.stopTimer)
      this.stopTimer = undefined
    }
    const prepared = this.prepared
    this.prepared = undefined
    prepared?.inference.endSession()
    const run = this.runDiagnostics
    this.runDiagnostics = undefined
    this.captureAndClearPcm(run)
    if (!prepared) {
      return
    }
    const wasRunning = this.running
    this.running = false
    void Promise.all([
      disposeMoonshineTranscriber(prepared.transcriber),
      prepared.audio.dispose(),
    ]).then(() => {
      if (wasRunning) {
        this.logRunSummary(run, "disposed", prepared)
      }
    })
  }

  private async initialize(): Promise<void> {
    let audio: G2PcmMediaStream | undefined
    let transcriber: Transcriber | undefined
    try {
      const moonshineModulePromise = import("@moonshine-ai/moonshine-js")
      audio = await G2PcmMediaStream.create()
      const { Transcriber } = await moonshineModulePromise
      transcriber = new Transcriber(
        "model/tiny",
        {
          onPermissionsRequested: () => undefined,
          onError: (error) => {
            console.warn(
              `[Moonshine shadow] transcriber error: ${describeError(error)}`,
            )
          },
          onModelLoadStarted: () => {
            console.info("[Moonshine shadow] loading local models")
          },
          onModelLoaded: () => undefined,
          onTranscribeStarted: () => {
            this.logLifecycle("transcriber started")
          },
          onTranscribeStopped: () => {
            this.logLifecycle("transcriber stopped")
          },
          onTranscriptionUpdated: (text) => {
            const transcript = text.trim()
            if (!transcript) {
              return
            }
            if (this.running) {
              this.candidates.observeTranscript(transcript)
            }
            this.logTranscript("partial", transcript)
          },
          onTranscriptionCommitted: (text) => {
            const transcript = text.trim()
            if (!transcript) {
              return
            }
            if (this.runDiagnostics) {
              this.runDiagnostics.commits += 1
            }
            if (this.running) {
              this.candidates.observeTranscript(transcript)
              if (!this.speechActive) {
                this.candidates.markEndpoint()
              }
            }
            this.logTranscript("committed", transcript)
          },
          onFrame: () => {
            if (this.running && this.runDiagnostics) {
              this.runDiagnostics.vadFrames += 1
            }
          },
          onSpeechStart: () => {
            if (!this.running) {
              return
            }
            this.speechActive = true
            if (
              this.voiceReplyArmed &&
              this.candidates.armForcedCandidate()
            ) {
              this.voiceReplyArmed = false
              this.logLifecycle("voice reply started")
              try {
                this.onVoiceReplyStarted?.()
              } catch {
                // Presentation bookkeeping must not interrupt VAD or PCM.
              }
            }
            this.candidates.markSpeechStart()
            if (this.runDiagnostics) {
              this.runDiagnostics.speechStarts += 1
            }
            this.logLifecycle("speech started")
          },
          onSpeechEnd: () => {
            if (!this.running) {
              return
            }
            this.speechActive = false
            if (this.runDiagnostics) {
              this.runDiagnostics.speechEnds += 1
            }
            this.candidates.markEndpoint()
            this.logLifecycle("speech ended")
          },
        },
        true,
      )
      const inference = createMoonshineInferenceLifecycle(transcriber)
      transcriber.attachStream(audio.stream)
      await transcriber.load()

      if (this.disposed) {
        await Promise.all([
          disposeMoonshineTranscriber(transcriber),
          audio.dispose(),
        ])
        return
      }

      this.prepared = { audio, inference, transcriber }
      console.info("[Moonshine shadow] ready")
    } catch (error) {
      await Promise.all([
        transcriber
          ? disposeMoonshineTranscriber(transcriber)
          : Promise.resolve(),
        audio ? audio.dispose() : Promise.resolve(),
      ])
      throw error
    }
  }

  private activate(): Promise<boolean> {
    if (
      !this.prepared ||
      this.running ||
      !this.shouldRun ||
      this.disposed
    ) {
      return Promise.resolve(false)
    }

    const prepared = this.prepared
    prepared.inference.beginSession()
    this.running = true
    this.speechActive = false
    this.candidates.startRun()
    const generation = ++this.runGeneration
    const buffer = this.candidates.snapshot()
    const run: MoonshineRunDiagnostics = {
      id: ++this.nextRunID,
      startedAt: Date.now(),
      pcmFrames: 0,
      pcmBytes: 0,
      vadFrames: 0,
      speechStarts: 0,
      speechEnds: 0,
      commits: 0,
      ringStartSampleOffset: buffer.startSampleOffset,
      ringEndSampleOffset: buffer.endSampleOffset,
      ringRetainedSamples: buffer.retainedSamples,
      gateTriggers: 0,
      candidates: 0,
      candidateBytes: 0,
      candidateSubmitted: false,
    }
    this.runDiagnostics = run
    this.logLifecycle("starting")
    const attempt = this.activatePrepared(prepared, generation, run)
    const tracked = attempt.finally(() => {
      if (this.activation === tracked) {
        this.activation = undefined
      }
    })
    this.activation = tracked
    return tracked
  }

  private async activatePrepared(
    prepared: PreparedMoonshine,
    generation: number,
    run: MoonshineRunDiagnostics,
  ): Promise<boolean> {
    try {
      // Confirm the adapter is running before VAD begins consuming its stream.
      await withTimeout(
        prepared.audio.resume(),
        START_TIMEOUT_MS,
        "G2 PCM audio context did not start in time",
      )
      if (!this.isCurrentRun(generation, run)) {
        return false
      }
      await withTimeout(
        prepared.transcriber.start(),
        START_TIMEOUT_MS,
        "Moonshine transcriber did not start in time",
      )
      if (!this.isCurrentRun(generation, run)) {
        if (!this.running) {
          prepared.transcriber.stop()
        }
        return false
      }
      const context = transcriberAudioContext(prepared.transcriber)
      if (!context) {
        throw new Error("Moonshine audio context is unavailable")
      }
      if (context.state !== "running") {
        await withTimeout(
          context.resume(),
          START_TIMEOUT_MS,
          "Moonshine audio context did not start in time",
        )
      }
      if (!this.isCurrentRun(generation, run)) {
        return false
      }
      if (context.state !== "running" || !prepared.transcriber.isActive) {
        throw new Error(
          `Moonshine did not become active (context=${context.state})`,
        )
      }
      this.logLifecycle("running")
      return true
    } catch (error) {
      await this.handleActivationError(generation, error)
      return false
    }
  }

  private isCurrentRun(
    generation: number,
    run: MoonshineRunDiagnostics,
  ): boolean {
    return (
      generation === this.runGeneration &&
      this.running &&
      this.shouldRun &&
      this.runDiagnostics === run
    )
  }

  private async handleActivationError(
    generation: number,
    error: unknown,
  ): Promise<void> {
    if (generation !== this.runGeneration || !this.prepared) {
      return
    }
    console.warn(
      `[Moonshine shadow] could not start: ${describeError(error)}`,
    )
    this.shouldRun = false
    this.voiceReplyArmed = false
    this.runGeneration += 1
    const run = this.runDiagnostics
    this.candidates.finalizePending("activation_failed")
    this.runDiagnostics = undefined
    this.speechActive = false
    this.captureAndClearPcm(run)
    this.running = false
    const prepared = this.prepared
    prepared.inference.endSession()
    await this.stopPrepared(prepared)
    this.logRunSummary(run, "failed", prepared)
    this.notifyRunComplete(run)
  }

  private logTranscript(kind: "partial" | "committed", text: string): void {
    this.diagnostics.transcript(kind, text, this.running)
  }

  private logLifecycle(event: string): void {
    this.diagnostics.lifecycle(event, this.runDiagnostics, this.prepared)
  }

  private logRunSummary(
    run: MoonshineRunDiagnostics | undefined,
    event: string,
    prepared: PreparedMoonshine,
  ): void {
    this.diagnostics.runSummary(run, event, prepared)
  }

  private finishStop(event: string): void {
    if (!this.prepared || !this.running) {
      return
    }
    const run = this.runDiagnostics
    this.candidates.finalizePending("run_stop")
    this.runDiagnostics = undefined
    this.speechActive = false
    this.voiceReplyArmed = false
    this.captureAndClearPcm(run)
    this.running = false
    const prepared = this.prepared
    prepared.inference.endSession()
    void this.stopPrepared(prepared).then(() => {
      this.logRunSummary(run, event, prepared)
      this.notifyRunComplete(run)
    })
  }

  private stopPrepared(prepared: PreparedMoonshine): Promise<void> {
    try {
      prepared.transcriber.stop()
    } catch (error) {
      console.warn(
        `[Moonshine shadow] could not stop cleanly: ${describeError(error)}`,
      )
    }
    try {
      // Keep the synthetic MediaStream's AudioContext alive between runs.
      // This WebView leaves resume() pending indefinitely after suspend().
      prepared.audio.clear()
      return Promise.resolve()
    } catch (error) {
      console.warn(
        `[Moonshine shadow] could not clear PCM adapter: ${describeError(error)}`,
      )
      // Never let the shadow path interfere with the G2 microphone lifecycle.
      return Promise.resolve()
    }
  }

  private captureAndClearPcm(
    run: MoonshineRunDiagnostics | undefined,
  ): void {
    const snapshot = this.candidates.clear()
    if (run) {
      run.ringStartSampleOffset = snapshot.startSampleOffset
      run.ringEndSampleOffset = snapshot.endSampleOffset
      run.ringRetainedSamples = snapshot.retainedSamples
    }
  }

  private recordCandidateTrigger(event: CandidateTriggerEvent): void {
    if (this.runDiagnostics) {
      this.runDiagnostics.gateTriggers += 1
    }
    this.diagnostics.candidateTrigger(event)
  }

  private recordCandidateFinalized(event: CandidateFinalizedEvent): void {
    if (this.runDiagnostics) {
      this.runDiagnostics.candidates += 1
      this.runDiagnostics.candidateBytes += event.byteLength
      this.runDiagnostics.candidateSubmitted ||= event.submitted
    }
    this.diagnostics.candidateFinalized(event)
    try {
      this.onCandidateFinalized?.(event)
    } catch {
      // Presentation bookkeeping must not change candidate cleanup.
    }
  }

  private notifyRunComplete(
    run: MoonshineRunDiagnostics | undefined,
  ): void {
    this.onRunComplete?.({
      candidateSubmitted: run?.candidateSubmitted === true,
    })
  }
}
