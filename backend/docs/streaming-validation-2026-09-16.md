# Streaming conversation validation — 2026-09-16

Implementation: `02329c0`. Baseline: `a06702e` (before streaming changes).

## Audio and interaction checks

- Full offline backend suite passed with the race detector; `go vet ./...` passed.
- Frontend tests, lint, and Beta packaging passed: 57 core tests, 29 runtime/config
  tests, and 13 authentication tests.
- Audio regression checks verify exact sample ordering across buffered replay and
  live audio, continued capture beyond the old 30-second clip limit, finalization
  of partial frames, cancellation, and release of paid-stream admission.
- On the AWS host, the native Moonshine/real Deepgram smoke test handled
  “Glasses. Remind me to buy milk tomorrow.” and “What about tomorrow?” on exactly
  **one** Deepgram connection, then closed it on inactivity. The test uses a
  five-second idle interval; production uses 30 seconds. It passed twice, including
  after final cleanup changes. Its assistant handler is a stub with no actions or
  saved user data.
- Main-app tests exercise follow-up display and server-owned timeout behavior.
  A real glasses test of the complete assistant conversation is still required.
- Eyes Listening Test 0.1.6 remains the separate transcript/clip diagnostic app.
  Rev Eyes 0.1.6 contains the new persistent conversation flow.

## Memory and response evaluation

The live tests ran with the deployment service's existing provider configuration.
Model inputs were synthetic. No real user memories were written.

| Check | Result |
| --- | --- |
| Live memory capture | Pass |
| PostgreSQL profile behavior, ownership, and expiration (rolled-back synthetic transaction) | Pass |
| Live response/router/profile suite on changed tree | 28/43 cases passed; 15 failed |
| Same suite on pre-change baseline | 29/43 cases passed; 14 failed |

Twelve failing cases overlapped. Three failed only in the initial changed-tree run;
two failed only in the initial baseline run. Repeating those five cases twice on
the baseline reproduced varying verdicts for web verification, Priya preparation,
and grocery advice. The store-list case passed both repeats; it originally failed
because its answer omitted the literal word “protein” despite suggesting chicken
and eggs.

Some assertions require exact phrases: “rice cooker” versus “start rice in the
cooker,” or “Jordan dislikes mushrooms” versus “Avoid ordering mushrooms for
Jordan—he dislikes them.” Other failures deserve behavioral review: routing a
question to memory review/task proposal, or skipping the expected web lookup.

The assistant, memory, session, tool, and dependency source trees are identical
between the baseline and changed tree. These text-based live evaluations do not
exercise the changed audio path. No response-quality regression was traced to the
streaming change, but **the live quality suite is not green**. These results are
not a claim that all response behavior is correct or that stochastic model output
is statistically unchanged.

## Reproduction

Offline: `go test -race ./...` and `go vet ./...` from `backend`; `pnpm test`,
`pnpm lint`, and `pnpm pack:beta` from `frontend`.

Live response tests: enable `RUN_LIVE_ASSISTANT_TEST=1`,
`RUN_PROFILE_MODEL_TEST=1`, and `RUN_LIVE_WEB_RESEARCH_EVAL=1`, then run
`go test -v ./internal/assistant/openai -run 'TestLive|TestProfile.*Live'` with the
normal provider environment. Memory capture uses `RUN_LIVE_MEMORY_EVAL=1` and
`go test -v . -run '^TestLiveMemoryCapture$'`.

See [server listening setup](server-moonshine.md) for the opt-in native streaming
smoke test and synthetic PCM requirements. None of the live provider tests run by
default.
