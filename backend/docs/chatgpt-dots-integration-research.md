# Rev Eyes → ChatGPT dots: integration research

Researched **2026-10-30** against current primary documentation and repository
commit `f6af7b930672a801951e747b868d4c1fc367a4f4`.
Scope: research only. No APK installation, credential extraction, private API
inspection, account connection, or application changes. No live account or
hardware tests were performed. The parent thread owns T3 implementation.

## Recommendation

**Do not promise a direct Dots thread client yet.** I found no documented public
contract for a third-party client to enumerate a personal dot's existing
threads, read their messages, and append input to a selected thread. This is a
documentation finding, not proof that OpenAI has no private or partner interface.
An Android APK does not establish that contract.

For the requested sequence—short thread descriptions → select → tap and speak →
continue that exact thread—keep the parent thread's T3 work independent. For
Dots, pursue a **small, explicitly mediated companion**: the dot publishes
selected work summaries to Rev Eyes through an authenticated MCP plugin; the
user continues work through a supported ChatGPT contact method. Treat automatic
glasses-to-dot delivery as an unresolved gate, not a completed integration.

If immediate, fully native thread interaction matters more than preserving the
existing dot, Rev Eyes can own its conversations and use documented OpenAI APIs.
That is a separate assistant, with separately supplied context and tools.

## Product identity

