# Server-side Moonshine listening

The default path forwards G2 PCM to Go during ambient listening. Native Moonshine
recognizes speech on the server; the phrase policy opens a persistent Deepgram
Nova-3 connection for an active conversation. No Python runtime or worker is used.
Manual Deepgram mode remains available when the server-listening flag is off.

## Runtime and audio behavior

- Go calls the Moonshine C API through CGo. The pinned Linux x86_64 release is
  v0.1.5; its header and MIT license are vendored in `internal/stt/moonshine`.
- A shared model serves independently allocated streams. Native calls, including
  copying borrowed transcripts, are serialized. Default admission is one listening
  session; excess sessions fail explicitly, without paid-streaming fallback.
- PCM is 16 kHz mono signed little-endian 16-bit. Each session keeps 30 seconds
  (960 KB) of PCM and evaluates recognition every 250 ms of received audio.
- A keyword in a partial Moonshine transcript opens Deepgram immediately. The
  buffered audio includes up to 10 seconds before the detected line, followed by
  continuous live audio. The buffer's 30-second limit does not truncate ongoing
  speech. Moonshine pauses inference during the active Deepgram conversation.
- Streams are recreated every 60 seconds with five seconds of audio replay to
  bound native transcript/audio history. Sample offsets suppress duplicate wakes.
  This should be evaluated against real long-form speech at stream boundaries.
- A manual tap opens a conversation without a keyword. Tap-to-finish flushes
  remaining PCM and asks Deepgram to finalize the utterance while keeping the
  connection open. Tapping a displayed answer dismisses the conversation.
- The same Deepgram connection handles subsequent replies without a keyword.
  A pause ends the turn through either Deepgram's acoustic endpoint or its
  finalized-word gap event, so steady ambient noise cannot leave the glasses
  stuck on the listening screen.
  The server closes it after 30 seconds without transcript activity or an
  assistant completion; an in-flight assistant turn has its own 60-second limit.
  Phone timers do not own this deadline. Idle returns to native keyword listening.
- Streaming conversations share paid-transcription admission with diagnostic
  clips. Automatic wakes require an accurate wake phrase before the first
  assistant turn; later turns use the normal utterance handler. Rough Moonshine
  text stays out of history, logs, and glasses transcripts.
- WebSocket disconnect or explicit app sleep cancels native listening, active
  conversations and queued audio. Reconnect starts fresh. Audio and controls share
  a bounded ordered queue; brief inference pauses apply backpressure, and a
  two-second stall fails explicitly.
- The frontend's server flag disables local Moonshine loading. Phone lock is
  supported as long as G2 PCM/WebSocket forwarding continues, as observed on the
  device. Real-device end-to-end behavior still needs validation after deployment.

## Prepare and build (Linux x86_64)

Run from `backend/`, with Go, GCC, curl, tar, and sha256sum installed:

```bash
bash infra/prepare-moonshine.sh /tmp/rev-eyes-moonshine
MOONSHINE_NATIVE_DIR=/tmp/rev-eyes-moonshine \
  bash infra/build-backend.sh /tmp/rev-eyes-backend
```

Preparation verifies the release archive's pinned SHA-256, then uses a small Go
setup command to download the English tiny-streaming model. Each file is checked
against the native release manifest's size and CRC32C. Large binary/model assets
are not committed. Preparation downloads files only; it does not deploy.

The resulting backend expects the native libraries in
`/opt/rev-eyes/moonshine/v0.1.5/lib`. For local execution, set `LD_LIBRARY_PATH` to
the prepared `lib` directory instead. The server host must have compatible glibc
and libstdc++; verify on the target host before deploying.

The existing `infra/deploy-backend-code.sh` accepts `MOONSHINE_NATIVE_DIR` and
copies its `lib` and `model` directories to the versioned runtime directory before
replacing the backend. Installation stages the runtime and renames it into place;
repeat deployments reuse identical files and reject different contents under the
same version path. Running processes never have their mapped libraries overwritten.
Without that variable the scripts prepare/use `/tmp/rev-eyes-moonshine` and build
the native backend by default. Set `SERVER_MOONSHINE_ENABLED=false` explicitly
to build the original static backend. The deployment script still runs migration 0014 and updates the supplied email/origin
settings: inspect those existing effects before any deployment. The initial
`deploy-lightsail.sh` path remains a static build; use the code-deployment path for
native Moonshine.

## Integration with the regular app

The listening pipeline changes only audio capture and transcription. Accurate
Deepgram utterances enter the same handler as manual voice input, including
session reopening and account-level turn ordering shared with typed chat.
Routing, tools, reminders, watches, profiles, and background memory learning
use the regular application services.

Eligible finalized utterances are queued for automatic memory extraction even
when the router chooses not to respond. The recorder uses the application
context, so accepted memory work survives a microphone stop or WebSocket
disconnect. Explicit memory commands keep their synchronous handling.

Apply the regular database migrations through `0018_memory_profile.sql` before
running this merged backend against an older database. The native deployment
helper does not apply these memory migrations automatically.

Configure the server service environment:

```dotenv
SERVER_MOONSHINE_ENABLED=true
SERVER_MOONSHINE_MAX_CONCURRENCY=1
MOONSHINE_MODEL_DIR=/opt/rev-eyes/moonshine/v0.1.5/model
```

The server flag also enables the existing candidate transcription handler. Its
Deepgram configuration and `CANDIDATE_AUDIO_MAX_CONCURRENCY` still apply.
A static binary rejects server listening at startup rather than silently falling
back. Server listening defaults on when the variable is unset. When intentionally
running or rolling back to a static/manual build, set `SERVER_MOONSHINE_ENABLED=false`.

The frontend defaults to continuous server capture. Set
`VITE_SERVER_LISTENING_ENABLED=false` only for intentional manual-mode builds.
The frontend contains no local speech model or gate.
Ship the compatible server first; an unavailable server reports a listening error.

## Verification

The temporary Eyes Listening Test app and `/ws/moonshine` endpoint have been
removed from the release. Rough Moonshine text stays internal to wake detection.

### Automated checks

Default backend tests work without downloading Moonshine. Native testing uses:

```bash
MOONSHINE_TEST_MODEL_DIR=/tmp/rev-eyes-moonshine/model \
CGO_ENABLED=1 \
CGO_LDFLAGS='-L/tmp/rev-eyes-moonshine/lib -Wl,-rpath,/tmp/rev-eyes-moonshine/lib' \
  go test -race -tags moonshine ./...
```

Set `MOONSHINE_TEST_WAV` to the upstream v0.1.5 `test-assets/two_cities_16k.wav`
fixture to include real speech inference. Paid Deepgram tests require explicit
`MOONSHINE_TEST_LIVE_DEEPGRAM=true`, `DEEPGRAM_API_KEY`, and synthetic PCM fixtures:
`MOONSHINE_TEST_PCM` says "glasses remind me to buy milk tomorrow" and
`MOONSHINE_TEST_FOLLOWUP_PCM` says "what about tomorrow" (16 kHz mono PCM16LE).
`TestNativeStreamingConversationRoundTrip` verifies two assistant turns on one
connection, idle closure, and return to native listening. Its assistant is a
test stub, so no actions or user memories are created.

Before enabling for users, measure trigger recall, false wakes, latency, CPU/RAM,
and concurrency on the deployment host using recorded G2 audio. A desktop native
smoke test establishes API/model compatibility, not production capacity or recall.
