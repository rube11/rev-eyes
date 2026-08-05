import type { Transcriber } from "@moonshine-ai/moonshine-js"

import type {
  CandidateFinalizedEvent,
  CandidateTriggerEvent,
} from "./candidate-audio-window"
import { G2_PCM_BYTES_PER_SECOND } from "./g2-audio-format"
import type { G2PcmMediaStream } from "./g2-pcm-media-stream"
import type { MoonshineDiagnosticEvent } from "./moonshine-diagnostic-event"
import { transcriberAudioContext } from "./moonshine-runtime"

export type MoonshineRunDiagnostics = {
  id: number
  startedAt: number
  pcmFrames: number
  pcmBytes: number
  vadFrames: number
  speechStarts: number
  speechEnds: number
  commits: number
  ringStartSampleOffset: number
  ringEndSampleOffset: number
  ringRetainedSamples: number
  gateTriggers: number
  candidates: number
  candidateBytes: number
  candidateSubmitted: boolean
}

type PreparedMoonshine = {
  audio: G2PcmMediaStream
  transcriber: Transcriber
}

export class MoonshineDiagnostics {
  private readonly enabled: boolean
  private readonly onEvent:
    | ((event: MoonshineDiagnosticEvent) => void)
    | undefined

  constructor(
    enabled: boolean,
    onEvent?: (event: MoonshineDiagnosticEvent) => void,
  ) {
    this.enabled = enabled
    this.onEvent = onEvent
  }

  transcript(
    kind: "partial" | "committed",
    text: string,
    running: boolean,
  ): void {
    if (this.enabled && running && text.trim().length > 0) {
      console.debug(`[Moonshine shadow] ${kind}:`, text)
      this.publish({ event: "transcript", kind, text })
    }
  }

  lifecycle(
    event: string,
    run: MoonshineRunDiagnostics | undefined,
    prepared: PreparedMoonshine | undefined,
  ): void {
    if (!this.enabled) {
      return
    }
    const prefix = run ? `run ${run.id} ${event}` : event
    if (!prepared) {
      console.info(`[Moonshine shadow] ${prefix}`)
      this.publish({ event: "lifecycle", name: event })
      return
    }
    console.info(
      `[Moonshine shadow] ${prefix}; ${this.contextSummary(prepared)}`,
    )
    this.publish({ event: "lifecycle", name: event })
  }

  runSummary(
    run: MoonshineRunDiagnostics | undefined,
    event: string,
    prepared: PreparedMoonshine,
  ): void {
    if (!this.enabled || !run) {
      return
    }
    const wallMilliseconds = Date.now() - run.startedAt
    const pcmMilliseconds = Math.round(
      (run.pcmBytes / G2_PCM_BYTES_PER_SECOND) * 1_000,
    )
    console.info(
      `[Moonshine shadow] run ${run.id} ${event}; ` +
        `pcm_frames=${run.pcmFrames} pcm_bytes=${run.pcmBytes} ` +
        `pcm_audio_ms=${pcmMilliseconds} wall_ms=${wallMilliseconds} ` +
        `vad_frames=${run.vadFrames} speech_starts=${run.speechStarts} ` +
        `speech_ends=${run.speechEnds} commits=${run.commits} ` +
        `ring_samples=${run.ringRetainedSamples} ` +
        `ring_range=[${run.ringStartSampleOffset},${run.ringEndSampleOffset}) ` +
        `gate_triggers=${run.gateTriggers} candidates=${run.candidates} ` +
        `candidate_bytes=${run.candidateBytes} ` +
        `candidate_submitted=${run.candidateSubmitted}; ` +
        this.contextSummary(prepared),
    )
  }

  candidateTrigger(event: CandidateTriggerEvent): void {
    if (!this.enabled) {
      return
    }
    console.info(
      `[Moonshine candidate] trigger category=${event.trigger.category} ` +
        `confidence=${event.trigger.confidence.toFixed(2)} ` +
        `sample=${event.sampleOffset}`,
    )
    this.publish({
      event: "candidate_trigger",
      category: event.trigger.category,
      confidence: event.trigger.confidence,
      sample_offset: event.sampleOffset,
    })
  }

  candidateFinalized(event: CandidateFinalizedEvent): void {
    if (!this.enabled) {
      return
    }
    console.info(
      `[Moonshine candidate] finalized reason=${event.reason} ` +
        `category=${event.category} bytes=${event.byteLength} ` +
        `range=[${event.startSampleOffset},${event.endSampleOffset}) ` +
        `submitted=${event.submitted}`,
    )
    this.publish({
      event: "candidate_finalized",
      reason: event.reason,
      category: event.category,
      byte_length: event.byteLength,
      start_sample_offset: event.startSampleOffset,
      end_sample_offset: event.endSampleOffset,
      submitted: event.submitted,
    })
  }

  private publish(event: MoonshineDiagnosticEvent): void {
    try {
      this.onEvent?.(event)
    } catch {
      // Diagnostics must never affect local transcription or candidate timing.
    }
  }

  private contextSummary(prepared: PreparedMoonshine): string {
    const context = transcriberAudioContext(prepared.transcriber)
    return (
      `adapter_context=${prepared.audio.contextState()} ` +
      `moonshine_context=${context?.state ?? "unavailable"} ` +
      `adapter_track=${prepared.audio.trackState()} ` +
      `transcriber_active=${prepared.transcriber.isActive}`
    )
  }
}
