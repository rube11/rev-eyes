# Moonshine to Deepgram Candidate-Audio Architecture

Status: Historical design. The `noMoonshine` branch has retired this pipeline.
See [the current package guide](../../internal/README.md#tap-to-talk-flow) for
the active tap-to-talk flow. The remaining content records the earlier design.

Last reviewed: 2026-08-05

## Executive verdict

The smallest viable architectural change is to add a frontend candidate-audio service, a typed WebSocket candidate message, and a finite-clip Deepgram adapter. The resulting accurate transcript should rejoin the system at the existing handleUtterance boundary. The session, memory, tools, proposals, notifications, and glasses rendering can remain intact.

The frontend now supports continuous local capture separately from backend transcription. Continuous capture does not continuously wake the backend: only an approved phrase family or an explicit tap may create a candidate, and the backend verifies non-manual candidates again against the accurate Deepgram transcript.

Assistant responses now use a bounded hands-free conversation lifecycle. A card remains visible for `2 seconds + approximately 1/3 second per word`, clamped to 5 through 14 seconds. Speech may interrupt the card immediately, and an eight-second voice window remains after the card clears. Moonshine supplies only VAD boundaries for that explicit reply; the original ring-buffer PCM is still sent to finite-clip Deepgram and the accurate transcript rejoins the normal utterance path.

## Current implementation status (authoritative)

The implemented candidate path is now:

    Even SDK PCM callback
      -> runtime-owned PCM copy
      -> AudioCaptureSession
      -> MoonshineShadowTranscriber
      -> CandidateAudioWindow / CandidateGate
      -> CandidateAudioTransport
      -> candidate_audio header + binary PCM frame
      -> realtime.Server validation and global admission
      -> per-connection candidate worker
      -> candidate.Service
      -> stt.ClipTranscriber / Deepgram prerecorded API
      -> accurate-transcript wake policy
      -> existing handleCompletedUtterance / handleUtterance path
      -> existing session, router, memory, proposal, tool, response, and glasses flow

Implemented lifecycle guarantees as of 2026-08-05:

- [audio-capture-session.ts](../../../frontend/src/even/audio-capture-session.ts) coordinates the G2 microphone and local transcription without putting that state machine in runtime.ts. Candidate mode does not report startup success until Moonshine and both relevant audio contexts are active. Shadow-only mode remains non-blocking so it cannot delay the legacy Deepgram path.
- [glasses-page-host.ts](../../../frontend/src/even/glasses-page-host.ts) now owns startup-page creation, serialized SDK page mutations, transcript upgrades, and page suspension. A failed bridge lookup is no longer cached permanently, so a later initialization can retry it.
- A local activation step has a five-second deadline. A failed required activation cancels Moonshine and shuts down device capture instead of leaving a false `LISTENING LOCALLY` state.
- Every candidate-mode tap arms a manual window. If a local gate window already exists, the tap upgrades it to `manual`, clears its automatic endpoint, and preserves tap-to-talk semantics through the backend wake check.
- Even SDK microphone-stop `false` results are retained and logged instead of discarded.
- Candidate uploads have a 75-second client terminal deadline. Expiry closes only the socket that owns the stale candidate, causing normal reconnect and backend cancellation.
- The backend admits raw clips into a bounded global pool before per-connection queueing. The existing compute semaphore continues to cover Deepgram transcription and downstream utterance handling.
- Backend candidate work has a 60-second deadline measured from clip acceptance. Timeout sends one terminal `assistant_done` when the connection is still alive.
- Raw PCM is zeroed on rejection, cancellation, timeout, worker drain, and immediately after the finite transcription handler returns. It is not retained through GPT or tool processing.
- [assistant-response-lifecycle.ts](../../../frontend/src/even/assistant-response-lifecycle.ts) owns only response-card and conversation deadlines, and [assistant-conversation-state.ts](../../../frontend/src/even/assistant-conversation-state.ts) owns the small response-to-reply state machine. Reading time begins after the SDK render succeeds rather than while the card is still being built. `runtime.ts` remains the UI orchestrator, while `MoonshineShadowTranscriber` exposes a one-shot voice-reply arm at its existing VAD speech-start boundary.
- Starting speech during a response replaces the card with `LISTENING`; the existing manual candidate mode then captures the complete utterance, including short answers that do not satisfy the ambient phrase gate. A pause does not end the turn until the existing two-second post-roll elapses, and resumed speech extends the same candidate.
- Focused replies are correlated to their candidate ID. A late `assistant_done`, `assistant_response`, or `assistant_repeat` for another in-flight ambient candidate cannot end the active reply turn.
- [realtime-protocol.ts](../../../frontend/src/even/realtime-protocol.ts) owns server-message parsing and workspace-resource validation instead of leaving that protocol code inside `runtime.ts`. An empty or malformed `assistant_response` is treated as a terminal completion so the glasses cannot remain stuck on `THINKING`.
- [realtime-socket.ts](../../../frontend/src/even/realtime-socket.ts) owns safe sends, close handling, and reconnect backoff. A socket that opens during teardown or after a newer connection generation wins is explicitly closed if the runtime never adopts it.
- [turn_coordinator.go](../../internal/realtime/turn_coordinator.go) serializes transcript persistence, proposal confirmation, routing, tools, and response persistence for one user/session within a backend process. Different sessions still run concurrently. This closes the same-process multi-socket proposal and conversation race without reducing the process-wide Deepgram limit to one.
- A live Deepgram turn now receives a child context that is canceled if transcript delivery or downstream handling exits early, preventing a failed WebSocket turn from leaving its transcription goroutine alive.
- `awaiting_confirmation` is now based on a successful `propose_task` or `propose_watch` tool result, not on the router's predicted action. Clarification questions therefore retain the ordinary follow-up prompt, while a real pending proposal receives the `SAVE THAT` / `NO` voice prompt. If proposal creation succeeds but final response generation fails, the effect metadata is preserved and a short confirmation fallback is returned instead of orphaning an invisible proposal.
- `show that again`, `show it again`, and `repeat that` are locally wakeable. After accurate Deepgram transcription the backend emits `assistant_repeat` before persistence or GPT routing, and the WebView restores its last assistant card with fresh deadlines.
- `save that` is an exact accepted answer for the existing pending reminder/watch proposal confirmer. It does not create a pending memory proposal; that domain model still does not exist.
- Continuous Deepgram remains available as the feature-flag rollback path.

Current automated verification:

- Frontend: 58 focused tests, ESLint, TypeScript project build, and production Vite build.
- Backend: full Go suite, candidate/realtime race tests, and `go vet`.

Remaining validation is primarily empirical: cold-start and resume behavior across repeated real G2 sessions, long-running WebView stability, CPU/battery/thermal measurements, and a labeled phrase corpus for recall and false-positive rates. A distinct durable commitment/event entity also remains a product decision; the minimum pipeline currently rejoins the existing router and proposal model.

Sections 1 through 8 below retain the original baseline mapping and design rationale. This status section and the stage table in section 9 are authoritative for what now exists.

## 1. Baseline architecture map (before candidate mode)

### End-to-end path

    G2 Even SDK audio event
      -> frontend initializeEvenExperience()
      -> binary WebSocket frame
      -> realtime.Server
      -> audio channel
      -> stt.Transcriber
      -> Deepgram live WebSocket
      -> partial transcript observer
      -> user_transcript WebSocket message
      -> finalized utterance
      -> handleUtterance()
      -> session transcript persistence
      -> assistant.Service
      -> assistant.Router / OpenAI classifier
      -> memory, agent, task/watch tool, or ignore
      -> assistant_response / assistant_done
      -> frontend handleServerMessage()
      -> glasses-ui page rendering

### Frontend audio and WebSocket lifecycle

The frontend is a sibling of the backend at ../../frontend.

- [App.tsx](../../../frontend/src/app/App.tsx) owns the React lifecycle. Its effect around line 375 calls initializeEvenExperience(accessToken, onResponse, onStatus) and invokes the returned cleanup function on unmount or token change.

- [runtime.ts](../../../frontend/src/even/runtime.ts) is the actual runtime controller. Despite being one function, it owns:

  - Even bridge setup.
  - WebSocket connection and reconnection.
  - Audio start and stop state.
  - Gesture handling.
  - Server message handling.
  - Page and display state.
  - Location forwarding.
  - Notification display.
  - Cleanup and sleep behavior.

- [client.ts](../../../frontend/src/shared/api/client.ts) creates the backend socket:

  1. Requests a single-use /auth/ws-ticket.
  2. Includes the current timezone.
  3. Opens /ws?ticket=....
  4. Uses an AbortController and timeout for connection establishment.

- The Even SDK event listener is installed around line 1094 of [runtime.ts](../../../frontend/src/even/runtime.ts). PCM first enters application code at event.audioEvent?.audioPcm around line 1104.

- Current audio forwarding is gated by listeningState === "listening". The callback makes a copy with Uint8Array.from(pcm).buffer and sends that copy as an untyped WebSocket binary message.

- Tap handling around line 998 of runtime.ts controls the current microphone:

  - Start: calls bridge.audioControl(true, AudioInputSource.Glasses), sends a listening_start message, and enters the listening state.
  - Stop: sends listening_stop, turns the microphone off, and returns to idle.
  - Receiving an assistant_response also turns the microphone off.

- [audio.ts](../../../frontend/src/even/audio.ts) is only a placeholder. It is not the current audio abstraction.

- The conversation feature hook and controls under ../../frontend/src/features/conversation are also placeholders. Moving Moonshine there would not align with the running application.

### Current frontend/server protocol

Inbound client text messages in [internal/realtime/server.go](../../internal/realtime/server.go):

- listening_start
- listening_stop
- location
- notification_ack

Inbound client binary messages have no envelope or identifier. Their meaning depends entirely on whether the connection currently has an active live audio channel.

Outbound server messages:

- user_transcript
- assistant_thinking
- assistant_response
- assistant_done
- listening_stopped
- notification

handleServerMessage around line 866 of runtime.ts interprets these. Transcript, thinking, response, and notification messages are rendered through [glasses-ui.ts](../../../frontend/src/even/glasses-ui.ts).

### WebSocket authentication and session state

- [internal/auth/tickets.go](../../internal/auth/tickets.go) authenticates the bearer token, resolves an application session, and issues a one-minute, single-use WebSocket ticket.

- The ticket contains a trusted tool.Scope from [internal/tool/tool.go](../../internal/tool/tool.go):

  - UserID
  - SessionID
  - TimeZone
  - UtteranceID is added later per utterance.

- session.Store.Resume in [internal/session/store.go](../../internal/session/store.go) resumes an active session within a 30-minute window or creates a new one. A WebSocket disconnect does not itself end that application session.

- session.Store.Append persists finalized user and assistant utterances and updates session activity.

- conversation.Manager in [internal/session/conversation.go](../../internal/session/conversation.go) reconstructs recent conversation context and compacts older context when its configured token limits are exceeded.

### Backend WebSocket and Deepgram path

[internal/realtime/server.go](../../internal/realtime/server.go) contains most realtime orchestration.

Important concurrency elements:

- readMessages is a goroutine that calls Gorilla WebSocket ReadMessage and sends incomingMessage values over an unbuffered channel.
- audio chan []byte is created on listening_start with capacity 100.
- transcription <-chan error reports completion of the Deepgram transcription goroutine.
- completed chan string has capacity 10.
- A transcript observer receives partial and final text updates.
- Completed utterances are processed sequentially by transcribeConnection.

Current behavior:

1. listening_start creates the audio channel and starts transcribeConnection.
2. Binary WebSocket frames are put on the audio channel.
3. listening_stop closes that channel.
4. stt.Transcriber consumes the channel.
5. Partial results become user_transcript messages.
6. A completed utterance causes assistant_thinking to be sent before semantic routing.
7. The configured UtteranceHandler processes it.
8. The result becomes assistant_response or assistant_done.

The current [stt.Transcriber](../../internal/stt/transcriber.go) interface is explicitly live-stream-oriented:

- Input: a receive-only channel of byte slices.
- Output: a send-only channel of transcript strings.
- Partial and final updates: TranscriptObserver.

[internal/stt/deepgram.go](../../internal/stt/deepgram.go) implements it with a persistent Deepgram live WebSocket configured as:

- Model: nova-3.
- Encoding: linear16.
- Sample rate: 16,000 Hz.
- Channels: 1.
- Language: en-US.
- Interim results enabled.
- Punctuation and smart formatting enabled.

[internal/stt/deepgram_handler.go](../../internal/stt/deepgram_handler.go) accumulates final fragments. A Deepgram speech-final event does not emit a completed utterance. Completion occurs when the backend explicitly finalizes the connection after the input channel closes.

The pinned Deepgram SDK contains a prerecorded FromStream API, but this repository neither wraps nor calls it. The existing implementation is therefore usable only as a live-stream adapter from the repository's perspective.

### Transcript, OpenAI, action, and memory path

[main.go](../../main.go) wires the realtime server. Its Utterance closure calls [handleUtterance](../../utterance.go).

handleUtterance is the key reuse boundary:

1. Persists the accurate user transcript.
2. Calls assistant.Service.HandleUtterance.
3. For ordinary actions, offers the persisted source to memory.Recorder.Capture without waiting for extraction or storage.
4. For ActionRemember, calls memory.Recorder.RememberExplicit and waits for candidate extraction and durable persistence before acknowledging success.
5. Persists a non-empty assistant response.
6. Returns realtime.UtteranceResult.

[assistant.Service.HandleUtterance](../../internal/assistant/service.go):

1. Assigns the utterance ID to the tool scope.
2. Checks whether a short yes/no utterance resolves an outstanding proposal.
3. Otherwise calls the activity router.
4. For respond, propose_task, or propose_watch, loads memory and conversation context and invokes the agent.
5. For ignore, state_update, and remember, it does not invoke the agent.

[internal/assistant/router.go](../../internal/assistant/router.go) defines:

- ActionIgnore
- ActionRespond
- ActionStateUpdate
- ActionRemember
- ActionProposeTask
- ActionProposeWatch
- ActionResolveProposal

Its Decision carries only the structured action, standalone query, and memory lookup. Memory content is deliberately not part of the routing result.

[internal/assistant/openai/classifier.go](../../internal/assistant/openai/classifier.go) invokes the OpenAI Responses API and requests a strict JSON Schema result. The router prompt already recognizes direct requests, reminders, and implied future tasks. However:

- The remember action identifies an explicit request to save information, but the router does not construct the memory.
- The classifier returns raw JSON to assistant.Router, which then unmarshals and validates it.
- It is not currently a generic commitment or event extractor.
- The router is not given timezone or current-time context.

[internal/assistant/openai/memory_extractor.go](../../internal/assistant/openai/memory_extractor.go) is a separate strict-schema invocation with a different responsibility. It returns zero or more atomic memory candidates from the finalized user utterance. [internal/memory/recorder.go](../../internal/memory/recorder.go) runs that extractor in two modes:

- Every non-remember outcome, including ignore, state_update, respond, proposal actions, and proposal confirmations, enters a bounded process-local queue through Capture. The assistant response path does not wait for extraction or persistence.
- An explicit remember action calls RememberExplicit synchronously. Success is acknowledged only after the candidate batch is persisted; unsafe content and utterances with no saveable candidates receive a truthful non-success response.

The recorder writes the entire candidate batch atomically through memory.Store. One source utterance may therefore create no memory, one memory, or several independently retrievable memories. This extractor does not choose assistant actions, invoke tools, or generate the user-facing response.

The requested GPT-5.4 Nano can fit this existing Responses API call. The dated model documented at review time is gpt-5.4-nano-2026-03-17, and the existing text.format JSON Schema pattern matches the Responses structured-output mechanism.

References:

- [OpenAI model and data-control documentation](https://developers.openai.com/api/docs/guides/your-data#which-models-and-features-are-eligible-for-data-residency)
- [OpenAI Structured Outputs documentation](https://developers.openai.com/api/docs/guides/structured-outputs#structured-outputs-vs-json-mode)

[internal/assistant/openai/agent.go](../../internal/assistant/openai/agent.go) is a separate model invocation. It creates user-facing responses and executes tools. That should remain the response and action model. Switching the router to Nano does not require changing the agent model.

### Persistence, scheduling, and later intervention

- [memory.Store](../../internal/memory/store.go) persists atomic candidate batches and links every accepted memory to its source transcript.
- [memory/candidate.go](../../internal/memory/candidate.go) adds stable keys and temporary-versus-durable lifecycle around the existing card content.
- [memory/candidate_store.go](../../internal/memory/candidate_store.go) uses PostgreSQL full-text search plus focused topic-kind and exact-entity matching.
- [reminder/tool.go](../../internal/automation/reminder/tool.go) exposes propose_task. It requires an absolute future RFC3339 due_at.
- [reminder/store.go](../../internal/automation/reminder/store.go) stores a pending task proposal tied to the source utterance.
- [confirmation.go](../../internal/automation/proposal/confirmation.go) accepts only short, explicit yes/no confirmation.
- Accepted proposals flow through the scheduler registration dispatcher.
- When due, [reminder/dispatcher.go](../../internal/automation/reminder/dispatcher.go) creates a notification.
- [notification.Service.Flush](../../internal/notification/service.go) sends it through [realtime.Hub](../../internal/realtime/hub.go).
- The frontend receives notification and renders it on the glasses.

There is no general commitment or event table. A future intention can currently become either:

- A pending reminder proposal.
- Zero or more atomic memories extracted independently from the finalized utterance.
- No persistent object at all.

### Cancellation and shutdown

- Each WebSocket connection has a derived context canceled when the connection ends.
- Deepgram live transcription derives from that context.
- Hub.Shutdown in [internal/realtime/hub.go](../../internal/realtime/hub.go) closes connections and waits for their handlers.
- [main.go](../../main.go) responds to process signals, cancels background dispatchers, shuts down HTTP, and then shuts down realtime connections.
- Frontend cleanup in runtime.ts aborts connection attempts, clears timers, unsubscribes SDK events, closes the socket, turns off audio, and stops location tracking.

## 2. Proposed architecture map

    Even SDK audioPcm
      -> CandidateAudioPipeline in frontend
           -> fixed-size PCM ring buffer
           -> Moonshine worker
           -> local approved-phrase gate
           -> pre-roll + post-roll candidate construction
      -> candidate_audio JSON header
      -> one associated binary PCM frame
      -> realtime.Server transport validation
      -> bounded candidate worker
      -> stt.ClipTranscriber
      -> Deepgram prerecorded transcription
      -> clear and release raw PCM
      -> authoritative wake-policy check on accurate text
      -> existing handleUtterance()
      -> existing assistant.Service and Router using GPT-5.4 Nano
      -> existing memory/agent/tool/proposal flow
      -> existing assistant_response/notification protocol
      -> existing glasses rendering

### Frontend responsibilities

New frontend responsibilities:

- Keep the G2 microphone active for local candidate detection.
- Copy incoming PCM immediately.
- Maintain a fixed-size rolling ring buffer.
- Feed Moonshine locally, preferably in a Web Worker.
- Run a local approved-phrase gate over rough transcript updates.
- Create finite audio windows.
- Serialize and upload only candidate windows.
- Drop audio when the socket is unavailable rather than persist it.

The automatic policy wakes on the standalone token "glasses" anywhere in the transcript and on the standalone token "need" (including "need to"). It also recognizes "Remember", "Remind me", "Don't let me forget", "I plan to", "I want to", "I should", and "I prefer". Tap-to-talk is an explicit manual override. Outside explicitly enabled local diagnostics, the rough transcript should not be stored, displayed, or used directly for backend actions.

### Backend responsibilities

New backend responsibilities:

- Validate candidate metadata and payload size.
- Associate metadata with exactly one binary clip.
- Bound concurrent clip processing.
- Use Deepgram's prerecorded API for the finite clip.
- Clear the owned raw byte slice as soon as transcription finishes.
- Recheck automatic candidates against the approved phrase families using the accurate transcript. Reject misses before persistence or OpenAI routing.
- Allow candidates marked manual by the tap-to-talk path to bypass the phrase check.
- Pass the accurate transcript into handleUtterance.

### Components that remain unchanged

At minimum:

- WebSocket ticket authentication and tool.Scope.
- Session resume and transcript storage.
- Conversation context management.
- handleUtterance.
- assistant.Service.
- Agent and tool execution.
- Memory persistence.
- Task and watch proposal confirmation.
- Scheduler and notification delivery.
- Existing assistant_response, assistant_done, and notification rendering.

### Old Deepgram flow

Candidate mode should bypass:

- Frontend listening_start.
- Per-frame binary forwarding.
- Backend audio chan []byte.
- transcribeConnection.
- The persistent Deepgram live connection.
- Partial user_transcript updates.

Keep that route behind a feature flag until candidate mode has been validated. It remains useful as a rollback path and potentially as an explicit push-to-talk mode.

Candidate and live audio modes should not operate concurrently on one socket because binary messages are currently untyped.

### Where transcripts rejoin

The rejoin point should be the UtteranceHandler currently wired to handleUtterance, not a new candidate-specific action path.

That preserves one implementation of:

- Transcript persistence.
- Confirmation handling.
- Router classification.
- Memory lookup.
- Agent and tool execution.
- Proposal persistence.
- Assistant transcript storage.

Do not classify once in a candidate service and then call assistant.Service, because the service would classify the same transcript again.

## 3. File-by-file change plan

| File | Current responsibility | Proposed change | Status | Likely types or functions |
|---|---|---|---|---|
| [frontend/runtime.ts](../../../frontend/src/even/runtime.ts) | Entire Even lifecycle, PCM callback, socket, gestures, display | Instantiate and dispose the candidate service; feed copied PCM; separate ambient capture state from tap-to-talk state; send candidates | Modify | Candidate callback, ambient capture state |
| [frontend/audio.ts](../../../frontend/src/even/audio.ts) | Unused placeholder | Make this the imperative candidate-audio facade | Modify | CandidateAudioPipeline, start, pushPcm, stop, dispose |
| frontend/src/even/pcm-ring-buffer.ts | Does not exist | Fixed-capacity PCM storage addressed by sample offsets | New | PcmRingBuffer, write, sliceWindow, clear |
| frontend/src/even/moonshine.ts | Does not exist | Worker-facing Moonshine lifecycle and transcript update API | New | MoonshineTranscriber, transcript segment types |
| frontend/src/even/moonshine.worker.ts | Does not exist | Run inference outside the UI and runtime callback | New | Worker request and result protocol |
| frontend/src/even/candidate-gate.ts | Does not exist | High-recall semantic trigger, window state, cooldown, deduplication | New | CandidateGate, CandidateTrigger, gate state machine |
| [frontend/client.ts](../../../frontend/src/shared/api/client.ts) | Ticket acquisition and socket creation | Mostly reuse; optionally expose typed socket helpers | Reuse or minor modification | No transport redesign required |
| frontend/src/shared/api/realtime-protocol.ts | Does not exist | Centralize currently inline and untyped message definitions | New | CandidateAudioHeader, server message union |
| [frontend/glasses-ui.ts](../../../frontend/src/even/glasses-ui.ts) | Builds glasses transcript and message pages | Continue rendering authoritative backend responses | Reuse | None |
| [frontend/App.tsx](../../../frontend/src/app/App.tsx) | Starts and stops the Even experience | Reuse; perhaps expose feature or status information only | Reuse or minor modification | None |
| frontend/package.json and lockfile | Frontend dependencies | Add the selected Moonshine runtime and worker or WASM build support | Modify | Dependency configuration |
| [frontend/env.ts](../../../frontend/src/shared/config/env.ts) | Frontend environment configuration | Candidate-mode feature flag and non-sensitive tuning | Modify | Feature configuration |
| frontend/app.json | Even app permissions and network whitelist | Reuse unless Moonshine assets or runtime impose packaging or worker changes | Conditional | None |
| [realtime/server.go](../../internal/realtime/server.go) | WebSocket protocol, live audio orchestration, UI events | Add candidate header and binary state, bounded worker lifecycle, validation, and candidate results | Modify | CandidateAudio, CandidateHandler, pending candidate state |
| internal/realtime/candidate_protocol.go | Does not exist | Isolate candidate metadata validation and assembly | New | Header validation, size and duration checks |
| [stt/transcriber.go](../../internal/stt/transcriber.go) | Live streaming STT contract | Preserve it; add a separate finite-clip contract | Modify | AudioFormat, ClipTranscriber |
| [stt/deepgram.go](../../internal/stt/deepgram.go) | Deepgram live WebSocket adapter | Retain for fallback; share configuration where sensible | Reuse or minor modification | Shared Deepgram options |
| internal/stt/deepgram_clip.go | Does not exist | Wrap Deepgram prerecorded FromStream | New | TranscribeClip |
| internal/candidate/service.go | Does not exist | Own validation, ephemeral clip transcription, and raw-buffer clearing | New | Service.Process, Result |
| [main.go](../../main.go) | Dependency construction and handlers | Construct clip transcriber and service, candidate concurrency limit, and feature flag; wire to existing utterance closure | Modify | Candidate dependencies |
| [utterance.go](../../utterance.go) | Canonical transcript-to-assistant path | Reuse for candidate audio; queue ordinary memory capture and wait for explicit remember persistence at this shared boundary | Reuse | UtteranceResult and memory-effect handling |
| [assistant/router.go](../../internal/assistant/router.go) | Action taxonomy and decision validation | Keep routing limited to action, query, and lookup; extend only if a true event union is required | Reuse or conditional | Event fields or typed decision |
| [OpenAI classifier](../../internal/assistant/openai/classifier.go) | Responses API strict-schema routing | Configure GPT-5.4 Nano and keep it separate from memory-content extraction | Configuration change | Action, query, memory lookup |
| [assistant/service.go](../../internal/assistant/service.go) | Confirmation, routing, context, agent invocation | Reuse; no candidate-specific branch should be necessary | Reuse | None |
| [OpenAI memory extractor](../../internal/assistant/openai/memory_extractor.go) | Zero-or-more atomic candidate extraction from finalized user text | Reuse for every accepted audio transport; never add a candidate-specific extractor | Reuse | Memory candidates only |
| [memory/recorder.go](../../internal/memory/recorder.go) | Bounded background capture plus synchronous explicit remembering | Run from the application lifecycle context; keep ordinary capture off the response critical path | Reuse | Capture, RememberExplicit, Run |
| [memory/store.go](../../internal/memory/store.go) | Atomic candidate persistence and indexed retrieval | Reuse for both explicit and background extraction | Reuse | RememberCandidates, Find |
| [reminder/tool.go](../../internal/automation/reminder/tool.go) | Creates pending reminder proposals | Reuse for actionable reminders with resolvable time | Reuse | None |
| [session/store.go](../../internal/session/store.go) | Session resume and transcript append | Reuse | Reuse | None |
| [realtime/hub.go](../../internal/realtime/hub.go) | Thread-safe writes and user connection fanout | Reuse | Reuse | None |
| [migrations](../../migrations) | Sessions, atomic memories, tasks, watches, notifications | Reuse the memory lifecycle and search schema; add another migration only if commitment becomes a new persistent entity | Reuse or conditional | Optional commitment schema |
| Existing and new tests | Unit and integration coverage | Add ring, gate, protocol, cancellation, size-limit, clip-STT, and duplicate-processing tests | Modify and new | Test fixtures and fakes |

