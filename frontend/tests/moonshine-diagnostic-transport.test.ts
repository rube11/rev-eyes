import assert from "node:assert/strict"
import test from "node:test"

import type { CandidateAudioSocket } from "../src/even/candidate-audio-transport.js"
import { MoonshineDiagnosticTransport } from "../src/even/moonshine-diagnostic-transport.js"

class FakeSocket implements CandidateAudioSocket {
  readyState = 1
  bufferedAmount = 0
  messages: Array<string | ArrayBuffer> = []

  send(data: string | ArrayBuffer): void {
    this.messages.push(data)
  }

  close(): void {}
}

test("sends a bounded diagnostic envelope when enabled", () => {
  const socket = new FakeSocket()
  const transport = new MoonshineDiagnosticTransport({
    enabled: true,
    getSocket: () => socket,
  })

  assert.equal(transport.send({
    event: "transcript",
    kind: "committed",
    text: "  hey glasses  ",
  }), true)
  assert.deepEqual(JSON.parse(socket.messages[0] as string), {
    type: "moonshine_diagnostic",
    diagnostic: {
      event: "transcript",
      kind: "committed",
      text: "hey glasses",
    },
  })
})

test("drops duplicate and high-frequency partial transcripts", () => {
  const socket = new FakeSocket()
  let now = 1_000
  const transport = new MoonshineDiagnosticTransport({
    enabled: true,
    getSocket: () => socket,
    now: () => now,
  })

  const partial = (text: string) => transport.send({
    event: "transcript",
    kind: "partial",
    text,
  })
  assert.equal(partial("hey"), true)
  now += 800
  assert.equal(partial("hey"), false)
  assert.equal(partial("hey glasses"), true)
  now += 100
  assert.equal(partial("hey glasses what"), false)
  assert.equal(socket.messages.length, 2)
})

test("does nothing when disabled or the socket is backpressured", () => {
  const socket = new FakeSocket()
  const disabled = new MoonshineDiagnosticTransport({
    enabled: false,
    getSocket: () => socket,
  })
  assert.equal(disabled.send({ event: "lifecycle", name: "running" }), false)

  socket.bufferedAmount = 1_000_001
  const enabled = new MoonshineDiagnosticTransport({
    enabled: true,
    getSocket: () => socket,
  })
  assert.equal(enabled.send({ event: "lifecycle", name: "running" }), false)
  assert.equal(socket.messages.length, 0)
})
