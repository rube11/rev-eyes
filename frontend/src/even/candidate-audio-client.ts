import {
  MoonshineShadowTranscriber,
  type MoonshineRunResult,
} from "./moonshine-shadow"
import {
  CandidateAudioTransport,
  type CandidateAudioSocket,
} from "./candidate-audio-transport"
import { AudioCaptureSession } from "./audio-capture-session"
import type { AudioCaptureState, AudioDeviceControls } from "./audio"
import { MoonshineDiagnosticTransport } from "./moonshine-diagnostic-transport"
import type { CandidateFinalizedEvent } from "./candidate-audio-window"

export type { MoonshineRunResult } from "./moonshine-shadow"
export type { CandidateFinalizedEvent } from "./candidate-audio-window"

type CandidateAudioClientOptions = {
  candidateAudioEnabled: boolean
  debugTranscripts: boolean
  device: AudioDeviceControls
  getSocket: () => CandidateAudioSocket | undefined
  moonshineEnabled: boolean
  forwardDiagnostics: boolean
  onCandidateFinalized?: (event: CandidateFinalizedEvent) => void
  onCandidateSent?: (candidateID: string) => void
  onRunComplete?: (result: MoonshineRunResult) => void
  onVoiceReplyStarted?: () => void
}

// CandidateAudioClient owns device capture, local transcription, and the
// two-frame candidate upload protocol. Glasses presentation remains in runtime.ts.
export class CandidateAudioClient {
  private readonly capture: AudioCaptureSession
  private readonly transport: CandidateAudioTransport
  private readonly transcriber: MoonshineShadowTranscriber | undefined

  constructor(options: CandidateAudioClientOptions) {
    this.transport = new CandidateAudioTransport({
      debug: options.debugTranscripts,
      getSocket: options.getSocket,
      onCandidateSent: options.onCandidateSent,
    })
    const diagnostics = new MoonshineDiagnosticTransport({
      enabled: options.forwardDiagnostics,
      getSocket: options.getSocket,
    })
    this.transcriber = options.moonshineEnabled
      ? new MoonshineShadowTranscriber({
          candidateAudioEnabled: options.candidateAudioEnabled,
          debugTranscripts: options.debugTranscripts,
          onCandidate: (candidate) => this.transport.send(candidate),
          onCandidateFinalized: options.onCandidateFinalized,
          onDiagnostic: (event) => diagnostics.send(event),
          onRunComplete: options.onRunComplete,
          onVoiceReplyStarted: options.onVoiceReplyStarted,
        })
      : undefined
    this.capture = new AudioCaptureSession({
      device: options.device,
      local: this.transcriber,
      localRequired: options.candidateAudioEnabled,
    })
  }

  async prepare(): Promise<boolean> {
    await this.transcriber?.prepare()
    return this.isReady()
  }

  isReady(): boolean {
    return this.transcriber?.isReady() === true
  }

  hasInFlightCandidates(): boolean {
    return this.transport.hasInFlightCandidates()
  }

  get captureState(): AudioCaptureState {
    return this.capture.state
  }

  get captureRunning(): boolean {
    return this.capture.running
  }

  async startCapture(
    isStillAllowed: () => boolean = () => true,
  ): Promise<boolean> {
    return this.capture.start(isStillAllowed)
  }

  async stopCapture(finalizeCandidate = true): Promise<boolean> {
    return this.capture.stop(finalizeCandidate)
  }

  complete(candidateID: string | undefined): void {
    this.transport.complete(candidateID)
  }

  resetTransport(): void {
    this.transport.reset()
  }

  push(pcm: Uint8Array): void {
    this.transcriber?.push(pcm)
  }

  armForcedCandidate(): boolean {
    return this.transcriber?.armForcedCandidate() === true
  }

  setVoiceReplyArmed(armed: boolean): boolean {
    return this.transcriber?.setVoiceReplyArmed(armed) === true
  }

  finalizePendingCandidate(): boolean {
    return this.transcriber?.finalizePendingCandidate() === true
  }

  discardPendingCandidate(): void {
    this.transcriber?.discardPendingCandidate()
  }

  dispose(): void {
    this.capture.dispose()
  }
}
