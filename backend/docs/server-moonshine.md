# Server-side Moonshine listening

The opt-in path forwards G2 PCM to Go during ambient listening. Native Moonshine
recognizes speech on the server; the existing phrase policy gates finite Deepgram
Nova-3 clips. No Python runtime or worker is used. Master’s manual Deepgram mode remains available when the new flag is off.

## Runtime and audio behavior

- Go calls the Moonshine C API through CGo. The pinned Linux x86_64 release is
  v0.1.5; its header and MIT license are vendored in `internal/stt/moonshine`.
- A shared model serves independently allocated streams. Native calls, including
  copying borrowed transcripts, are serialized. Default admission is one listening
  session; excess sessions fail explicitly, without paid-streaming fallback.
- PCM is 16 kHz mono signed little-endian 16-bit. Each session keeps 30 seconds
  (960 KB) of PCM and evaluates recognition every 250 ms of received audio.
- Triggered clips include up to 10 seconds before the detected line, and two
  seconds after completion. A clip is capped at 30 seconds. Long uninterrupted
  requests can therefore be truncated; subsequent segments need a new trigger.
- Streams are recreated every 60 seconds with five seconds of audio replay to
  bound native transcript/audio history. Sample offsets suppress duplicate wakes.
  This should be evaluated against real long-form speech at stream boundaries.
- A manual tap opens a bounded candidate without a keyword. Tap-to-finish flushes
  it, including a reply whose first rough transcript has not arrived yet. Audio and control messages share one ordered queue.
- After a displayed assistant response, the frontend may arm one keyword-free
  reply. The server expires that authorization after at most 30 seconds even if
  WebView timers stop; frontend dismissal disarms it sooner.
- The existing candidate worker handles Deepgram concurrency, timeout, PCM
  clearing, accurate-transcript wake validation, and assistant routing. Rough
  Moonshine text stays out of history, logs, and glasses transcripts.
- WebSocket disconnect or explicit app sleep cancels native listening, active
  ambient clip processing, and queued ambient clips, dropping pending raw audio. Reconnect starts fresh. Audio overload fails the connection
  rather than building an unbounded backlog or blocking control messages.
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
Without that variable it builds the original static Go
backend. It still runs migration 0014 and updates the supplied email/origin
settings: inspect those existing effects before any deployment. The initial
`deploy-lightsail.sh` path remains a static build; use the code-deployment path for
native Moonshine.

## Integration with the regular app

The listening pipeline changes only audio capture and transcription. Accurate
Deepgram clips enter the same utterance handler as live voice input, including
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
A static binary rejects `SERVER_MOONSHINE_ENABLED=true` at startup rather than
silently falling back. When rolling back to a static build, disable that flag.

Build the frontend with `VITE_SERVER_LISTENING_ENABLED=true` to enable continuous
capture. The frontend follows master and contains no local speech model or gate.
Ship the compatible server first; an unavailable server reports a listening error.

## Verification

Default backend tests work without downloading Moonshine. Native testing uses:

```bash
MOONSHINE_TEST_MODEL_DIR=/tmp/rev-eyes-moonshine/model \
CGO_ENABLED=1 \
CGO_LDFLAGS='-L/tmp/rev-eyes-moonshine/lib -Wl,-rpath,/tmp/rev-eyes-moonshine/lib' \
  go test -race -tags moonshine ./...
```

Set `MOONSHINE_TEST_WAV` to the upstream v0.1.5 `test-assets/two_cities_16k.wav`
fixture to include real speech inference. No test calls the paid Deepgram API.

Before enabling for users, measure trigger recall, false wakes, latency, CPU/RAM,
and concurrency on the deployment host using recorded G2 audio. A desktop native
smoke test establishes API/model compatibility, not production capacity or recall.
