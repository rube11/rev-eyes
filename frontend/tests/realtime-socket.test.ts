import assert from "node:assert/strict"
import test from "node:test"

import {
  closeUnadoptedSocket,
  reconnectDelay,
  safeSend,
  safeSendJson,
  socketIsOpen,
  type RealtimeSocket,
} from "../src/even/realtime-socket.js"

function socket(readyState = 1): RealtimeSocket & {
  closes: number
  sent: Array<string | ArrayBuffer>
} {
  return {
    readyState,
    closes: 0,
    sent: [],
    close() {
      this.closes += 1
    },
    send(data) {
      this.sent.push(data)
    },
  }
}

test("sends only through an open socket", () => {
  const open = socket()
  const closed = socket(3)

  assert.equal(socketIsOpen(open), true)
  assert.equal(safeSend(open, "audio"), true)
  assert.equal(safeSendJson(open, { type: "location" }), true)
  assert.deepEqual(open.sent, ["audio", '{"type":"location"}'])
  assert.equal(safeSend(closed, "ignored"), false)
  assert.deepEqual(closed.sent, [])
})

test("closes a newly opened socket that the runtime did not adopt", () => {
  const opened = socket()
  const current = socket()

  closeUnadoptedSocket(opened, current)
  assert.equal(opened.closes, 1)

  closeUnadoptedSocket(current, current)
  assert.equal(current.closes, 0)
})

test("bounds reconnect delay and accepts deterministic jitter", () => {
  assert.equal(reconnectDelay(0, () => 0), 375)
  assert.equal(reconnectDelay(0, () => 1), 625)
  assert.equal(reconnectDelay(100, () => 1), 10_000)
})
