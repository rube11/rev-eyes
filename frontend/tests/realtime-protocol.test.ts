import assert from "node:assert/strict"
import test from "node:test"

import { parseRealtimeServerMessage } from "../src/even/realtime-protocol.js"

test("parses response metadata and filters workspace resources", () => {
  assert.deepEqual(
    parseRealtimeServerMessage(JSON.stringify({
      type: "assistant_response",
      id: "candidate-1",
      text: "Saved.",
      awaiting_confirmation: true,
      resources: ["tasks", "invalid", "tasks", "memories"],
    })),
    {
      type: "assistant_response",
      id: "candidate-1",
      text: "Saved.",
      error: undefined,
      awaitingConfirmation: true,
      resources: ["tasks", "memories"],
    },
  )
})

test("rejects non-text, malformed, and typeless messages", () => {
  assert.equal(parseRealtimeServerMessage(new ArrayBuffer(0)), undefined)
  assert.equal(parseRealtimeServerMessage("not json"), undefined)
  assert.equal(parseRealtimeServerMessage(JSON.stringify({ text: "missing type" })), undefined)
})
