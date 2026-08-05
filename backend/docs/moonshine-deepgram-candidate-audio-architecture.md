# Moonshine to Deepgram Candidate-Audio Architecture

Status: Candidate pipeline and lifecycle hardening implemented behind feature flags; real-device soak and performance validation remain.

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

- [audio-capture-session.ts](../../frontend/src/even/audio-capture-session.ts) coordinates the G2 microphone and local transcription without putting that state machine in runtime.ts. Candidate mode does not report startup success until Moonshine and both relevant audio contexts are active. Shadow-only mode remains non-blocking so it cannot delay the legacy Deepgram path.
- [glasses-page-host.ts](../../frontend/src/even/glasses-page-host.ts) now owns startup-page creation, serialized SDK page mutations, transcript upgrades, and page suspension. A failed bridge lookup is no longer cached permanently, so a later initialization can retry it.
- A local activation step has a five-second deadline. A failed required activation cancels Moonshine and shuts down device capture instead of leaving a false `LISTENING LOCALLY` state.
- Every candidate-mode tap arms a manual window. If a local gate window already exists, the tap upgrades it to `manual`, clears its automatic endpoint, and preserves tap-to-talk semantics through the backend wake check.
- Even SDK microphone-stop `false` results are retained and logged instead of discarded.
- Candidate uploads have a 75-second client terminal deadline. Expiry closes only the socket that owns the stale candidate, causing normal reconnect and backend cancellation.
- The backend admits raw clips into a bounded global pool before per-connection queueing. The existing compute semaphore continues to cover Deepgram transcription and downstream utterance handling.
- Backend candidate work has a 60-second deadline measured from clip acceptance. Timeout sends one terminal `assistant_done` when the connection is still alive.
- Raw PCM is zeroed on rejection, cancellation, timeout, worker drain, and immediately after the finite transcription handler returns. It is not retained through GPT or tool processing.
- [assistant-response-lifecycle.ts](../../frontend/src/even/assistant-response-lifecycle.ts) owns only response-card and conversation deadlines, and [assistant-conversation-state.ts](../../frontend/src/even/assistant-conversation-state.ts) owns the small response-to-reply state machine. Reading time begins after the SDK render succeeds rather than while the card is still being built. `runtime.ts` remains the UI orchestrator, while `MoonshineShadowTranscriber` exposes a one-shot voice-reply arm at its existing VAD speech-start boundary.
- Starting speech during a response replaces the card with `LISTENING`; the existing manual candidate mode then captures the complete utterance, including short answers that do not satisfy the ambient phrase gate. A pause does not end the turn until the existing two-second post-roll elapses, and resumed speech extends the same candidate.
- Focused replies are correlated to their candidate ID. A late `assistant_done`, `assistant_response`, or `assistant_repeat` for another in-flight ambient candidate cannot end the active reply turn.
- [realtime-protocol.ts](../../frontend/src/even/realtime-protocol.ts) owns server-message parsing and workspace-resource validation instead of leaving that protocol code inside `runtime.ts`. An empty or malformed `assistant_response` is treated as a terminal completion so the glasses cannot remain stuck on `THINKING`.
- [realtime-socket.ts](../../frontend/src/even/realtime-socket.ts) owns safe sends, close handling, and reconnect backoff. A socket that opens during teardown or after a newer connection generation wins is explicitly closed if the runtime never adopts it.
- [turn_coordinator.go](../internal/realtime/turn_coordinator.go) serializes transcript persistence, proposal confirmation, routing, tools, and response persistence for one user/session within a backend process. Different sessions still run concurrently. This closes the same-process multi-socket proposal and conversation race without reducing the process-wide Deepgram limit to one.
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

- [App.tsx](../../frontend/src/app/App.tsx) owns the React lifecycle. Its effect around line 375 calls initializeEvenExperience(accessToken, onResponse, onStatus) and invokes the returned cleanup function on unmount or token change.

- [runtime.ts](../../frontend/src/even/runtime.ts) is the actual runtime controller. Despite being one function, it owns:

  - Even bridge setup.
  - WebSocket connection and reconnection.
  - Audio start and stop state.
  - Gesture handling.
  - Server message handling.
  - Page and display state.
  - Location forwarding.
  - Notification display.
  - Cleanup and sleep behavior.

- [client.ts](../../frontend/src/shared/api/client.ts) creates the backend socket:

  1. Requests a single-use /auth/ws-ticket.
  2. Includes the current timezone.
  3. Opens /ws?ticket=....
  4. Uses an AbortController and timeout for connection establishment.

- The Even SDK event listener is installed around line 1094 of [runtime.ts](../../frontend/src/even/runtime.ts). PCM first enters application code at event.audioEvent?.audioPcm around line 1104.

- Current audio forwarding is gated by listeningState === "listening". The callback makes a copy with Uint8Array.from(pcm).buffer and sends that copy as an untyped WebSocket binary message.

