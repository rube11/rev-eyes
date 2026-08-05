export type PcmSampleRange = {
  startSampleOffset: number
  endSampleOffset: number
}

export type PcmWindow = PcmSampleRange & {
  pcm: Uint8Array
}

export class PcmRingBuffer {
  readonly capacitySamples: number
  readonly bytesPerSample: number
  readonly capacityBytes: number

  private readonly storage: Uint8Array
  private writeByteOffset = 0
  private retainedSampleCount = 0
  private nextSampleOffset = 0

  constructor(capacitySamples: number, bytesPerSample: number) {
    if (!Number.isSafeInteger(capacitySamples) || capacitySamples <= 0) {
      throw new RangeError("PCM ring capacity must be a positive integer")
    }
    if (!Number.isSafeInteger(bytesPerSample) || bytesPerSample <= 0) {
      throw new RangeError("PCM bytes per sample must be a positive integer")
    }
    const capacityBytes = capacitySamples * bytesPerSample
    if (!Number.isSafeInteger(capacityBytes)) {
      throw new RangeError("PCM ring byte capacity exceeds the safe integer range")
    }

    this.capacitySamples = capacitySamples
    this.bytesPerSample = bytesPerSample
    this.capacityBytes = capacityBytes
    this.storage = new Uint8Array(capacityBytes)
  }

  get retainedSamples(): number {
    return this.retainedSampleCount
  }

  get startSampleOffset(): number {
    return this.nextSampleOffset - this.retainedSampleCount
  }

  get endSampleOffset(): number {
    return this.nextSampleOffset
  }

  write(pcm: Uint8Array): PcmSampleRange {
    if (pcm.byteLength % this.bytesPerSample !== 0) {
      throw new RangeError(
        `PCM byte length must be divisible by ${this.bytesPerSample}`,
      )
    }

    const inputSamples = pcm.byteLength / this.bytesPerSample
    const range = {
      startSampleOffset: this.nextSampleOffset,
      endSampleOffset: this.nextSampleOffset + inputSamples,
    }
    this.nextSampleOffset = range.endSampleOffset
    if (inputSamples === 0) {
      return range
    }

    if (inputSamples >= this.capacitySamples) {
      this.storage.set(pcm.subarray(pcm.byteLength - this.capacityBytes))
      this.writeByteOffset = 0
      this.retainedSampleCount = this.capacitySamples
      return range
    }

    const firstByteCount = Math.min(
      pcm.byteLength,
      this.capacityBytes - this.writeByteOffset,
    )
    this.storage.set(pcm.subarray(0, firstByteCount), this.writeByteOffset)
    if (firstByteCount < pcm.byteLength) {
      this.storage.set(pcm.subarray(firstByteCount), 0)
    }
    this.writeByteOffset =
      (this.writeByteOffset + pcm.byteLength) % this.capacityBytes
    this.retainedSampleCount = Math.min(
      this.capacitySamples,
      this.retainedSampleCount + inputSamples,
    )
    return range
  }

  slice(startSampleOffset: number, endSampleOffset: number): PcmWindow {
    this.validateSampleOffset("start", startSampleOffset)
    this.validateSampleOffset("end", endSampleOffset)
    if (endSampleOffset < startSampleOffset) {
      throw new RangeError("PCM window end must not precede its start")
    }
    if (
      startSampleOffset < this.startSampleOffset ||
      endSampleOffset > this.endSampleOffset
    ) {
      throw new RangeError(
        `PCM window [${startSampleOffset}, ${endSampleOffset}) is outside ` +
          `retained range [${this.startSampleOffset}, ${this.endSampleOffset})`,
      )
    }

    const output = new Uint8Array(
      (endSampleOffset - startSampleOffset) * this.bytesPerSample,
    )
    if (output.byteLength === 0) {
      return { startSampleOffset, endSampleOffset, pcm: output }
    }

    const retainedStartByteOffset =
      (this.writeByteOffset -
        this.retainedSampleCount * this.bytesPerSample +
        this.capacityBytes) %
      this.capacityBytes
    const requestedByteOffset =
      (retainedStartByteOffset +
        (startSampleOffset - this.startSampleOffset) * this.bytesPerSample) %
      this.capacityBytes
    const firstByteCount = Math.min(
      output.byteLength,
      this.capacityBytes - requestedByteOffset,
    )
    output.set(
      this.storage.subarray(
        requestedByteOffset,
        requestedByteOffset + firstByteCount,
      ),
    )
    if (firstByteCount < output.byteLength) {
      output.set(
        this.storage.subarray(0, output.byteLength - firstByteCount),
        firstByteCount,
      )
    }
    return { startSampleOffset, endSampleOffset, pcm: output }
  }

  latest(maxSamples: number): PcmWindow {
    this.validateSampleOffset("maximum sample count", maxSamples)
    const sampleCount = Math.min(maxSamples, this.retainedSampleCount)
    return this.slice(this.nextSampleOffset - sampleCount, this.nextSampleOffset)
  }

  clear(): void {
    this.storage.fill(0)
    this.writeByteOffset = 0
    this.retainedSampleCount = 0
  }

  private validateSampleOffset(name: string, value: number): void {
    if (!Number.isSafeInteger(value) || value < 0) {
      throw new RangeError(`PCM ${name} must be a non-negative integer`)
    }
  }
}
