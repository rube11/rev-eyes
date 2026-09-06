# Internal packages

The top level is reserved for application capabilities shared across features:

- `assistant`: utterance routing and response orchestration. Provider-specific
  code lives below it, such as `assistant/openai`.
- `automation`: user-confirmed work that continues after the current request.
- `auth`: token verification and short-lived WebSocket tickets.
- `memory`, `session`, and `notification`: persisted application state.
- `realtime` and `stt`: the live glasses connection and transcription.
- `tool`: the tool contract, registry, executor, and standalone tool adapters.
- `web`: shared HTTP policies and health handlers.

Automation is grouped by workflow:

- `proposal`: confirmation shared by reminder and watch proposals.
- `reminder`: reminder model, persistence, tool, and due-event execution.
- `watch`: watch model, persistence, tool, and due-event execution.
- `scheduler`: durable incoming due events and dispatch.
- `scheduler/registration`: durable outgoing schedule registration.

Keep interfaces beside the code that consumes them, and keep storage, transport,
and tool implementations with the feature whose state they own.

## Tap-to-talk flow

The frontend sends `listening_start` after a tap, then streams PCM to Deepgram.
Deepgram's speech endpoint completes the utterance; another tap starts the next
turn. `listening_stop` remains available for explicit cancellation/finalization.

- `realtime/protocol.go` defines the socket messages and application handlers.
- `realtime/server.go` owns the connection and streaming lifecycle.
- `realtime/utterance_handler.go` delivers transcripts and assistant responses.
- `assistant/service.go` coordinates routing, context, and agent execution.
- `assistant/memory_management.go` handles memory review and removal responses.
- Root `utterance.go` persists transcripts and handles explicit/background memory
  recording. Both remembering and correcting use the same persistence path.

The retired Moonshine clip-upload protocol, wake-phrase gate, diagnostics, and
candidate-audio feature flags have been removed. Atomic memory candidates in
`memory/` are part of the active memory service.

Run `go test ./...` for offline coverage. Live model and database integration
tests are opt-in; an offline pass does not verify deployed provider behavior.
