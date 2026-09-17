# Internal packages

The top level is reserved for application capabilities shared across features:

- `assistant`: utterance routing and response orchestration. Provider-specific
  code lives below it, such as `assistant/openai`.
- `automation`: user-confirmed work that continues after the current request.
- `auth`: token verification and short-lived WebSocket tickets.
- `memory`, `session`, and `notification`: persisted application state.
- `realtime`: authenticated connections, turn serialization, and audio admission.
- `ambient`: native listening, rolling audio, and persistent conversation handoff.
- `candidate`: accurate wake authorization and bounded clip transcription.
- `stt`: Deepgram transcription and the native Moonshine boundary.
- `tool`: the tool contract, registry with execution, and standalone tool adapters.
- `web`: shared HTTP policies and health handlers.

Automation is grouped by workflow:

- `proposal`: confirmation shared by reminder and watch proposals.
- `reminder`: reminder model, persistence, tool, and due-event execution.
- `watch`: watch model, persistence, tool, and due-event execution.
- `scheduler`: durable incoming due events and dispatch.
- `scheduler/registration`: durable outgoing schedule registration.

Keep interfaces beside the code that consumes them, and keep storage, transport,
and tool implementations with the feature whose state they own.

## Default server-listening flow

The frontend sends `ambient_start` and forwards PCM while awake and connected.
The ambient listener owns the native stream, rolling buffer, and active paid
conversation. A rough wake triggers buffered replay followed by live audio into
one Deepgram connection. Accurate speech must authorize automatic interaction;
follow-ups reuse that connection. Moonshine resumes after paid work joins.

A tap opens a manual conversation inside the ambient session. Finalization ends
an utterance without closing Deepgram; conversation stop returns to Moonshine.
Sleep stops ambient capture. Explicit manual mode retains the separate legacy
tap-to-talk lifecycle. Neither mode silently substitutes for a failed listener.

- `realtime/protocol.go` defines the socket messages and application handlers.
- `realtime/server.go` owns the connection and streaming lifecycle.
- `realtime/utterance_handler.go` delivers transcripts and assistant responses.
- `assistant/service.go` coordinates routing, context, and agent execution.
- `assistant/memory_management.go` handles memory review and removal responses.
- Root `utterance.go` persists transcripts and handles explicit/background memory
  recording. Both remembering and correcting use the same persistence path.

Rough Moonshine text is private trigger data. The temporary diagnostic app and
endpoint are removed from production. Atomic memory candidates in `memory/` are
a separate concept from audio candidates.

Frontend `RealtimeConnection` owns socket setup, adoption, listeners, and retries;
the glasses runtime serializes UI transitions and owns microphone interaction.
See [the refactor report](../docs/simplification-refactor.md) for behavior
boundaries and validation, and [server Moonshine](../docs/server-moonshine.md)
for runtime setup.

Run `go test ./...` for offline coverage. Live model and database integration
tests are opt-in; an offline pass does not verify deployed provider behavior.