- Tap handling around line 998 of runtime.ts controls the current microphone:

  - Start: calls bridge.audioControl(true, AudioInputSource.Glasses), sends a listening_start message, and enters the listening state.
  - Stop: sends listening_stop, turns the microphone off, and returns to idle.
  - Receiving an assistant_response also turns the microphone off.

- [audio.ts](../../frontend/src/even/audio.ts) is only a placeholder. It is not the current audio abstraction.

- The conversation feature hook and controls under ../../frontend/src/features/conversation are also placeholders. Moving Moonshine there would not align with the running application.

### Current frontend/server protocol

Inbound client text messages in [internal/realtime/server.go](../internal/realtime/server.go):

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

handleServerMessage around line 866 of runtime.ts interprets these. Transcript, thinking, response, and notification messages are rendered through [glasses-ui.ts](../../frontend/src/even/glasses-ui.ts).

### WebSocket authentication and session state

- [internal/auth/tickets.go](../internal/auth/tickets.go) authenticates the bearer token, resolves an application session, and issues a one-minute, single-use WebSocket ticket.

- The ticket contains a trusted tool.Scope from [internal/tool/tool.go](../internal/tool/tool.go):

  - UserID
  - SessionID
  - TimeZone
  - UtteranceID is added later per utterance.

- session.Store.Resume in [internal/session/store.go](../internal/session/store.go) resumes an active session within a 30-minute window or creates a new one. A WebSocket disconnect does not itself end that application session.

- session.Store.Append persists finalized user and assistant utterances and updates session activity.

- conversation.Manager in [internal/session/conversation.go](../internal/session/conversation.go) reconstructs recent conversation context and compacts older context when its configured token limits are exceeded.

### Backend WebSocket and Deepgram path

[internal/realtime/server.go](../internal/realtime/server.go) contains most realtime orchestration.

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

The current [stt.Transcriber](../internal/stt/transcriber.go) interface is explicitly live-stream-oriented:

- Input: a receive-only channel of byte slices.
- Output: a send-only channel of transcript strings.
- Partial and final updates: TranscriptObserver.

[internal/stt/deepgram.go](../internal/stt/deepgram.go) implements it with a persistent Deepgram live WebSocket configured as:

- Model: nova-3.
- Encoding: linear16.
- Sample rate: 16,000 Hz.
- Channels: 1.
- Language: en-US.
- Interim results enabled.
- Punctuation and smart formatting enabled.

[internal/stt/deepgram_handler.go](../internal/stt/deepgram_handler.go) accumulates final fragments. A Deepgram speech-final event does not emit a completed utterance. Completion occurs when the backend explicitly finalizes the connection after the input channel closes.

The pinned Deepgram SDK contains a prerecorded FromStream API, but this repository neither wraps nor calls it. The existing implementation is therefore usable only as a live-stream adapter from the repository's perspective.

### Transcript, OpenAI, action, and memory path

[main.go](../main.go) wires the realtime server. Its Utterance closure calls [handleUtterance](../utterance.go).

handleUtterance is the key reuse boundary:

1. Persists the accurate user transcript.
2. Calls assistant.Service.HandleUtterance.
3. For ActionRemember, stores a memory.
4. Persists a non-empty assistant response.
5. Returns realtime.UtteranceResult.

[assistant.Service.HandleUtterance](../internal/assistant/service.go):

1. Assigns the utterance ID to the tool scope.
2. Checks whether a short yes/no utterance resolves an outstanding proposal.
3. Otherwise calls the activity router.
4. For respond, propose_task, or propose_watch, loads memory and conversation context and invokes the agent.
5. For ignore, state_update, and remember, it does not invoke the agent.

[internal/assistant/router.go](../internal/assistant/router.go) defines:

- ActionIgnore
- ActionRespond
- ActionStateUpdate
- ActionRemember
- ActionProposeTask
- ActionProposeWatch
- ActionResolveProposal

Its Decision already carries structured action, query, memory lookup, and memory-card fields.

[internal/assistant/openai/classifier.go](../internal/assistant/openai/classifier.go) invokes the OpenAI Responses API and requests a strict JSON Schema result. The router prompt already recognizes direct requests, reminders, and implied future tasks. However:

- Passive preference retention is explicitly prohibited unless the user asks to remember it.
- The classifier returns raw JSON to assistant.Router, which then unmarshals and validates it.
- It is not currently a generic commitment or event extractor.
- The router is not given timezone or current-time context.

The requested GPT-5.4 Nano can fit this existing Responses API call. The dated model documented at review time is gpt-5.4-nano-2026-03-17, and the existing text.format JSON Schema pattern matches the Responses structured-output mechanism.

References:

