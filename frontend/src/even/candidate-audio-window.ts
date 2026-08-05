import {
  G2_PCM_BYTES_PER_SAMPLE,
  G2_PCM_CHANNEL_COUNT,
  G2_PCM_SAMPLE_RATE_HZ,
} from "./g2-audio-format.js"
import {
  CandidateGate,
  type CandidateCategory,
  type CandidateTrigger,
} from "./candidate-gate.js"
import { PcmRingBuffer } from "./pcm-ring-buffer.js"

const RING_RETENTION_SECONDS = 30
const PRE_ROLL_SECONDS = 10
const POST_ROLL_SECONDS = 2
const CONTINUITY_OVERLAP_SECONDS = 1

export type CandidateAudio = {
  id: string
  encoding: "linear16"
  sampleRate: number
  channels: number
  startSampleOffset: number
  endSampleOffset: number
  category: CandidateCategory
  confidence: number
  pcm: Uint8Array
}

export type CandidateTriggerEvent = {
  trigger: CandidateTrigger
  sampleOffset: number
}

export type CandidateFinalizedEvent = {
  reason: string
  category: CandidateCategory
  byteLength: number
  startSampleOffset: number
  endSampleOffset: number
  submitted: boolean
}

export type CandidateBufferSnapshot = {
  startSampleOffset: number
  endSampleOffset: number
  retainedSamples: number
}

type PendingCandidate = {
  startSampleOffset: number
  finishAfterSampleOffset: number | undefined
  trigger: CandidateTrigger
}

type CandidateAudioWindowOptions = {
  enabled: boolean
  onCandidate?: (candidate: CandidateAudio) => boolean
  onCandidateError?: (error: unknown) => void
  onFinalized?: (event: CandidateFinalizedEvent) => void
  onTrigger?: (event: CandidateTriggerEvent) => void
}

export class CandidateAudioWindow {
  private readonly enabled: boolean
  private readonly onCandidate: ((candidate: CandidateAudio) => boolean) | undefined
  private readonly onCandidateError: ((error: unknown) => void) | undefined
  private readonly onFinalized:
    | ((event: CandidateFinalizedEvent) => void)
    | undefined
  private readonly onTrigger:
    | ((event: CandidateTriggerEvent) => void)
    | undefined
  private readonly gate = new CandidateGate()
  private readonly pcm = new PcmRingBuffer(
    G2_PCM_SAMPLE_RATE_HZ * RING_RETENTION_SECONDS,
    G2_PCM_BYTES_PER_SAMPLE,
  )
  private nextCandidateID = 0
  private lastFinalizedEndSampleOffset = 0
  private pending: PendingCandidate | undefined

  constructor(options: CandidateAudioWindowOptions) {
    this.enabled = options.enabled
    this.onCandidate = options.onCandidate
    this.onCandidateError = options.onCandidateError
    this.onFinalized = options.onFinalized
    this.onTrigger = options.onTrigger
  }

  startRun(): void {
    this.gate.reset()
    this.pending = undefined
  }

  push(frame: Uint8Array): void {
    if (!this.enabled) {
      return
    }
    this.pcm.write(frame)
    this.maybeFinalize()
  }

  observeTranscript(text: string): void {
    if (!this.enabled) {
      return
    }
    const trigger = this.gate.evaluate(text)
    if (!trigger) {
      return
    }

    const triggerSampleOffset = this.pcm.endSampleOffset
    if (this.pending) {
      this.pending.finishAfterSampleOffset = undefined
      if (trigger.confidence > this.pending.trigger.confidence) {
        this.pending.trigger = trigger
      }
    } else {
      const preRollSamples = G2_PCM_SAMPLE_RATE_HZ * PRE_ROLL_SECONDS
      const overlapSamples =
        G2_PCM_SAMPLE_RATE_HZ * CONTINUITY_OVERLAP_SECONDS
      this.pending = {
        startSampleOffset: Math.max(
          this.pcm.startSampleOffset,
          triggerSampleOffset - preRollSamples,
          this.lastFinalizedEndSampleOffset - overlapSamples,
        ),
        finishAfterSampleOffset: undefined,
        trigger,
      }
    }
    try {
      this.onTrigger?.({ trigger, sampleOffset: triggerSampleOffset })
    } catch {
      // Diagnostics must not interrupt PCM ownership or window timing.
    }
    this.maybeFinalize()
  }

