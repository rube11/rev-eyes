import assert from "node:assert/strict"
import test from "node:test"

import {
  CandidateAudioWindow,
  type CandidateAudio,
} from "../src/even/candidate-audio-window.js"

const SAMPLE_RATE = 16_000
const BYTES_PER_SAMPLE = 2

function pcm(sampleCount: number, value = 1): Uint8Array {
  return new Uint8Array(sampleCount * BYTES_PER_SAMPLE).fill(value)
}

test("emits multiple bounded candidates during one continuous run", () => {
  const received: Array<{
    candidate: Omit<CandidateAudio, "pcm">
    pcm: Uint8Array
    ownedPcm: Uint8Array
  }> = []
  const window = new CandidateAudioWindow({
    enabled: true,
    onCandidate: (candidate) => {
      const { pcm: ownedPcm, ...metadata } = candidate
      received.push({
        candidate: metadata,
        pcm: ownedPcm.slice(),
        ownedPcm,
      })
      return true
    },
  })
  window.startRun()

  window.push(pcm(SAMPLE_RATE))
  window.observeTranscript("I need to call the dentist tomorrow.")
  window.markEndpoint()
  window.push(pcm(SAMPLE_RATE * 2, 2))

  window.push(pcm(SAMPLE_RATE, 3))
  window.observeTranscript("Remind me to buy groceries tomorrow.")
  window.markEndpoint()
  window.push(pcm(SAMPLE_RATE * 2, 4))

  assert.equal(received.length, 2)
  assert.equal(received[0].candidate.category, "commitment")
  assert.equal(received[1].candidate.category, "reminder")
  assert.ok(
    received[1].candidate.startSampleOffset >=
      received[0].candidate.endSampleOffset - SAMPLE_RATE,
  )
  assert.ok(received.every(({ pcm: audio }) => audio.some((value) => value !== 0)))
  assert.ok(
    received.every(({ ownedPcm }) => ownedPcm.every((value) => value === 0)),
    "candidate-owned PCM should be cleared after the upload callback",
  )
})

test("forced candidates cover short replies that do not pass the gate", () => {
  let transcriptBytes = 0
  let category = ""
  const window = new CandidateAudioWindow({
    enabled: true,
    onCandidate: (candidate) => {
      transcriptBytes = candidate.pcm.byteLength
      category = candidate.category
      return true
    },
  })
  window.startRun()
  window.push(pcm(SAMPLE_RATE))

  assert.equal(window.armForcedCandidate(), true)
  window.push(pcm(SAMPLE_RATE / 2))
  assert.equal(window.finalizePending("manual_stop"), true)
  assert.equal(category, "manual")
  assert.equal(transcriptBytes, SAMPLE_RATE * 1.5 * BYTES_PER_SAMPLE)
})

test("a tap upgrades a gate-created window to a manual candidate", () => {
  let category = ""
  let candidates = 0
  const window = new CandidateAudioWindow({
    enabled: true,
    onCandidate: (candidate) => {
      candidates += 1
      category = candidate.category
      return true
    },
  })
  window.startRun()
  window.push(pcm(SAMPLE_RATE))
  window.observeTranscript("I need to buy groceries tomorrow.")
  window.markEndpoint()

  assert.equal(window.armForcedCandidate(), true)
  window.push(pcm(SAMPLE_RATE * 3))
  assert.equal(candidates, 0, "the old speech endpoint must be cleared")
  assert.equal(window.finalizePending("manual_stop"), true)
  assert.equal(category, "manual")
})

test("new speech extends a pending candidate beyond its old endpoint", () => {
  let candidates = 0
  const window = new CandidateAudioWindow({
    enabled: true,
    onCandidate: () => {
      candidates += 1
      return true
    },
  })
  window.startRun()
  window.push(pcm(SAMPLE_RATE))
  window.observeTranscript("I need to call the dentist tomorrow.")
  window.markEndpoint()
  window.push(pcm(SAMPLE_RATE))

  window.markSpeechStart()
  window.push(pcm(SAMPLE_RATE * 2))
  assert.equal(candidates, 0)

  window.markEndpoint()
  window.push(pcm(SAMPLE_RATE * 2))
  assert.equal(candidates, 1)
})

test("upload callback failures are contained and PCM is still cleared", () => {
  let callbackAudio: Uint8Array | undefined
  let reportedError: unknown
  const expectedError = new Error("socket failed")
  const window = new CandidateAudioWindow({
    enabled: true,
    onCandidate: (candidate) => {
      callbackAudio = candidate.pcm
      throw expectedError
    },
    onCandidateError: (error) => {
      reportedError = error
    },
  })
  window.startRun()
  window.armForcedCandidate()
  window.push(pcm(10))

  assert.equal(window.finalizePending("test"), false)
  assert.equal(reportedError, expectedError)
  assert.ok(callbackAudio?.every((value) => value === 0))
})

test("a rejected upload expires instead of being retried in later pre-roll", () => {
  const ranges: Array<[number, number]> = []
  let attempts = 0
  const window = new CandidateAudioWindow({
    enabled: true,
    onCandidate: (candidate) => {
      attempts += 1
      ranges.push([candidate.startSampleOffset, candidate.endSampleOffset])
      return attempts > 1
    },
  })
  window.startRun()

  window.armForcedCandidate()
  window.push(pcm(SAMPLE_RATE * 2))
  assert.equal(window.finalizePending("dropped"), false)

  window.armForcedCandidate()
  window.push(pcm(SAMPLE_RATE))
  assert.equal(window.finalizePending("sent"), true)

  assert.deepEqual(ranges[0], [0, SAMPLE_RATE * 2])
  assert.equal(ranges[1][0], ranges[0][1] - SAMPLE_RATE)
})

test("disabled windows do not retain PCM", () => {
  const window = new CandidateAudioWindow({ enabled: false })
  window.startRun()
  window.push(pcm(SAMPLE_RATE))

  assert.deepEqual(window.snapshot(), {
    startSampleOffset: 0,
    endSampleOffset: 0,
    retainedSamples: 0,
  })
})
