import assert from "node:assert/strict"
import test from "node:test"

import {
  CandidateGate,
  type CandidateCategory,
} from "../src/even/candidate-gate.js"

test("recognizes only the approved wake-phrase families", () => {
  const cases: readonly [string, CandidateCategory][] = [
    ["Hey, Glasses, what time is it?", "assistant_request"],
    ["Glasses, what time is it?", "assistant_request"],
    ["My glasses are on the table.", "assistant_request"],
    ["Remember that Maya is my manager.", "assistant_request"],
    ["Please remember this preference.", "assistant_request"],
    ["Show that again.", "assistant_request"],
    ["Repeat that, please.", "assistant_request"],
    ["Remind me to call Mom tomorrow.", "reminder"],
    ["Don't let me forget the groceries.", "reminder"],
    ["Don’t let me forget my keys.", "reminder"],
    ["I need to go to the gym after class.", "commitment"],
    ["Need to buy groceries tomorrow.", "commitment"],
    ["I need groceries tomorrow.", "commitment"],
    ["I should call the dentist tomorrow.", "commitment"],
    ["I plan to visit Maya next week.", "intention"],
    ["I want to exercise after class.", "intention"],
    ["I prefer window seats.", "preference"],
  ]

  for (const [transcript, category] of cases) {
    const trigger = new CandidateGate().evaluate(transcript, 1_000)
    assert.equal(trigger?.category, category, transcript)
    assert.ok((trigger?.confidence ?? 0) > 0)
  }
})

test("does not trigger on ordinary statements and filler", () => {
  const gate = new CandidateGate()

  assert.equal(gate.evaluate("The blue cup is on the table.", 1_000), undefined)
  assert.equal(gate.evaluate("Um, okay.", 2_000), undefined)
  assert.equal(gate.evaluate("Seven birds flew over the river.", 3_000), undefined)
  assert.equal(gate.evaluate("I really love window seats.", 7_000), undefined)
  assert.equal(gate.evaluate("I will call the dentist tomorrow.", 8_000), undefined)
  assert.equal(gate.evaluate("The last is my favorite.", 10_000), undefined)
  assert.equal(gate.evaluate("The needle is upstairs.", 11_000), undefined)
  assert.equal(gate.evaluate("Those classes start tomorrow.", 12_000), undefined)
  assert.equal(gate.evaluate("I know what time the meeting starts.", 13_000), undefined)
  assert.equal(gate.evaluate("That was needless work.", 14_000), undefined)
})

test("recognizes bounded rough variants of the glasses wake phrase", () => {
  const cases: readonly [string, number][] = [
    ["Hey classes, what time is it?", 0.78],
    ["Hey glass, what time is it?", 0.78],
    ["Big glasses were time is in.", 0.98],
    ["Think last is what time is it?", 0.78],
  ]

  for (const [index, [transcript, confidence]] of cases.entries()) {
    const trigger = new CandidateGate().evaluate(transcript, 1_000 + index)
    assert.equal(trigger?.category, "assistant_request", transcript)
    assert.equal(trigger?.confidence, confidence, transcript)
  }
})

test("recognizes question and request shaped rough transcripts", () => {
  const cases = [
    "What time is the meeting?",
    "Could you find the nearest coffee shop?",
    "Please help me with this.",
    "Do you remember that movie?",
    "Tell me the weather forecast.",
    "Look up the train schedule.",
  ]

  for (const [index, transcript] of cases.entries()) {
    const trigger = new CandidateGate().evaluate(transcript, 1_000 + index)
    assert.equal(trigger?.category, "assistant_request", transcript)
    assert.equal(trigger?.confidence, 0.68, transcript)
  }
})

test("suppresses an equivalent transcript during the cooldown", () => {
  const gate = new CandidateGate()
  const phrase = "I need to call the dentist tomorrow."

  assert.equal(gate.evaluate(phrase, 1_000)?.category, "commitment")
  assert.equal(gate.evaluate(phrase, 2_000), undefined)
  assert.equal(gate.evaluate(phrase, 5_001)?.category, "commitment")
})

test("allows progressive partials so an active window can be extended", () => {
  const gate = new CandidateGate()

  assert.equal(gate.evaluate("I need to", 1_000)?.category, "commitment")
  assert.equal(
    gate.evaluate("I need to call the dentist tomorrow", 1_100)?.category,
    "commitment",
  )
})

test("reset removes duplicate history between capture runs", () => {
  const gate = new CandidateGate()
  const phrase = "Hey Glasses, show me directions home."

  assert.equal(gate.evaluate(phrase, 1_000)?.category, "assistant_request")
  assert.equal(gate.evaluate(phrase, 1_100), undefined)
  gate.reset()
  assert.equal(gate.evaluate(phrase, 1_200)?.category, "assistant_request")
})
