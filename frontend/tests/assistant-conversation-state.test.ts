import assert from "node:assert/strict"
import test from "node:test"

import { AssistantConversationState } from "../src/even/assistant-conversation-state.js"

test("starts exactly one reply from an armed response window", () => {
  const state = new AssistantConversationState()

  state.openResponseWindow(true)
  assert.equal(state.voiceReplyAvailable, true)
  assert.equal(state.beginReply(), true)
  assert.equal(state.voiceReplyAvailable, false)
  assert.equal(state.replyActive, true)
  assert.equal(state.beginReply(), false)
})

test("closing a response window does not terminate a reply already in progress", () => {
  const state = new AssistantConversationState()

  state.openResponseWindow(true)
  assert.equal(state.beginReply(), true)
  state.closeResponseWindow()
  assert.equal(state.replyActive, true)

  state.finishReply()
  assert.equal(state.replyActive, false)
})

test("cannot start a voice reply from an unavailable or reset window", () => {
  const state = new AssistantConversationState()

  state.openResponseWindow(false)
  assert.equal(state.beginReply(), false)
  state.openResponseWindow(true)
  state.reset()
  assert.equal(state.voiceReplyAvailable, false)
  assert.equal(state.beginReply(), false)
})
