import workletUrl from "./g2-pcm-source.worklet.js?url&no-inline"

import {
  G2_PCM_BYTES_PER_SAMPLE,
  G2_PCM_SAMPLE_RATE_HZ,
} from "./g2-audio-format"

const WORKLET_QUEUE_SECONDS = 2

export class G2PcmMediaStream {
  readonly stream: MediaStream

  private readonly context: AudioContext
  private readonly source: AudioWorkletNode
  private contextTransition: Promise<void> = Promise.resolve()
  private disposed = false

  private constructor(
    context: AudioContext,
    source: AudioWorkletNode,
    destination: MediaStreamAudioDestinationNode,
  ) {
    this.context = context
    this.source = source
    this.stream = destination.stream
  }

  static async create(): Promise<G2PcmMediaStream> {
    if (
      typeof AudioContext === "undefined" ||
      typeof AudioWorkletNode === "undefined"
    ) {
      throw new Error("AudioWorklet is unavailable in this WebView")
    }

    const context = new AudioContext({
      latencyHint: "interactive",
      sampleRate: G2_PCM_SAMPLE_RATE_HZ,
    })
    try {
      if (context.sampleRate !== G2_PCM_SAMPLE_RATE_HZ) {
        throw new Error(
          `WebView created a ${context.sampleRate} Hz audio context; 16000 Hz is required`,
        )
      }

      await context.audioWorklet.addModule(workletUrl)
      const source = new AudioWorkletNode(context, "rev-eyes-g2-pcm-source", {
        numberOfInputs: 0,
        numberOfOutputs: 1,
        outputChannelCount: [1],
        processorOptions: {
          // This queue only absorbs scheduling jitter. It is not the future
          // candidate-audio ring buffer.
          maxBufferedSamples: G2_PCM_SAMPLE_RATE_HZ * WORKLET_QUEUE_SECONDS,
        },
      })
      const destination = context.createMediaStreamDestination()
      source.connect(destination)
      return new G2PcmMediaStream(context, source, destination)
    } catch (error) {
      await context.close().catch(() => undefined)
      throw error
    }
  }

  push(pcm: Uint8Array): void {
    if (this.disposed || pcm.byteLength === 0) {
      return
    }
    if (pcm.byteLength % G2_PCM_BYTES_PER_SAMPLE !== 0) {
      throw new Error("G2 PCM frame has an odd byte length")
    }

    // Even SDK audio is mono, signed 16-bit little-endian PCM at 16 kHz.
    // Conversion allocates a new buffer, so the SDK frame remains untouched.
    const view = new DataView(pcm.buffer, pcm.byteOffset, pcm.byteLength)
    const samples = new Float32Array(
      pcm.byteLength / G2_PCM_BYTES_PER_SAMPLE,
    )
    for (let index = 0; index < samples.length; index += 1) {
      samples[index] =
        view.getInt16(index * G2_PCM_BYTES_PER_SAMPLE, true) / 32_768
    }
    this.source.port.postMessage(
      { type: "samples", samples },
      [samples.buffer],
    )
  }

  contextState(): string {
    return this.context.state
  }

  trackState(): string {
    const track = this.stream.getAudioTracks()[0]
    return track ? `${track.readyState}/${track.muted ? "muted" : "audible"}` : "none"
  }

  async resume(): Promise<void> {
    if (this.disposed) {
      return
    }
    await this.enqueueContextTransition(async () => {
      if (this.disposed) {
        return
      }
      if (this.context.state === "closed") {
        throw new Error("G2 PCM audio context is closed")
      }
      if (this.context.state !== "running") {
        await this.context.resume()
      }
      if (this.context.state !== "running") {
        throw new Error(
          `G2 PCM audio context did not resume (${this.context.state})`,
        )
      }
    })
  }

  clear(): void {
    if (this.disposed) {
      return
    }
    this.source.port.postMessage({ type: "clear" })
  }

  async dispose(): Promise<void> {
    if (this.disposed) {
      return
    }
    this.disposed = true
    try {
      this.source.port.postMessage({ type: "clear" })
      this.source.disconnect()
    } catch {
      // The browser may already have torn down the worklet graph.
    }
    for (const track of this.stream.getTracks()) {
      try {
        track.stop()
      } catch {
        // The browser may already have finalized the track.
      }
    }
    await this.enqueueContextTransition(async () => {
      if (this.context.state !== "closed") {
        await this.context.close()
      }
    }).catch(() => undefined)
  }

  private enqueueContextTransition(
    transition: () => Promise<void>,
  ): Promise<void> {
    const result = this.contextTransition
      .catch(() => undefined)
      .then(transition)
    this.contextTransition = result.catch(() => undefined)
    return result
  }
}
