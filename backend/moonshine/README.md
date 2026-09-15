# Continuous server Moonshine test

Eyes Listening Test 0.1.5 uses this path:

Glasses PCM16 (16 kHz mono) → authenticated `/ws/moonshine` on Go →
native Moonshine streaming model → Go wake policy → selected PCM clip →
Deepgram → transcript returned to the test app.

The phone forwards every audio callback without local inference, an AudioContext,
VAD, or a keyword gate. Moonshine runs in a Go-owned Python subprocess using the
native `moonshine-voice` runtime. It emits partial and final transcripts; Go
matches keywords on completed speech and submits the matching audio to Deepgram.
The test endpoint displays text only and does not execute assistant actions.

## Deploy

From `backend`, with the deployment SSH identity loaded:

```sh
bash infra/deploy-moonshine-runtime.sh ubuntu@52.11.78.112
bash infra/deploy-backend-code.sh ubuntu@52.11.78.112 ferruben739@gmail.com https://rev-eyes.com,https://www.rev-eyes.com
```

The runtime installer pins moonshine-voice 0.1.5 and downloads the English tiny
streaming model once. It sets non-secret systemd environment overrides for the
Python executable, worker, and model paths. Restarting the backend applies them.

## Bounds and verification

- One simultaneous test on the current 1 GB instance; seven-minute server deadline.
- 32 seconds of PCM in RAM; selected speech clips capped at 30 seconds.
- Two pending Deepgram clips. Overflow and overlong speech are visible errors.
- No raw audio files or transcript logs. The page retains transcripts for the
  current test, including across visibility changes, but not across reloads.
- `audio_received` acknowledgements show PCM bytes written to the model process.
- `go test -race ./internal/realtime -run TestMoonshineStreams` checks the real
  WebSocket/framing/keyword/clip flow with inference replaced by a fixture worker.
- `TestMoonshineNativeDeepgram` is an opt-in deployment smoke test using
  `MOONSHINE_TEST_AUDIO` (synthetic raw PCM) and runtime-owned credentials.
- Actual phone lock-screen streaming must be verified on the glasses/phone.
  Server-side inference removes the previous local-model dependency; it does not
  prove that the host app keeps WebSocket delivery active while locked.

The older candidate-audio architecture document describes the previous path,
which still exists for the main app. This dedicated test endpoint supersedes
that path for Eyes Listening Test.
