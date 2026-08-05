import type { CandidateAudio } from "./candidate-audio-window.js"

const WEBSOCKET_OPEN_STATE = 1
const MAX_CANDIDATES_IN_FLIGHT = 2
const MAX_SOCKET_BUFFERED_BYTES = 2 * 1_000_000
const CANDIDATE_RESPONSE_TIMEOUT_MS = 75_000

export type CandidateAudioSocket = {
  readonly readyState: number
  readonly bufferedAmount: number
  send(data: string | ArrayBuffer): void
  close(): void
}

type CandidateAudioTransportOptions = {
  candidateTimeoutMilliseconds?: number
  debug: boolean
  getSocket: () => CandidateAudioSocket | undefined
  onCandidateSent?: (candidateID: string) => void
}

type InFlightCandidate = {
  socket: CandidateAudioSocket
  timeout: ReturnType<typeof setTimeout>
}

export class CandidateAudioTransport {
  private readonly debug: boolean
  private readonly getSocket: () => CandidateAudioSocket | undefined
  private readonly onCandidateSent: ((candidateID: string) => void) | undefined
  private readonly candidateTimeoutMilliseconds: number
  private readonly inFlightCandidates = new Map<string, InFlightCandidate>()

  constructor(options: CandidateAudioTransportOptions) {
    this.debug = options.debug
    this.getSocket = options.getSocket
    this.onCandidateSent = options.onCandidateSent
    this.candidateTimeoutMilliseconds =
      options.candidateTimeoutMilliseconds ?? CANDIDATE_RESPONSE_TIMEOUT_MS
  }

  hasInFlightCandidates(): boolean {
    return this.inFlightCandidates.size > 0
  }

  complete(candidateID: string | undefined): void {
    if (candidateID) {
      this.clearInFlightCandidate(candidateID)
    }
  }

  reset(): void {
    for (const candidateID of this.inFlightCandidates.keys()) {
      this.clearInFlightCandidate(candidateID)
    }
  }

  send(candidate: CandidateAudio): boolean {
    const socket = this.getSocket()
    if (!socket || socket.readyState !== WEBSOCKET_OPEN_STATE) {
      return false
    }
    if (
      this.inFlightCandidates.size >= MAX_CANDIDATES_IN_FLIGHT ||
      socket.bufferedAmount + candidate.pcm.byteLength >
        MAX_SOCKET_BUFFERED_BYTES
    ) {
      return false
    }

    try {
      socket.send(JSON.stringify({
        type: "candidate_audio",
        id: candidate.id,
        encoding: candidate.encoding,
        sample_rate: candidate.sampleRate,
        channels: candidate.channels,
        byte_length: candidate.pcm.byteLength,
        start_sample_offset: candidate.startSampleOffset,
        end_sample_offset: candidate.endSampleOffset,
        gate_category: candidate.category,
        gate_confidence: candidate.confidence,
      }))
      socket.send(candidate.pcm.slice().buffer)
    } catch {
      try {
        socket.close()
      } catch {
        // The browser may already have finalized the socket.
      }
      return false
    }

    const timeout = setTimeout(() => {
      const inFlight = this.inFlightCandidates.get(candidate.id)
      if (!inFlight) {
        return
      }
      this.inFlightCandidates.delete(candidate.id)
      if (this.debug) {
        console.warn(
          `[Candidate audio] timed out waiting for id=${candidate.id}`,
        )
      }
      try {
        inFlight.socket.close()
      } catch {
        // The browser may already have finalized the socket.
      }
    }, this.candidateTimeoutMilliseconds)
    this.inFlightCandidates.set(candidate.id, { socket, timeout })
    try {
      this.onCandidateSent?.(candidate.id)
    } catch {
      // Presentation bookkeeping must not change upload ownership.
    }
    if (this.debug) {
      console.info(
        `[Candidate audio] sent id=${candidate.id} ` +
          `bytes=${candidate.pcm.byteLength} ` +
          `range=[${candidate.startSampleOffset},${candidate.endSampleOffset})`,
      )
    }
    return true
  }

  private clearInFlightCandidate(candidateID: string): void {
    const inFlight = this.inFlightCandidates.get(candidateID)
    if (!inFlight) {
      return
    }
    clearTimeout(inFlight.timeout)
    this.inFlightCandidates.delete(candidateID)
  }
}
