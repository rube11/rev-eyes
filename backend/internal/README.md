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

## Crowded package map

`assistant/openai` is organized by model responsibility:

- root: final response composition, conversation compaction, tonality, and the
  legacy OpenAI router retained for comparison evaluations;
- `extraction`: finalized-utterance to memory-candidate extraction;
- `responses`: shared Responses API transport and output parsing;
- `routing`: retrieval enrichment after Jev has fixed the action;
- `tooling`: argument preparation after Jev has fixed the tool selection.

Files beginning with `eval_` are opt-in or synthetic behavior evaluations, not
production wiring. `compatibility.go` only keeps those evaluations compatible
with the focused packages; `main.go` imports the focused packages directly.

The larger cohesive packages use filename families instead of nested packages:

- `realtime`: `server` owns connection lifetime, `protocol` owns wire shapes,
  `*_handler` files own entry points, `candidate_*` owns admitted audio work,
  and `turn_coordinator` serializes assistant turns;
- `memory`: `card` and `candidate` own domain values, `store` and `*_store` own
  persistence, `lookup` and `profile` own reads, `management` owns mutations,
  `recorder` owns asynchronous learning, and `workspace*` owns the HTTP editor.

These remain single packages because their implementations share core types and
lifecycle state; splitting them further would create forwarding APIs without a
real dependency boundary.

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
- `assistant/response_context.go` shares the retrieved context across Jev tool
  selection, argument preparation, and final response generation.
- `assistant/tool_workflow.go` executes Jev-selected tools before the final model
  is invoked.
- `assistant/memory_management.go` handles memory review and removal responses.
- Root `utterance.go` persists transcripts and handles explicit/background memory
  recording. Both remembering and correcting use the same persistence path.

Rough Moonshine text is private trigger data. The temporary diagnostic app and
endpoint are removed from production. Atomic memory candidates in `memory/` are
a separate concept from audio candidates.

Frontend `RealtimeConnection` owns socket setup, adoption, listeners, and retries;
the glasses runtime serializes UI transitions and owns microphone interaction.
See [server Moonshine](../docs/server-moonshine.md) for runtime setup.

## Jev routing and tool workflow

Each assistant turn first uses Jev to choose the utterance action. Tool-bearing
requests route as ordinary responses; tool choice happens only in the workflow.
Open-ended route fields are enriched separately, then the service loads the saved profile,
retrieves relevant memories, and prepares recent conversation context. The
resulting `assistant.ResponseContext` contains the routed query, profile,
retrieved memories, conversation summary and messages, local time, time zone,
and turn flags. Authentication remains in the trusted `tool.Scope`.

For response turns, `JevToolClassifier` asks one independent Noul question for
each currently available tool. Answers above the explicit 0.5 policy threshold
select tools; selecting zero or several tools is valid. Code owns a bounded loop
of at most eight selection rounds and passes completed observations back to Jev.
This allows evidence from location or search to make a later action ready while
keeping tool output as data rather than authorization.

The provider client lives in `assistant/jev`; OpenAI-specific enrichment,
argument preparation, and response composition live in `assistant/openai`.

Execution limits are deterministic:

- `search_web` may execute once per turn and is never retried.
- Every other tool may be attempted once per turn.
- Location results or errors return to Jev before dependent tools run.
- Read-only tools execute before a proposal selected in the same round; Jev must
  reconsider the proposal against the resulting evidence.
- Reminder and watch tools create inactive proposals only. Existing confirmation
  code must activate them, and a proposal is never repeated after failure.

The OpenAI argument builder receives exactly the tools Jev selected and a strict
schema containing only their argument objects. It may fill open-ended search
queries and dates, but it cannot add, remove, skip, or execute tools. The workflow
and each tool validate the arguments before effects. A malformed structured
answer gets one formatting retry.

After the loop ends, `OPENAI_AGENT_MODEL` receives the shared context and recorded
tool results once. It has no tool definitions or execution authority and only
composes the user-facing response. Memory-review turns bypass the workflow.

The integration requires `JEV_API_KEY`, `OPENAI_API_KEY`,
`OPENAI_ROUTER_MODEL`, and `OPENAI_AGENT_MODEL`, plus credentials required by
registered production tools. Offline coverage runs with `go test ./...`; live Jev
selection and pipeline checks are opt-in via `RUN_LIVE_JEV_TOOL_TEST=1`. Other
live model and database tests are also opt-in; offline success does not verify
deployed provider behavior.
