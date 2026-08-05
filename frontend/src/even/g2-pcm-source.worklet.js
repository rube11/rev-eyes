class G2PcmSourceProcessor extends AudioWorkletProcessor {
  constructor(options) {
    super()
    this.chunks = []
    this.chunkOffset = 0
    this.bufferedSamples = 0
    this.maxBufferedSamples = Math.max(
      128,
      options.processorOptions?.maxBufferedSamples ?? 32_000,
    )

    this.port.onmessage = (event) => {
      if (event.data?.type === "clear") {
        this.clear()
        return
      }
      if (event.data?.type === "samples" && event.data.samples instanceof Float32Array) {
        this.enqueue(event.data.samples)
      }
    }
  }

  clear() {
    for (const chunk of this.chunks) {
      chunk.fill(0)
    }
    this.chunks = []
    this.chunkOffset = 0
    this.bufferedSamples = 0
  }

  enqueue(samples) {
    if (samples.length >= this.maxBufferedSamples) {
      this.clear()
      const retainedStart = samples.length - this.maxBufferedSamples
      samples.fill(0, 0, retainedStart)
      this.chunks = [samples.subarray(retainedStart)]
      this.chunkOffset = 0
      this.bufferedSamples = this.maxBufferedSamples
      return
    }

    this.chunks.push(samples)
    this.bufferedSamples += samples.length
    this.discardOldest(this.bufferedSamples - this.maxBufferedSamples)
  }

  discardOldest(sampleCount) {
    let remaining = Math.max(0, sampleCount)
    while (remaining > 0 && this.chunks.length > 0) {
      const available = this.chunks[0].length - this.chunkOffset
      const discarded = Math.min(available, remaining)
      this.chunks[0].fill(
        0,
        this.chunkOffset,
        this.chunkOffset + discarded,
      )
      this.chunkOffset += discarded
      this.bufferedSamples -= discarded
      remaining -= discarded
      if (this.chunkOffset === this.chunks[0].length) {
        this.chunks.shift()
        this.chunkOffset = 0
      }
    }
  }

  process(_inputs, outputs) {
    const output = outputs[0]?.[0]
    if (!output) {
      return true
    }

    output.fill(0)
    let outputOffset = 0
    while (outputOffset < output.length && this.chunks.length > 0) {
      const chunk = this.chunks[0]
      const available = chunk.length - this.chunkOffset
      const copied = Math.min(available, output.length - outputOffset)
      output.set(
        chunk.subarray(this.chunkOffset, this.chunkOffset + copied),
        outputOffset,
      )
      chunk.fill(0, this.chunkOffset, this.chunkOffset + copied)
      this.chunkOffset += copied
      this.bufferedSamples -= copied
      outputOffset += copied
      if (this.chunkOffset === chunk.length) {
        this.chunks.shift()
        this.chunkOffset = 0
      }
    }

    return true
  }
}

registerProcessor("rev-eyes-g2-pcm-source", G2PcmSourceProcessor)
