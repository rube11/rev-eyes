import assert from "node:assert/strict"
import test from "node:test"

import {
  CandidateAudioTransport,
  type CandidateAudioSocket,
} from "../src/even/candidate-audio-transport.js"
import type { CandidateAudio } from "../src/even/candidate-audio-window.js"

class FakeSocket implements CandidateAudioSocket {
  readyState = 1
  bufferedAmount = 0
  messages: Array<string | ArrayBuffer> = []
  closed = false
  failAtMessage: number | undefined

  send(data: string | ArrayBuffer): void {
    if (this.messages.length === this.failAtMessage) {
      throw new Error("send failed")
    }
    this.messages.push(data)
  }

  close(): void {
    this.closed = true
  }
}

function candidate(id: string): CandidateAudio {
  return {
    id,
    encoding: "linear16",
    sampleRate: 16_000,
    channels: 1,
    startSampleOffset: 10,
    endSampleOffset: 12,
    category: "reminder",
    confidence: 0.98,
    pcm: new Uint8Array([1, 2, 3, 4]),
  }
}

test("sends one metadata frame followed by an owned binary copy", () => {
  const socket = new FakeSocket()
  const sent: string[] = []
  const transport = new CandidateAudioTransport({
    debug: false,
    getSocket: () => socket,
    onCandidateSent: (id) => sent.push(id),
  })
  const input = candidate("candidate-1")

  assert.equal(transport.send(input), true)
  assert.equal(socket.messages.length, 2)
  assert.deepEqual(JSON.parse(socket.messages[0] as string), {
    type: "candidate_audio",
    id: "candidate-1",
    encoding: "linear16",
    sample_rate: 16_000,
    channels: 1,
    byte_length: 4,
    start_sample_offset: 10,
    end_sample_offset: 12,
    gate_category: "reminder",
    gate_confidence: 0.98,
  })
  assert.deepEqual(
    new Uint8Array(socket.messages[1] as ArrayBuffer),
    input.pcm,
  )
  assert.notEqual(socket.messages[1], input.pcm.buffer)
  assert.deepEqual(sent, ["candidate-1"])
  transport.reset()
})

test("bounds candidate uploads until completion arrives", () => {
  const socket = new FakeSocket()
  const transport = new CandidateAudioTransport({
    debug: false,
    getSocket: () => socket,
  })

  assert.equal(transport.send(candidate("candidate-1")), true)
  assert.equal(transport.send(candidate("candidate-2")), true)
  assert.equal(transport.send(candidate("candidate-3")), false)
  transport.complete("candidate-1")
  assert.equal(transport.send(candidate("candidate-3")), true)
  assert.equal(transport.hasInFlightCandidates(), true)
  transport.reset()
  assert.equal(transport.hasInFlightCandidates(), false)
})

test("rejects backpressured sockets without writing either frame", () => {
  const socket = new FakeSocket()
  socket.bufferedAmount = 2_000_000
  const transport = new CandidateAudioTransport({
    debug: false,
    getSocket: () => socket,
  })

  assert.equal(transport.send(candidate("candidate-1")), false)
  assert.equal(socket.messages.length, 0)
})

test("closes a socket if the binary half of an upload fails", () => {
  const socket = new FakeSocket()
  socket.failAtMessage = 1
  const transport = new CandidateAudioTransport({
    debug: false,
    getSocket: () => socket,
  })

  assert.equal(transport.send(candidate("candidate-1")), false)
  assert.equal(socket.closed, true)
  assert.equal(transport.hasInFlightCandidates(), false)
})

test("closes the candidate socket when a response never arrives", async () => {
  const socket = new FakeSocket()
  const transport = new CandidateAudioTransport({
    candidateTimeoutMilliseconds: 5,
    debug: false,
    getSocket: () => socket,
  })

  assert.equal(transport.send(candidate("candidate-1")), true)
  await new Promise((resolve) => setTimeout(resolve, 10))
  assert.equal(socket.closed, true)
  assert.equal(transport.hasInFlightCandidates(), false)
})

test("completion cancels the candidate response deadline", async () => {
  const socket = new FakeSocket()
  const transport = new CandidateAudioTransport({
    candidateTimeoutMilliseconds: 5,
    debug: false,
    getSocket: () => socket,
  })

  assert.equal(transport.send(candidate("candidate-1")), true)
  transport.complete("candidate-1")
  await new Promise((resolve) => setTimeout(resolve, 10))
  assert.equal(socket.closed, false)
  assert.equal(transport.hasInFlightCandidates(), false)
})
