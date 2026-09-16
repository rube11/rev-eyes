# Behavior-preserving simplification

## Starting point

- Source: remote `testingMoonshineAgain`, fetched and fast-forward checked.
- Actual head: `7d36e80c5b47969d0754a3f4e71411f6a1847c72`.
- Work branch: `refactor/simplify-moonshine-codebase`.
- Go 1.22.2; Node 24.13.0; pnpm 10.28.1; Linux x86_64.
- CGo enabled, GCC available. Moonshine v0.1.5 libraries and verified model
  prepared locally in `/tmp/rev-eyes-moonshine` using the existing setup script.
- Live-provider credentials are unverified for this run. The repository requires
  the `aws-secrets-manager` skill before credential work; that skill is absent
  from the available catalog and local skill/plugin directories. No credential
  files or deployed service environments were read.
- Paid Deepgram, live OpenAI/Tavily, and live database checks are not configured
  for this run. Native tests run locally; physical glasses testing is unavailable.

## Baseline before code changes

Passed: `go test -race ./...`, `go vet ./...`, the explicit static/manual build,
and `go test -race -tags moonshine ./...` with the prepared model and libraries.
The opt-in native/Deepgram round trips were skipped, as was speech-fixture
inference without `MOONSHINE_TEST_WAV`.

Passed: `pnpm test`, `pnpm lint`, `pnpm build`, `pnpm build:beta`,
`pnpm build:diagnostics`, `pnpm pack:beta`, and `pnpm pack:diagnostics`.
Local baseline logs use `/tmp/rev-eyes-simplify-baseline-*`.

The recorded stochastic response suite remains **not green**: the prior branch
run passed 28/43 cases versus 29/43 on its pre-streaming baseline. Those are
historical results, not new runs. Prompts, models, and phrase assertions are not
being changed to improve these numbers.

## Audio flow and ownership

The authenticated glasses connection starts ambient capture by default and sends
copied PCM frames in socket order. One native listener consumes 250 ms blocks and
retains 30 seconds of audio. A rough keyword starts one paid conversation with up
to 10 seconds of pre-roll; the trigger block appears once, followed by live audio.
Moonshine pauses while this connection is active. Accurate transcription must
authorize the automatic conversation before assistant execution. Manual taps
already authorize it. Follow-ups reuse the same Deepgram connection.

The server's 30-second idle timer measures transcript activity and turn completion;
an active assistant turn has its separate 60-second limit. Finalization flushes a
partial block without closing Deepgram. Conversation stop joins paid work and
returns to Moonshine; sleep/disconnect cancel the entire audio session. The
application memory worker has its own lifetime and survives socket disconnect.
Diagnostics shares native and paid capacity but cannot reach assistant handlers.

## Existing behavior requiring a separate product decision

- In streaming automatic mode, an unmatched accurate utterance is discarded and
  the same connection waits for a later approved utterance or idle expiry. It
  does not immediately close on the first mismatch.
- Streaming conversations use the ordinary turn delivery, which announces
  thinking before routing. The diagnostic/clip candidate path suppresses that
  announcement. Making ignored streaming turns completely silent would change
  current UI behavior.

These behaviors are preserved during this refactor.