The likely product is **OpenAI's dot inside ChatGPT**. A dot is an ongoing agent;
ChatGPT Work executes delegated work; Codex handles coding tasks. These are
related experiences, not interchangeable account APIs. Dots are available in
the ChatGPT mobile and desktop apps and desktop web, subject to account rollout.
[Meet dots](https://learn.chatgpt.com/docs/dots).

There are unrelated products with similar names. For example,
[Dots at dotsai.bot](https://dotsai.bot/terms) describes an on-demand chat service
and explicitly disclaims external account access and outside actions. Its
documentation is not evidence about OpenAI dots. An exact product link is
necessary before applying any separate app's integration documentation.

**Identity remains unconfirmed.** Before implementation, obtain the exact app or
website link if this is not the dot within ChatGPT. Also establish whether
“threads” means the dot's own conversation, its delegated Work/Codex tasks, or
all ChatGPT chats.

## Supported surfaces and their limits

| Surface | Established capability | Consequence for Rev Eyes |
| --- | --- | --- |
| ChatGPT dot UI | Open a dot conversation and call it; use the same dot across supported clients. | A supported human access path, not an external thread API. |
| Dot task/context tools | A dot can search past ChatGPT chats, create visible tasks, continue existing local Codex tasks, and follow up on tasks it created. | Ask the dot to curate relevant work. This does not give glasses an exhaustive account-level thread index. |
| Sign in with ChatGPT | Identity sign-in and separately authorized eligible AI inference. | Neither scope gives access to ChatGPT conversations. |
| OpenAI model/conversation APIs | Manage application-supplied conversation state. | Suitable for Rev Eyes-owned chats; no documented mapping from a ChatGPT dot/chat URL to these resources. |
| MCP plugin | ChatGPT/dot invokes tools exposed by the developer's service. | Good for publishing selected cards and results to Rev Eyes; it does not itself let Rev Eyes invoke a dot. |
| Embedded plugin UI | Component-authored follow-up messages; extensions describe current/new conversation targets on supported hosts. | Potential foreground experiment, not documented arbitrary-thread enumeration or a headless push endpoint. |
| Slack / Teams | The owner can contact their dot using connected messaging channels. | Possible human handoff. Acceptance of messages authored by an API relay is unverified. |
| Android texting | One-to-one RCS contact, including voice messages, in a U.S. Pro beta rollout. | Useful phone fallback; no documented programmatic RCS inbox, thread selector, or response feed for Rev Eyes. |
| Workspace Agents API | Trigger a published workspace agent and track beta run status. | Different product and credentials; not evidence of a personal Dots API. Response content is currently unavailable through this API. |

Evidence for the distinctions:

- [Tasks and memory](https://learn.chatgpt.com/docs/dots/tasks-and-memory):
  task continuity and past-chat search are dot capabilities. Calls use selected
  context, and separate channels do not mirror every message. A returned summary
  must not be represented as a complete transcript.
- [Sign-in quickstart](https://developers.openai.com/siwc/quickstart):
  OpenID Connect with PKCE; identity and inference permissions are separate,
  and neither grants conversation access. Commercial access is a limited trial;
  the open-source/private-client inference route has separate eligibility.
- [Conversation state](https://developers.openai.com/api/docs/guides/conversation-state):
  application-managed history and API conversation state. Authentication to the
  model API is not authorization to a personal dot's history.
- [MCP integration guide](https://developers.openai.com/api/docs/mcp) and
  [plugin authentication](https://developers.openai.com/plugins/build/auth):
  the OpenAI host is the MCP client; Rev Eyes would serve tools and authenticate
  the end user with OAuth 2.1/PKCE, validating issuer, audience, expiry, and scopes.
- [Plugin extensions](https://developers.openai.com/plugins/build/extensions)
  and [UI reference](https://developers.openai.com/plugins/reference): embedded
  UI can request follow-ups. No inspected contract establishes a background
  service sending to an arbitrary existing dot thread.
- [Dot messaging](https://learn.chatgpt.com/docs/dots/channels): calls, Slack,
  Teams, iOS shortcuts, and RCS. Mobile web is unsupported. Texting requires the
  verified owner's phone; texting does not provide phone calls.
- [Dots admin guidance](https://learn.chatgpt.com/docs/enterprise/dots-admin-guide):
  only the owner can direct the dot in Slack; other people's messages do not
  start work. Do not assume a bot token relay is accepted as the owner.
- [Workspace agent triggers](https://developers.openai.com/workspace-agents/trigger-runs)
  and [authentication](https://developers.openai.com/workspace-agents/authentication):
  an admin-enabled, scoped access token targets a published `agtch_…` API channel.
  A caller-defined `conversation_key` continues that agent conversation; it is
  not a selector for an arbitrary existing personal chat. The API returns a
  conversation URL and optional run status, not the agent's answer.

### Android app / APK answer

The official Android application is **ChatGPT, published by OpenAI**, package
`com.openai.chatgpt`, on [Google Play](https://play.google.com/store/apps/details?id=com.openai.chatgpt).
Current dot documentation directs users to the ChatGPT mobile app; I found no
documented separate first-party “Dots APK” integration or standalone APK download
that grants third-party access.

Installing the official app is a supported way for the user to reach their dot.
Possessing an APK is not an OAuth grant, history export, SDK, or thread API.
Android isolates apps' private data by default
([Application Sandbox](https://source.android.com/docs/security/app-sandbox)).

Android documents user-facing messaging intents such as `ACTION_SENDTO` and
`smsto:` ([common intents](https://developer.android.com/guide/components/intents-common#Messaging)).
That supports a possible compose-screen handoff; it does **not** establish RCS
delivery to a dot, background sending, or reading replies. No inspected OpenAI
documentation establishes an Android intent for listing dot tasks or starting
a call in an arbitrary selected thread. Do not invent URI schemes or exported
activities. iOS does have documented “Open your dot” Messaging/Dictation/Call
shortcuts; these target the dot and are not an Android integration contract.

### Authentication and voice implications

Keep Rev Eyes authentication, plugin OAuth authorization to Rev Eyes, and any
OpenAI inference authorization separate. Do not import mobile-app sessions or
reuse another client's credentials.

Sign in with ChatGPT is not a drop-in replacement for this repository's API-key
client. The current open-source preview requires streaming with `store:false`,
forbids `max_output_tokens` and hosted conversation state, and does not support
audio input or transcription through that flow. Rev Eyes would still need its
own history and speech pipeline.
[Preview limitations](https://developers.openai.com/siwc/token-sharing-open-source/preview-limitations).

For a separate API-backed assistant, the
[Realtime API](https://developers.openai.com/api/docs/guides/realtime) supports
speech interaction and tools using WebRTC or WebSockets. This is a supported
voice-building surface, not a call to the user's existing dot. There is no reason
to replace the existing Rev Eyes transcription pipeline just to research Dots.

### Export and import

- **Selected context:** ask the dot to produce short summaries of relevant work
  and transfer only the chosen summaries via a connected tool or user-provided
  file. This is the recommended initial import, not a complete account sync.
- **Codex transcripts:** `/export` saves or copies a Markdown transcript for a
  Codex conversation. Do not extend that guarantee to all dot/Work chats.
  [Developer commands](https://learn.chatgpt.com/docs/developer-commands).
- **Enterprise audit export:** the Compliance API exports supported audit
  records for authorized governance workflows. It is not a general personal
  account integration or a message-send API; validate exact dot coverage with
  the admin reference before claiming completeness.
  [Compliance API](https://learn.chatgpt.com/docs/enterprise/compliance-api).
- **Unknown:** the inspected Dots documentation does not establish the coverage
  or format of dots in a consumer account export. Do not promise that a consumer
  archive includes delegated tasks, private notes, tool state, or attachments.
  Any static import would be a snapshot, not continuation of the source thread.

## Repository findings

These are source observations, not verification of a deployed service:

- `frontend/README.md`, `frontend/package.json`, and `frontend/app.json`: React/
  TypeScript Even G2 client using Even Hub SDK `0.0.16`; distribution is an
  `.ehpk` package. No native Android app module was found in the tracked source.
- `frontend/src/even/runtime.ts`: taps, display state, and glasses microphone
  capture already exist. `frontend/src/shared/api/client.ts` obtains a socket
  ticket and opens `/ws`.
- `backend/main.go:95` and `backend/internal/auth/tickets.go:144`: existing
  application authentication verifies the user's Supabase session, then issues
  a one-use ticket with a one-minute lifetime. The ticket handler resolves a
  recent Rev Eyes session; its request does not select an external thread.
- `frontend/src/features/workspace/workspaceData.ts:175`: conversation titles
  are shortened first user messages; descriptions are shortened last assistant
  replies, not generated semantic summaries. The loader reads up to 50 owned
  Rev Eyes sessions and their transcript previews.
- `backend/main.go:365` and `backend/internal/realtime/text_handler.go:21`:
  typed messages can target an owned Rev Eyes session through
  `POST /workspace/conversations/{session_id}/messages`. This route executes
  the Rev Eyes assistant, not Dots or T3.
- `backend/internal/assistant/openai/responses/client.go`: API-key Responses
  transport, application-supplied history, `store:false`, no ChatGPT account
  history connection. Targeted source searches found no Dots/T3/MCP adapter.

## Proposed architecture, not implemented

Use the existing glasses capture/rendering and backend connection:

1. **Thread cards:** represent each selectable item with provider, account,
   stable external reference, short title, one-sentence description, status,
   update time, and continuation mode. Distinguish `direct`, `open in app`, and
   `queued for dot`; never silently send a Dots request to the Rev Eyes model.
2. **Scoped selection:** bind the chosen card to the wearer and an explicit
   selection generation. A tap begins a manual recording for that selection.
   Keep external provider/thread identity separate from the current Rev Eyes
   session UUID. Ambient speech must not become an external send by default.
3. **Dot → glasses:** a proposed Rev Eyes MCP plugin exposes narrowly scoped
   tools to publish selected cards/results and read explicitly queued user
   requests. The dot invokes them under user instructions. These tools are new
   work, not interfaces that already exist in this repository.
4. **Glasses → dot:** initially show a handoff or “queued” state. The owner
   opens/messages the dot through the official app or available contact method;
   the dot retrieves the request and publishes a result. Scheduled retrieval is
   an experiment with unknown latency/reliability, not instant tap-to-talk.
   MCP alone does not wake the dot.
5. **Result delivery:** correlate each request and response; push received results
   to the glasses through the existing backend connection. Keep “sent”,
   “accepted”, “waiting for approval”, and “completed” distinct. Use an
   idempotency key so retries cannot duplicate external instructions.

The MCP design gives **selected summaries and mediated work**, not arbitrary
live thread browsing. A foreground plugin UI relay may be worth a separate
experiment, but must prove dot-host availability, current-thread identity,
message provenance, response return, and behavior when its component is closed.
Nothing inspected proves it works headlessly or on Android.

## Next steps and acceptance gates

1. Confirm product identity, phone OS, account/workspace type, and meaning of
   “threads.” Ask whether a phone handoff is acceptable or fully glasses-only
   operation is essential.
2. In the owner's official ChatGPT client, manually verify that their dot can
   identify two intended existing tasks, summarize each, and direct a follow-up
   to the chosen task. Confirm the update appears in the original task, not
   merely in a new conversation. This is not yet a glasses integration.
3. If mediated access is acceptable, design the authenticated Rev Eyes MCP
   plugin and test one card plus one explicitly submitted request. Verify the
   account can actually install/use that plugin with its dot. Measure latency
   and behavior after closing the client; mark unsupported transitions visibly.
4. Before promising direct access, require primary documentation or written
   vendor confirmation covering authorization, thread list/read/send, stable
   IDs, reply events, approvals, and mobile/device access. Without that, retain
   the mediated mode or defer native Dots support.
5. If the user accepts a separate assistant, use the existing API architecture
   with opt-in summary import. Evaluate Sign in with ChatGPT eligibility only
   as an optional inference/account-linking project, not a Dots connector.

No changes to the parent thread's T3 implementation are required by this note.
