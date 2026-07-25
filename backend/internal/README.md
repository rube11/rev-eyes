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
