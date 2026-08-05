import assert from "node:assert/strict"
import test from "node:test"

import { PcmRingBuffer } from "../src/even/pcm-ring-buffer.js"

function pcmBytes(buffer: PcmRingBuffer): Uint8Array {
  return buffer.slice(buffer.startSampleOffset, buffer.endSampleOffset).pcm
}

test("retains copied PCM bytes with monotonic sample offsets", () => {
  const buffer = new PcmRingBuffer(5, 2)
  const input = Uint8Array.from([1, 2, 3, 4])

  assert.deepEqual(buffer.write(input), {
    startSampleOffset: 0,
    endSampleOffset: 2,
  })
  input.fill(9)

  assert.equal(buffer.startSampleOffset, 0)
  assert.equal(buffer.endSampleOffset, 2)
  assert.equal(buffer.retainedSamples, 2)
  assert.deepEqual(pcmBytes(buffer), Uint8Array.from([1, 2, 3, 4]))
})

test("returns chronological windows after wrapping", () => {
  const buffer = new PcmRingBuffer(5, 1)
  buffer.write(Uint8Array.from([10, 11, 12]))
  buffer.write(Uint8Array.from([13, 14, 15, 16]))

  assert.equal(buffer.startSampleOffset, 2)
  assert.equal(buffer.endSampleOffset, 7)
  assert.equal(buffer.retainedSamples, 5)
  assert.deepEqual(pcmBytes(buffer), Uint8Array.from([12, 13, 14, 15, 16]))

  const window = buffer.slice(3, 6)
  assert.deepEqual(window, {
    startSampleOffset: 3,
    endSampleOffset: 6,
    pcm: Uint8Array.from([13, 14, 15]),
  })

  window.pcm.fill(99)
  assert.deepEqual(buffer.slice(3, 6).pcm, Uint8Array.from([13, 14, 15]))
})

test("keeps only the newest samples from writes larger than capacity", () => {
  const buffer = new PcmRingBuffer(4, 1)

  assert.deepEqual(buffer.write(Uint8Array.from([1, 2, 3, 4, 5, 6])), {
    startSampleOffset: 0,
    endSampleOffset: 6,
  })
  assert.equal(buffer.startSampleOffset, 2)
  assert.deepEqual(pcmBytes(buffer), Uint8Array.from([3, 4, 5, 6]))

  buffer.write(Uint8Array.from([7, 8]))
  assert.equal(buffer.startSampleOffset, 4)
  assert.equal(buffer.endSampleOffset, 8)
  assert.deepEqual(pcmBytes(buffer), Uint8Array.from([5, 6, 7, 8]))
})

test("returns the newest requested window and clamps to retained audio", () => {
  const buffer = new PcmRingBuffer(5, 1)
  buffer.write(Uint8Array.from([1, 2, 3]))

  assert.deepEqual(buffer.latest(2), {
    startSampleOffset: 1,
    endSampleOffset: 3,
    pcm: Uint8Array.from([2, 3]),
  })
  assert.deepEqual(buffer.latest(10), {
    startSampleOffset: 0,
    endSampleOffset: 3,
    pcm: Uint8Array.from([1, 2, 3]),
  })
  assert.deepEqual(buffer.latest(0), {
    startSampleOffset: 3,
    endSampleOffset: 3,
    pcm: new Uint8Array(),
  })
})

test("clear overwrites retained storage and preserves the sample timeline", () => {
  const buffer = new PcmRingBuffer(4, 1)
  buffer.write(Uint8Array.from([1, 2, 3]))

  buffer.clear()

  assert.equal(buffer.retainedSamples, 0)
  assert.equal(buffer.startSampleOffset, 3)
  assert.equal(buffer.endSampleOffset, 3)
  const internal = buffer as unknown as { storage: Uint8Array }
  assert.deepEqual(internal.storage, new Uint8Array(4))

  assert.deepEqual(buffer.write(Uint8Array.from([4, 5])), {
    startSampleOffset: 3,
    endSampleOffset: 5,
  })
  assert.deepEqual(buffer.slice(3, 5).pcm, Uint8Array.from([4, 5]))
})

test("rejects invalid capacities, frames, counts, and ranges", () => {
  assert.throws(() => new PcmRingBuffer(0, 1), RangeError)
  assert.throws(() => new PcmRingBuffer(1.5, 1), RangeError)
  assert.throws(() => new PcmRingBuffer(1, 0), RangeError)

  const buffer = new PcmRingBuffer(4, 2)
  assert.throws(() => buffer.write(Uint8Array.from([1])), RangeError)
  buffer.write(Uint8Array.from([1, 2, 3, 4]))

  assert.throws(() => buffer.slice(-1, 1), RangeError)
  assert.throws(() => buffer.slice(0.5, 1), RangeError)
  assert.throws(() => buffer.slice(2, 1), RangeError)
  assert.throws(() => buffer.slice(0, 3), RangeError)
  assert.throws(() => buffer.latest(-1), RangeError)
  assert.throws(() => buffer.latest(1.5), RangeError)
})
