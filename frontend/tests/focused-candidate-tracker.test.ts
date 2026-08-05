import assert from "node:assert/strict"
import test from "node:test"

import { FocusedCandidateTracker } from "../src/even/focused-candidate-tracker.js"

test("locks the first eligible candidate until it is cleared", () => {
  const tracker = new FocusedCandidateTracker()

  assert.equal(tracker.focus("ambient", false), false)
  assert.equal(tracker.active, false)
  assert.equal(tracker.focus("focused-1", true), true)
  assert.equal(tracker.focus("ambient-2", true), false)
  assert.equal(tracker.matches("focused-1"), true)
  assert.equal(tracker.competes("ambient-2"), true)

  tracker.clear()
  assert.equal(tracker.active, false)
  assert.equal(tracker.focus("focused-2", true), true)
  assert.equal(tracker.matches("focused-2"), true)
})