  markEndpoint(): void {
    if (!this.pending) {
      return
    }
    this.pending.finishAfterSampleOffset =
      this.pcm.endSampleOffset + G2_PCM_SAMPLE_RATE_HZ * POST_ROLL_SECONDS
  }

  markSpeechStart(): void {
    if (this.pending) {
      this.pending.finishAfterSampleOffset = undefined
    }
  }

  armForcedCandidate(): boolean {
    if (!this.enabled) {
      return false
    }
    if (this.pending) {
      // An explicit reply mode (tap-to-talk or a temporary conversation
      // window) must bypass the ambient wake policy. Upgrade any gate-created
      // window and let the explicit mode decide when it ends.
      this.pending.trigger = { category: "manual", confidence: 1 }
      this.pending.finishAfterSampleOffset = undefined
      return true
    }

    const triggerSampleOffset = this.pcm.endSampleOffset
    const overlapSamples =
      G2_PCM_SAMPLE_RATE_HZ * CONTINUITY_OVERLAP_SECONDS
    this.pending = {
      startSampleOffset: Math.max(
        this.pcm.startSampleOffset,
        triggerSampleOffset - overlapSamples,
        this.lastFinalizedEndSampleOffset - overlapSamples,
      ),
      finishAfterSampleOffset: undefined,
      trigger: { category: "manual", confidence: 1 },
    }
    return true
  }

  finalizePending(reason: string): boolean {
    const pending = this.pending
    this.pending = undefined
    if (!pending) {
      return false
    }

    const endSampleOffset = this.pcm.endSampleOffset
    const startSampleOffset = Math.max(
      pending.startSampleOffset,
      this.pcm.startSampleOffset,
      endSampleOffset - G2_PCM_SAMPLE_RATE_HZ * RING_RETENTION_SECONDS,
    )
    if (endSampleOffset <= startSampleOffset) {
      return false
    }

    const window = this.pcm.slice(startSampleOffset, endSampleOffset)
    const candidate: CandidateAudio = {
      id: `candidate-${Date.now().toString(36)}-${(++this.nextCandidateID).toString(36)}`,
      encoding: "linear16",
      sampleRate: G2_PCM_SAMPLE_RATE_HZ,
      channels: G2_PCM_CHANNEL_COUNT,
      startSampleOffset,
      endSampleOffset,
      category: pending.trigger.category,
      confidence: pending.trigger.confidence,
      pcm: window.pcm,
    }
    let submitted = false
    try {
      submitted = this.onCandidate?.(candidate) === true
    } catch (error) {
      try {
        this.onCandidateError?.(error)
      } catch {
        // Error reporting must not change raw-audio cleanup.
      }
    } finally {
      candidate.pcm.fill(0)
    }
    // A failed upload expires with the same overlap policy as a successful one;
    // later candidates must not silently retry the discarded raw audio.
    this.lastFinalizedEndSampleOffset = endSampleOffset
    try {
      this.onFinalized?.({
        reason,
        category: candidate.category,
        byteLength: candidate.pcm.byteLength,
        startSampleOffset,
        endSampleOffset,
        submitted,
      })
    } catch {
      // Diagnostics must not change candidate delivery semantics.
    }
    return submitted
  }

  discardPending(): void {
    this.pending = undefined
  }

  clear(): CandidateBufferSnapshot {
    const snapshot = this.snapshot()
    this.pending = undefined
    this.pcm.clear()
    return snapshot
  }

  snapshot(): CandidateBufferSnapshot {
    return {
      startSampleOffset: this.pcm.startSampleOffset,
      endSampleOffset: this.pcm.endSampleOffset,
      retainedSamples: this.pcm.retainedSamples,
    }
  }

  private maybeFinalize(): void {
    if (!this.pending) {
      return
    }
    const hardLimit =
      this.pending.startSampleOffset +
      G2_PCM_SAMPLE_RATE_HZ * RING_RETENTION_SECONDS
    const finishAfter = this.pending.finishAfterSampleOffset
    if (
      this.pcm.endSampleOffset >= hardLimit ||
      (finishAfter !== undefined && this.pcm.endSampleOffset >= finishAfter)
    ) {
      this.finalizePending("endpoint")
    }
  }
}
