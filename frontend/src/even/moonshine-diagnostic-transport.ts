import type { CandidateAudioSocket } from "./candidate-audio-transport.js"
import type { MoonshineDiagnosticEvent } from "./moonshine-diagnostic-event.js"

const WEBSOCKET_OPEN_STATE = 1
const MAX_SOCKET_BUFFERED_BYTES = 1_000_000
const PARTIAL_TRANSCRIPT_INTERVAL_MS = 750
const MAX_TRANSCRIPT_CHARACTERS = 512

type MoonshineDiagnosticTransportOptions = {
  enabled: boolean
  getSocket: () => CandidateAudioSocket | undefined
  now?: () => number
}

// Development-only transport for observing the local gate from the Go
// terminal. It is enabled only by runtime.ts in Vite development builds.
export class MoonshineDiagnosticTransport {
  private readonly enabled: boolean
  private readonly getSocket: () => CandidateAudioSocket | undefined
  private readonly now: () => number
  private lastPartialAt = Number.NEGATIVE_INFINITY
  private lastPartialText = ""

  constructor(options: MoonshineDiagnosticTransportOptions) {
    this.enabled = options.enabled
    this.getSocket = options.getSocket
    this.now = options.now ?? Date.now
  }

  send(event: MoonshineDiagnosticEvent): boolean {
    if (!this.enabled) {
      return false
    }
    const socket = this.getSocket()
    if (
      !socket ||
      socket.readyState !== WEBSOCKET_OPEN_STATE ||
      socket.bufferedAmount > MAX_SOCKET_BUFFERED_BYTES
    ) {
      return false
    }

    let outbound = event
    let acceptedPartial: { text: string; at: number } | undefined
    if (event.event === "transcript") {
      const text = event.text.trim().slice(0, MAX_TRANSCRIPT_CHARACTERS)
      if (!text) {
        return false
      }
      if (event.kind === "partial") {
        const now = this.now()
        if (
          text === this.lastPartialText ||
          now - this.lastPartialAt < PARTIAL_TRANSCRIPT_INTERVAL_MS
        ) {
          return false
        }
        acceptedPartial = { text, at: now }
      }
      outbound = { ...event, text }
    }

    try {
      socket.send(JSON.stringify({
        type: "moonshine_diagnostic",
        diagnostic: outbound,
      }))
      if (acceptedPartial) {
        this.lastPartialText = acceptedPartial.text
        this.lastPartialAt = acceptedPartial.at
      }
      return true
    } catch {
      // Diagnostics must never close or otherwise disturb the audio socket.
      return false
    }
  }
}