- [OpenAI model and data-control documentation](https://developers.openai.com/api/docs/guides/your-data#which-models-and-features-are-eligible-for-data-residency)
- [OpenAI Structured Outputs documentation](https://developers.openai.com/api/docs/guides/structured-outputs#structured-outputs-vs-json-mode)

[internal/assistant/openai/agent.go](../internal/assistant/openai/agent.go) is a separate model invocation. It creates user-facing responses and executes tools. That should remain the response and action model. Switching the router to Nano does not require changing the agent model.

### Persistence, scheduling, and later intervention

- [memory.Store](../internal/memory/store.go) persists structured memory cards and links them to source transcripts.
- [memory/card.go](../internal/memory/card.go) supports kinds including preference, event, and goal.
- [reminder/tool.go](../internal/automation/reminder/tool.go) exposes propose_task. It requires an absolute future RFC3339 due_at.
- [reminder/store.go](../internal/automation/reminder/store.go) stores a pending task proposal tied to the source utterance.
- [confirmation.go](../internal/automation/proposal/confirmation.go) accepts only short, explicit yes/no confirmation.
- Accepted proposals flow through the scheduler registration dispatcher.
- When due, [reminder/dispatcher.go](../internal/automation/reminder/dispatcher.go) creates a notification.
- [notification.Service.Flush](../internal/notification/service.go) sends it through [realtime.Hub](../internal/realtime/hub.go).
- The frontend receives notification and renders it on the glasses.

There is no general commitment or event table. A future intention can currently become either:

- A pending reminder proposal.
- An explicitly requested memory card.
- No persistent object at all.

### Cancellation and shutdown

- Each WebSocket connection has a derived context canceled when the connection ends.
- Deepgram live transcription derives from that context.
- Hub.Shutdown in [internal/realtime/hub.go](../internal/realtime/hub.go) closes connections and waits for their handlers.
- [main.go](../main.go) responds to process signals, cancels background dispatchers, shuts down HTTP, and then shuts down realtime connections.
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
| [frontend/runtime.ts](../../frontend/src/even/runtime.ts) | Entire Even lifecycle, PCM callback, socket, gestures, display | Instantiate and dispose the candidate service; feed copied PCM; separate ambient capture state from tap-to-talk state; send candidates | Modify | Candidate callback, ambient capture state |
| [frontend/audio.ts](../../frontend/src/even/audio.ts) | Unused placeholder | Make this the imperative candidate-audio facade | Modify | CandidateAudioPipeline, start, pushPcm, stop, dispose |
| frontend/src/even/pcm-ring-buffer.ts | Does not exist | Fixed-capacity PCM storage addressed by sample offsets | New | PcmRingBuffer, write, sliceWindow, clear |
| frontend/src/even/moonshine.ts | Does not exist | Worker-facing Moonshine lifecycle and transcript update API | New | MoonshineTranscriber, transcript segment types |
| frontend/src/even/moonshine.worker.ts | Does not exist | Run inference outside the UI and runtime callback | New | Worker request and result protocol |
| frontend/src/even/candidate-gate.ts | Does not exist | High-recall semantic trigger, window state, cooldown, deduplication | New | CandidateGate, CandidateTrigger, gate state machine |
| [frontend/client.ts](../../frontend/src/shared/api/client.ts) | Ticket acquisition and socket creation | Mostly reuse; optionally expose typed socket helpers | Reuse or minor modification | No transport redesign required |
| frontend/src/shared/api/realtime-protocol.ts | Does not exist | Centralize currently inline and untyped message definitions | New | CandidateAudioHeader, server message union |
| [frontend/glasses-ui.ts](../../frontend/src/even/glasses-ui.ts) | Builds glasses transcript and message pages | Continue rendering authoritative backend responses | Reuse | None |
| [frontend/App.tsx](../../frontend/src/app/App.tsx) | Starts and stops the Even experience | Reuse; perhaps expose feature or status information only | Reuse or minor modification | None |
| frontend/package.json and lockfile | Frontend dependencies | Add the selected Moonshine runtime and worker or WASM build support | Modify | Dependency configuration |
| [frontend/env.ts](../../frontend/src/shared/config/env.ts) | Frontend environment configuration | Candidate-mode feature flag and non-sensitive tuning | Modify | Feature configuration |
| frontend/app.json | Even app permissions and network whitelist | Reuse unless Moonshine assets or runtime impose packaging or worker changes | Conditional | None |
| [realtime/server.go](../internal/realtime/server.go) | WebSocket protocol, live audio orchestration, UI events | Add candidate header and binary state, bounded worker lifecycle, validation, and candidate results | Modify | CandidateAudio, CandidateHandler, pending candidate state |
| internal/realtime/candidate_protocol.go | Does not exist | Isolate candidate metadata validation and assembly | New | Header validation, size and duration checks |
| [stt/transcriber.go](../internal/stt/transcriber.go) | Live streaming STT contract | Preserve it; add a separate finite-clip contract | Modify | AudioFormat, ClipTranscriber |
| [stt/deepgram.go](../internal/stt/deepgram.go) | Deepgram live WebSocket adapter | Retain for fallback; share configuration where sensible | Reuse or minor modification | Shared Deepgram options |
| internal/stt/deepgram_clip.go | Does not exist | Wrap Deepgram prerecorded FromStream | New | TranscribeClip |
| internal/candidate/service.go | Does not exist | Own validation, ephemeral clip transcription, and raw-buffer clearing | New | Service.Process, Result |
| [main.go](../main.go) | Dependency construction and handlers | Construct clip transcriber and service, candidate concurrency limit, and feature flag; wire to existing utterance closure | Modify | Candidate dependencies |
| [utterance.go](../utterance.go) | Canonical transcript-to-assistant path | Reuse unchanged initially; later return a richer disposition if UI signaling needs it | Reuse or optional modification | Optional richer UtteranceResult |
| [assistant/router.go](../internal/assistant/router.go) | Action taxonomy and decision validation | Reuse initially; extend only if a true event union is required | Reuse or conditional | Event fields or typed decision |
| [OpenAI classifier](../internal/assistant/openai/classifier.go) | Responses API strict-schema routing | Configure GPT-5.4 Nano; evolve its single schema for event extraction rather than adding a second model call | Configuration change; conditional schema change | Typed classifier result |
| [assistant/service.go](../internal/assistant/service.go) | Confirmation, routing, context, agent invocation | Reuse; no candidate-specific branch should be necessary | Reuse | None |
| [memory/store.go](../internal/memory/store.go) | Structured memory persistence | Reuse if product policy permits extracted preferences or events to become memories | Reuse | None |
| [reminder/tool.go](../internal/automation/reminder/tool.go) | Creates pending reminder proposals | Reuse for actionable reminders with resolvable time | Reuse | None |
| [session/store.go](../internal/session/store.go) | Session resume and transcript append | Reuse | Reuse | None |
| [realtime/hub.go](../internal/realtime/hub.go) | Thread-safe writes and user connection fanout | Reuse | Reuse | None |
| [migrations](../migrations) | Sessions, memories, tasks, watches, notifications | No change for minimum implementation; add a migration only if commitment becomes a new persistent entity | Conditional | Optional commitment schema |
| Existing and new tests | Unit and integration coverage | Add ring, gate, protocol, cancellation, size-limit, clip-STT, and duplicate-processing tests | Modify and new | Test fixtures and fakes |

## 4. Frontend integration

### Where Moonshine belongs

Create one imperative CandidateAudioPipeline inside initializeEvenExperience. This matches the current repository because runtime.ts, not React state, owns the Even bridge, microphone, socket, and cleanup lifecycle.

It should not be a React hook:

- PCM should not pass through React state.
- The frontend conversation hooks are not part of the running audio path.
- Audio callbacks, workers, timers, and ring buffers require deterministic imperative cleanup.

runtime.ts should remain the lifecycle owner, but not the implementation location for buffering, Moonshine, or gating. Putting those directly into the already large initializeEvenExperience closure would deepen its current coupling.

Initialize the service after the bridge is usable and the Moonshine model has loaded. Do not enable continuous ambient capture before successful initialization unless a deliberate degraded mode is selected.

### PCM ownership

Copy every audioPcm frame before retaining or asynchronously processing it. The current Uint8Array.from(pcm) already establishes the correct ownership boundary.

If the worker uses transferable buffers:

1. Copy the samples into the ring buffer first.
2. Transfer a separate buffer to the worker, or accept structured-clone copying.
3. Never transfer the only buffer before ring storage, because transfer detaches it.

### Ring buffer

Use a fixed-size circular typed-array buffer, not an array of arbitrary chunks.

Its capacity should be calculated from confirmed audio format:

    capacityBytes =
      sampleRate * channelCount * bytesPerSample * retentionSeconds

At the backend's assumed format, 16 kHz mono PCM16:

- 15 seconds: 480,000 bytes.
- 30 seconds: 960,000 bytes.

Track monotonic sample offsets in addition to wall-clock timestamps. Sample offsets make wraparound, overlap, and gaps deterministic.

Do not store raw audio in:

- React state.
- IndexedDB.
- localStorage.
- Application logs.
- Analytics events.

### Candidate windows

A reasonable starting policy is:

- Ring capacity: 30 seconds.
- Pre-trigger context: 8 to 12 seconds, initially 10 seconds.
- Post-trigger tail: 2 to 3 seconds or until Moonshine or local VAD reports a stable endpoint.
- Hard candidate limit: 20 to 30 seconds.

The 30-second ring should not automatically mean 30 seconds of pre-roll plus additional post-roll. Freeze the candidate's start sample when the gate fires, continue writing to the ring during post-roll, then copy one contiguous candidate window out.

### Duplicate and overlap control

Use a state machine such as:

    idle -> collecting-post-roll -> uploading/cooldown -> idle

Recommended behavior:

- Additional triggers while collecting extend the same candidate instead of creating another.
- Track lastSubmittedEndSample.
- Do not start the next candidate before that end, except for a deliberate 0.5 to 1 second continuity overlap.
- Fingerprint normalized rough transcript segments and suppress equivalent triggers for a short cooldown.
- Allow only one upload in flight and at most one merged pending candidate.
- If the socket is unavailable, expire the candidate instead of saving it for later.

### Use of the rough transcript

Moonshine output should be used only for:

- Gate feature extraction.
- Endpoint and stability detection.
- Deduplication.
- Optional local diagnostics behind a developer flag.

It should not:

- Be sent as the authoritative user transcript.
- Enter memory or tool execution.
- Be displayed as a user transcript in ambient mode.
- Be persisted.

The local gate should recognize only the approved phrase families. The backend repeats that deterministic check against Deepgram's accurate transcript so a Moonshine false positive cannot wake the model or enter conversation history. GPT-5.4 Nano remains the semantic decision-maker after wake authorization.

### WebSocket transport

Send:

1. A JSON candidate_audio header containing:

   - Candidate ID.
   - Encoding.
   - Sample rate.
   - Channel count.
   - Byte length.
   - Duration.
   - Capture start and end timestamps or sample offsets.
   - Gate category and confidence, if useful for metrics.

2. Exactly one immediately following binary WebSocket message containing the PCM.

Browser WebSockets preserve message order. This permits the server to associate the next binary message with a pending candidate ID as long as candidate uploads are serialized.

Do not include the rough transcript in production metadata unless it proves necessary. It is additional sensitive derived data and the backend should not trust it.

Candidate mode and legacy live streaming should be mutually exclusive per connection. Otherwise, a binary frame cannot safely be distinguished from a live audio chunk.

### Ambient capture state

The current listeningState is insufficient. Candidate mode needs separate concepts:

- captureState: whether ambient audio is being captured locally.
- interactionState: idle, processing, displaying a response, or explicit legacy listening.
- candidateState: idle, collecting, uploading, cooldown.

In candidate mode, receiving assistant_response should not automatically disable ambient capture. Sleep, teardown, sign-out, permission loss, and bridge disconnection should still stop capture, terminate the worker, cancel post-roll collection, and clear every raw buffer.

## 5. Backend integration

### Entry point and protocol state

Add candidate_audio to the text-message switch in realtime.Server.

The connection should hold a small pending-header state:

- Header received with validated candidate ID and expected byte count.
- Exactly one binary message expected next.
- Another header while pending is rejected.
- Orphan binary is rejected or ignored.
- Length mismatch rejects the candidate.
- Reused IDs are rejected for the connection lifetime.
- Candidate mode cannot overlap an active legacy audio channel.

The current read limit is 1 MiB. Thirty seconds of 16 kHz mono PCM16 is 960,000 bytes, which is uncomfortably close. At 48 kHz it would be approximately 2.88 MB. Confirm the real format, then set both:

- A protocol duration and byte cap.
- A modest WebSocket read limit derived from that cap.

Do not simply remove the limit.

### Processing location

The WebSocket handler should assemble and validate transport data, but it should not directly call Deepgram or OpenAI.

Introduce a candidate service with a narrow responsibility:

    validated candidate bytes
      -> ClipTranscriber
      -> accurate transcript
      -> clear candidate bytes
      -> return transcript

The realtime worker then calls the existing UtteranceHandler with that transcript.

This keeps transport concerns in internal/realtime, speech-to-text in internal/stt, and transcript semantics in the existing assistant pipeline.

### Deepgram interface

Do not force finite clips through the current channel-based Transcriber. Although closing a channel could make it act finite, it would still:

- Open a live Deepgram WebSocket.
- Depend on explicit finalization behavior.
- Expose partial-transcript machinery that candidate mode does not need.
- Preserve an interface shaped around long-lived streaming.

Add a separate interface:

- AudioFormat: encoding, sample rate, channels.
- ClipTranscriber.TranscribeClip(ctx, pcm, format) returning a transcript or error.

The existing Deepgram concrete implementation may implement both interfaces, but separate source files are preferable. The clip method should use the SDK's prerecorded FromStream operation.

### Accurate transcript routing

After Deepgram returns:

1. Normalize and validate the transcript.
2. Clear and release the raw PCM.
3. If empty, finish silently.
4. Call the existing handleUtterance closure with the accurate transcript and original session scope.

This means persistence still happens before assistant routing, as it does today.

### GPT-5.4 Nano extraction

The existing classifier is the right invocation site, but its abstraction needs refinement if structured event means more than the current Decision.

Minimum implementation:

- Set OPENAI_ROUTER_MODEL to a pinned GPT-5.4 Nano model.
- Keep the existing strict JSON Schema.
- Continue returning the current action taxonomy.

Richer implementation:

- Extend Decision with a typed event union such as request, commitment, intention, reminder, preference, or ignore.
- Include fields such as title, schedule text, confidence, and whether confirmation is required.
- Make the classifier return a typed result rather than raw JSON.
- Let assistant.Router validate and translate that result into existing actions.

Do not introduce a candidate-specific Nano extractor followed by the existing router. That would duplicate cost and can produce conflicting decisions.

Timezone-sensitive extraction needs special handling. Router.Route currently receives only transcript text, so it cannot safely turn tomorrow into an absolute timestamp. Either:

- Extract schedule_text such as "tomorrow after class" and let the existing agent and tool resolve it using tool.Scope.TimeZone.
- Extend the router input with timezone and current time.

The first is the smaller change.

### Cancellation and concurrency

Candidate work should derive from the connection context. Cancellation must propagate through:

- Deepgram prerecorded HTTP request.
- OpenAI Responses request.
- Agent and tool work.
- Database operations where supported.

Use an owned worker, not detached goroutines:

- One active candidate per connection.
- At most one queued or merged candidate.
- A configurable process-wide semaphore, initially perhaps 2 to 4 Deepgram clips.
- Serialize by user and SessionID, because the hub permits multiple sockets for the same application session and conversation and proposal operations are ordered turns. The implementation now does this at the shared utterance rejoin point.
- Wait for the worker during connection shutdown so Hub.Shutdown retains its current semantics.

After Deepgram returns, call clear(audio) and remove all references. This is best-effort in Go because garbage collection is not cryptographic erasure. The raw payload must never be placed in logs, database rows, object storage, retry queues, or error objects.

### Response signaling

The current live path sends user_transcript and assistant_thinking before the router decides to ignore an utterance. That is unsuitable for ambient candidates.

For candidate mode:

- Do not send partial user_transcript.
- Do not send assistant_thinking before semantic classification.
- For ignored candidates, send nothing or a candidate-specific non-visual acknowledgement.
- For actionable results, reuse assistant_response.
- assistant_done can be retained for protocol state, but should not cause ambient UI activity.

If early thinking feedback is required for direct assistant requests, the handler needs to expose a semantic disposition before the full agent response. The current UtteranceResult does not provide that phase boundary.

## 6. Existing abstractions evaluation

### Reusable

- handleUtterance is the strongest reuse boundary.
- Session resolution and tool.Scope already carry the identity and timezone needed by downstream tools.
- assistant.Service centralizes proposal confirmation, routing, memory context, and agent execution.
- Strict-schema Responses classification is already present.
- Memory, reminder, watch, scheduler, notification, and glasses rendering are independent of the audio transport.
- The connection write mutex in realtime.Hub supports writes from controlled worker or result paths.
- Context propagation and shutdown structure are broadly reusable.

### Poorly suited or tightly coupled

1. **runtime.ts remains overloaded.** Candidate transport, PCM windows, capture coordination, response deadlines, response/reply state, protocol parsing, and page-host mutations have been extracted. The file still coordinates connection, gesture, notification, location, and presentation policy and remains the largest frontend maintenance risk.

2. **The baseline frontend audio abstraction was insufficient.** `audio.ts` now serializes device transitions, while `AudioCaptureSession`, `MoonshineShadowTranscriber`, `CandidateAudioWindow`, and `CandidateAudioTransport` own distinct parts of candidate capture. The unused conversation placeholders still should not become a second runtime path.

3. **The STT interface assumes persistent streaming.** Channels, observers, and explicit finalization are the wrong contract for a finite prerecorded clip.

4. **Binary WebSocket messages are implicitly typed.** Their meaning depends on mutable connection state. This is safe only for the current single live-stream mode.

5. **Realtime transport is coupled to UX timing.** It emits transcript and thinking messages before knowing whether speech is actionable. Ambient processing needs classification to remain silent until a meaningful result exists.

6. **Legacy connection backpressure can block control messages.** Sending a live binary frame into the 100-element audio channel still happens in the main connection loop. Candidate mode avoids that channel and uses bounded admission plus a per-connection worker.

7. **Legacy concurrency is not globally bounded.** Candidate work is globally bounded, and assistant turns are serialized per user/session inside one backend process. Live streaming connections remain outside the candidate semaphore, and multi-process ordering would require a distributed/session-store mechanism.

8. **UtteranceResult still exposes effects indirectly.** It now carries targeted workspace resources and authoritative confirmation state, but it does not expose a general typed list of persisted effects or semantic dispositions.

9. **The baseline confirmation signal was predictive.** This is now corrected for reminder/watch proposals by propagating successful proposal-tool results through `AgentResult` and `Outcome`. A future memory-proposal domain would need the same effect-based contract.

10. **The existing taxonomy does not fully represent the goal.** A commitment is not presently a persistent domain object. state_update also has no obvious dedicated persistence path.

11. **Passive preference storage conflicts with current policy.** The classifier explicitly avoids remembering preferences unless the user requests it. Candidate detection alone should not silently change that privacy and product rule.

12. **OpenAI transport code is duplicated.** Classifier and agent use separate raw HTTP implementations. This does not block the candidate pipeline, but extending both independently would add maintenance cost.

13. **The page-host exit subscription is module-lifetime.** `glasses-page-host.ts` registers it once, preventing duplicates across token-driven runtime recreation, but the SDK unsubscribe handle is intentionally retained for the WebView lifetime rather than disposed per signed-in session.

### Transcript-path duplication to avoid

Do not create:

- A candidate handler that persists transcripts independently.
- A second memory or action executor just for candidates.
- A Nano event extractor followed by the existing Nano router.
- A Moonshine transcript path into handleUtterance followed later by the Deepgram transcript.
- A frontend direct-Supabase path for voice-derived memories.

There should be exactly one authoritative transcript and one downstream assistant-processing path.

## 7. Recommended data flow example

For:

> I need to go to the gym tomorrow after class.

1. **PCM arrival**

   runtime.ts receives event.audioEvent.audioPcm from the Even callback. In candidate mode, this occurs continuously rather than only while listeningState is active.

2. **Local buffering**

   CandidateAudioPipeline.pushPcm copies the frame into PcmRingBuffer. Assume a 30-second capacity after the actual format is confirmed.

3. **Moonshine transcription**

   The Moonshine worker receives PCM increments and emits rough partial or final text such as "I need to go to the gym tomorrow after class."

4. **Local gate**

   CandidateGate recognizes the approved standalone "need" token. It records the trigger sample and moves to post-roll collection. This remains provisional until the backend checks the accurate transcript.

5. **Post-trigger collection**

   The pipeline waits for approximately 2 to 3 seconds or a stable endpoint, then extracts perhaps 10 seconds of pre-roll plus the utterance and tail. Overlapping new triggers are merged.

6. **WebSocket transmission**

   The client sends a candidate_audio header followed by exactly one binary PCM frame.

7. **Backend assembly**

   realtime.Server validates metadata and byte count, associates the frame with the candidate ID, and hands ownership to a bounded candidate worker.

8. **Deepgram transcription**

   stt.ClipTranscriber calls the Deepgram prerecorded endpoint. It returns the accurate transcript. The candidate service immediately clears the raw byte slice.

9. **Authoritative wake check**

   candidate.MatchWakePhrase checks the accurate transcript. Because it contains the standalone token "need", processing continues. A non-match returns assistant_done without persistence or an OpenAI call. A tap-generated candidate carries the manual category and bypasses this phrase check.

10. **Nano extraction**

   The worker calls the existing handleUtterance. That persists the user transcript, then assistant.Service invokes assistant.Router, whose OpenAI classifier is configured for GPT-5.4 Nano.

11. **Commitment or event decision**

    With the current prompt, this is likely to become propose_task, similar to its existing "call the dentist tomorrow" example. With an extended schema, it could first be represented as:

    - Kind: future intention or commitment.
    - Title: go to the gym.
    - Schedule text: tomorrow after class.
    - Confirmation needed: true.

    This is not currently a durable commitment entity.

12. **Storage or scheduling**

    "After class" is not an absolute RFC3339 time. Therefore:

    - If conversation or memory contains the class time, the agent can resolve it and call propose_task.
    - Otherwise, the correct response is a clarification question.
    - Once a valid time exists, reminder.Store.Propose persists a pending task proposal.
    - A later "yes" utterance is handled by proposal.Confirmer, which accepts and schedules it.

    Without adding a new commitment table, nothing should claim a generic commitment was stored merely because Nano classified it.

13. **Later intervention**

    At the accepted task's due time, the scheduler calls the reminder dispatcher, which inserts a notification and flushes it through realtime.Hub. The existing frontend notification handler renders it on the glasses.

## 8. Questions and uncertainties

The implementation resolved the package, model, adapter, protocol, and reconnect-policy questions. It uses `@moonshine-ai/moonshine-js` 0.1.29 with the tiny model, converts copied G2 linear16 samples to float32 without resampling, drops unsent candidate audio, and does not render rough or accurate candidate transcripts as a live glasses transcript. The tested app can be built, sideloaded, and run in the target WebView.

The remaining uncertainties require product decisions or broader real-device evidence:

- The Even SDK still exposes PCM as an untyped `Uint8Array`; the 16 kHz, mono, signed 16-bit little-endian contract is an application assumption supported by current diagnostics rather than an authoritative SDK type.
- Long-running CPU, memory, thermal, and battery impact during continuous inference.
- Cold model download, caching, offline behavior, and the final distribution/licensing requirements for model assets.
- Audio-context resume reliability across repeated reconnects, sleep/wake transitions, app backgrounding, and WebView recreation.
- Whether the WebView and G2 microphone remain active, paused, or terminated under every phone/glasses background state.
- Whether the SDK reuses `audioPcm` backing buffers. The implementation defensively copies every accepted frame regardless.
- Whether PCM frames can have gaps and what cadence or timing guarantees the SDK provides.
- Moonshine VAD/endpoint recall under real background noise, accents, short commands, and clipped wake phrases.
- Deepgram prerecorded accuracy for the actual candidate-duration and signal-level distribution. Short low-volume clips have already produced empty transcripts during manual testing.
- The consent, disclosure, and retention policy for ambient candidate audio sent to Deepgram, beyond the implemented ephemeral handling.
- Whether passive preferences should be stored automatically. Current policy still requires an explicit memory request.
- What a commitment means as a durable product entity. No commitment table exists.
- How phrases such as "after class" should be resolved without calendar context.
- How multiple devices or sockets targeting one session should be ordered.
- Whether non-reminder commitments should produce later proactive intervention. Today, proactive delivery exists for accepted reminders and watches.
- The observed frequency and recovery behavior of Even SDK microphone-stop failures on real hardware.

Binary WebSocket reception, candidate metadata and ID association, and finite-clip semantics are now implemented. The remaining protocol uncertainty is how the target WebView behaves during long background, sleep, reconnect, and repeated audio-context resume cycles.

## 9. Recommended implementation order

| Stage | Work | Status | Acceptance criteria |
|---|---|---|---|
| 1 | Instrument current PCM with content-free metrics | Implemented; device observations performed | Confirm format, frame cadence, bytes per second, gaps, and maximum frame size on real G2 hardware. No raw audio or transcript content enters logs. |
| 2 | Build the ring buffer independently | Complete | Unit tests prove correct ordering across wraparound, exact capacity, window extraction, clearing, and bounded memory. |
| 3 | Integrate Moonshine behind a frontend flag while retaining current Deepgram behavior | Functionally complete; performance measurements pending | Local transcription works on the target WebView without blocking gestures or rendering; CPU, memory, battery, and latency are measured. |
| 4 | Add local gate diagnostics only | Implemented and manually tuned; labeled-corpus metrics pending | The configured token and phrase families trigger reliably, speech without those signals does not wake the backend agent, and diagnostics contain no raw PCM. Measure false negatives, false positives, trigger delay, duplicates, and candidate duration. |
| 5 | Add candidate WebSocket protocol with a fake backend handler | Complete | Header and binary association, byte limits, invalid ordering, reconnect, duplicate IDs, cancellation, global admission, deadlines, and legacy-mode compatibility are tested. |
| 6 | Add finite-clip Deepgram transcription | Implemented; broader recorded-fixture evaluation pending | Empty, error, and timeout cases terminate; cancellation works; raw bytes are cleared and never persisted. Compare real recorded fixtures against expected transcripts. |
| 7 | Add the accurate-transcript wake check, then rejoin handleUtterance | Complete | Unapproved automatic candidates are discarded before persistence or OpenAI; manual candidates and approved phrases use the same session, memory, proposal, tool, response, and notification path as equivalent live transcripts. |
| 8 | Configure GPT-5.4 Nano and evolve the schema only as needed | Existing structured router reused; distinct commitment schema undecided | Exactly one router classification occurs per accepted candidate. Add a new event schema only if commitments become a separate durable domain object. |
| 9 | Enable candidate mode behind a server and client feature flag | Implemented; soak validation pending | Candidate mode sends no listening_start or continuous backend PCM; ignored speech produces no glasses activity; rollback to legacy live mode requires configuration only. |
| 9a | Add hands-free response and answer windows | Implemented; real-device timing validation pending | Cards auto-clear using bounded reading time; speech during the card or eight-second grace window sends original PCM through Deepgram without a tap; matching terminal messages alone end the focused turn; replay and existing proposal confirmation work by voice. |
| 10 | Compare production-quality metrics | Not started | Measure Deepgram audio minutes, OpenAI calls, end-to-end latency, local battery and CPU, candidate recall, false positives, duplicate actions, and user-visible interruptions against the current baseline. |

## Final recommendation

The frontend candidate service buffers copied PCM, runs Moonshine locally, applies the approved phrase policy, and sends one metadata header plus one PCM frame. The backend uses a bounded candidate transport path and separate prerecorded ClipTranscriber. After transcription and immediate audio disposal, the accurate text must pass the same wake policy before entering the existing handleUtterance path.

That is the smallest change that preserves the working session, response, tool, memory, proposal, scheduling, notification, and glasses-display architecture. The only domain-level expansion beyond it is optional: a real commitment or event model if reminders and memory cards are not sufficient.
