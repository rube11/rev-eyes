# Behavior-preserving simplification

This report records the refactor baseline. Before release, the owner requested
removal of the temporary Eyes Listening Test app, diagnostic endpoint, and
client audit logging. References to diagnostics below describe historical checks,
not currently shipped functionality. Native conversation tests remain.

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

## Optional future product changes

- In streaming automatic mode, an unmatched accurate utterance is discarded and
  the same connection waits for a later approved utterance or idle expiry. It
  does not immediately close on the first mismatch.
- Streaming conversations use the ordinary turn delivery, which announces
  thinking before routing. The diagnostic/clip candidate path suppresses that
  announcement. Making ignored streaming turns completely silent would change
  current UI behavior.

These behaviors are preserved during this refactor.

## Simplifications delivered

- Assistant routing always receives prepared conversation context, and agents
  explicitly return proposal effects. Removed both optional capability checks.
- Streaming transcription is a construction dependency. PCM and utterance
  finalization use an explicit `AudioInput`, replacing the nil-buffer command.
- Ambient listening owns an explicit active conversation and a fixed completion
  channel. Realtime connections track worker and capture state explicitly instead
  of using channel nilness to switch select cases.
- Main and diagnostic servers receive the same transcription capacity at
  construction. Retained-audio and paid-work permit lifetimes are unchanged.
- Workspace callbacks are immutable constructor arguments. The memory recorder
  uses its existing state lock instead of additional atomics and a callback lock.
- The tool registry executes tools directly; the separate executor is gone.
- Assistant and database-store construction no longer return errors for missing
  internal dependencies. Actual initialization and provider failures still do.
- `RealtimeConnection` owns frontend socket adoption, bindings, cancellation,
  retry timers, and generations. The existing serialized glasses transition
  queue still owns UI and microphone decisions. Removed unused answer wrappers
  and redundant presentation helper functions.

Small consumer-owned interfaces remain for persistence, native/provider access,
and isolated behavioral tests. This is not a wholesale domain rename or file
layout migration. Some older assembly constructors still return nil-dependency
errors; this pass removes that scaffolding from the assistant and database stores.

## Characterization and final validation

New coverage locks down automatic versus manual authorization, exactly one idle
notification, queued PCM clearing and worker joining, repeated persistent
Deepgram endpoints, reconnect with late old-socket events, teardown during socket
setup, and notification deferral while server capture remains active.

Existing coverage remains for replay without duplicate trigger audio, audio
continuing beyond the rolling-window limit, partial-block finalization,
connection reuse, slow assistant turns, admission expiry and cancellation,
diagnostic isolation, memory lifetime, and explicit manual frontend behavior.
Tests that used the retired non-contextual router fake now exercise the same
context-before-routing order as the production router. Live assertions and
assistant prompts were not changed.

Final checks all passed:

| Check | Result |
| --- | --- |
| Backend `go test -race ./...` | Pass |
| Backend `go vet ./...` | Pass |
| Explicit static/manual build | Pass; not deployed |
| Native `go test -race -tags moonshine ./...` | Pass |
| Native speech fixture inference | Pass with Moonshine v0.1.5 `two_cities_16k.wav` |
| Runtime installation and immutable redeployment shell tests | Pass locally |
| Frontend tests and lint | Pass |
| Frontend normal, beta, diagnostic builds | Pass |
| Beta and diagnostic Even packages | Pass |

Final local logs use `/tmp/rev-eyes-simplify-final-*`. Native tests used
`MOONSHINE_TEST_MODEL_DIR=/tmp/rev-eyes-moonshine/model`,
`MOONSHINE_TEST_WAV=/tmp/rev-eyes-simplify-two-cities.wav`, `CGO_ENABLED=1`,
and library/rpath flags for `/tmp/rev-eyes-moonshine/lib`.

Offline tests do not establish live response quality, paid-provider connectivity,
database integration, or locked-phone behavior. Those checks were not run. The
historical quality failures above remain unresolved; no new provider failure or
model-output regression can be inferred from this offline run. No AWS deployment
or changes to public message shapes, model configuration, prompts, native setup
scripts, or environment-variable meanings were made.
