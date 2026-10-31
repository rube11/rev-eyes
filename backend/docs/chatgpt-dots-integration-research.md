# Reach the existing ChatGPT dot from Rev Eyes

Updated **2026-10-30**. Product confirmed by the user: their existing OpenAI dot
inside ChatGPT. The aim is to reach that dot and its ongoing work. Creating
another assistant is outside the recommendation.

This is a documentation review and a test plan. No live accounts were opened,
messages sent, APKs installed, credentials inspected, or application files
changed. None of the proposed connections has been tested with the user's dot.

## Start here

**First prove one complete exchange through a Rev Eyes plugin, started by the
owner in their existing dot conversation. Then test an open ChatGPT panel to
remove the repeated owner message.**

The first route uses documented features in their documented direction:

1. The owner asks their dot to put two current work items on the glasses.
2. The dot calls a Rev Eyes tool to publish short cards.
3. The owner selects a card and speaks. Rev Eyes saves that request.
4. In the same dot conversation, the owner sends: “Read my next glasses request.”
5. The dot reads the request through a Rev Eyes tool, continues the selected
   work, and calls a tool to send the result back to the glasses.

This preserves the same dot. The extra ChatGPT message is an honest first step,
not the final glasses-only experience. A supported ChatGPT contact method starts
or continues the dot; merely saving a request at Rev Eyes does not.

The tool pattern is documented, but the end-to-end combination remains a test:
we must verify this account's dot can use the custom plugin and act on the right
existing work. [Connect apps to your dot](https://learn.chatgpt.com/docs/dots/computers-and-apps),
[MCP integration](https://developers.openai.com/api/docs/mcp).

## What the other threads establish

Read the parent and glasses UI briefs; did not operate their accounts or alter
their files:

- Parent: <t3-thread://v2/6f8d42dc-1900-4020-b1db-8a8d095811a8/28c34646-5fa0-4296-8471-cad4682cef26>
- Glasses UI: <t3-thread://v2/6f8d42dc-1900-4020-b1db-8a8d095811a8/mcp:launch:5190e259fff7b55824045dc7764de83dbb712afb0626c3410c7663325535ca25>

The parent reports a verified T3 connection; release and physical glasses checks
remain separate. Its flow is glasses → phone → Rev Eyes backend → T3. The UI
thread owns the picker, gestures, and short status text. Dots research should
supply accurate connection states without changing either thread's application
work.

Read-only source check of `feature/agent-bridge` at
`3a75a5f67b27337f269d139562a0adf3c9eb984d`:

- `backend/internal/agentbridge/gateway.go` has an intentionally disabled `Dot`
  provider. Do not simply turn on its list/read/send flags.
- `backend/internal/agentbridge/voice.go` binds a voice connection to a chosen
  target, but its send result text is T3-specific. Saving a Dots request would
  not satisfy the same delivery promise.
- `frontend/src/features/workspace/agentStatus.ts` expects provider capabilities
  and thread IDs. Dot-published cards would be selected work references, not an
  automatically discovered ChatGPT account thread list.

The research checkout remains at
`f6af7b930672a801951e747b868d4c1fc367a4f4`. Its existing microphone, transcript,
rendering, and socket code can support a later test. No new phone app, model
service, or scheduler is needed to test the first exchange.

## Route 1: the dot reads a glasses request

**Direction:** dot → Rev Eyes tools, with tool results returning to the dot;
Rev Eyes backend → glasses for cards and replies. The owner → dot message starts
the exchange. Rev Eyes has no inbound dot API in this route.

**Identity and consent:** the owner installs the Rev Eyes plugin in the ChatGPT
account that owns the dot, then signs in to their Rev Eyes account through the
plugin's OAuth flow. That account must be the one used by the glasses. Permit
only publishing cards/replies and reading deliberately recorded requests. No
ChatGPT password or mobile-app session is transferred. The server checks token
issuer, audience, expiry, and scope on every call.
[Plugin authentication](https://developers.openai.com/plugins/build/auth).

**Wake and continuity:** the owner's message reaches the existing dot. Tool
calls alone cannot wake it. The dot can follow up on work it created and can
continue existing local Codex work where supported. This is dot-mediated task
continuation, not an API append to every kind of ChatGPT thread.
[Tasks and memory](https://learn.chatgpt.com/docs/dots/tasks-and-memory).

**Smallest tool set, proposed:**

- `publish_cards`: save a title, one-sentence description, and work reference.
- `read_request`: return one explicitly recorded request and its card reference.
- `publish_reply`: save a result for that request, including any approval needed.

These names are a proposed contract, not existing repo endpoints. Keep one
pending request initially. Store the request before showing “Saved”; keep it
until a matching result or explicit cancellation. Reading is repeatable and
must not silently delete it. Return readable request content to the model, not
only hidden component metadata.

**Owner does:** open their dot, connect the plugin once, choose the work to show,
and send “Read my next glasses request” after recording a message. Use a text
message for the first test; plugin access during a voice call is a separate
check.

**First blockers:** custom plugin access in this dot; tool selection; correct
work continuation; result publication. These are measurable in one exchange.

## Route 2: an open panel posts the follow-up

**Direction:** glasses → Rev Eyes request store → an open component inside
ChatGPT → ChatGPT host → its current conversation. The dot replies through the
same Rev Eyes tools as Route 1.

OpenAI documents `ui/message` for follow-up messages, with the compatibility
method `window.openai.sendFollowUpMessage({ prompt, scrollToBottom })`.
Components run inside ChatGPT and may contact declared backend domains. They
are not standalone web pages with account access.
[Component guide](https://developers.openai.com/plugins/build/chatgpt-ui),
[bridge reference](https://developers.openai.com/plugins/reference).

**Identity and consent:** open the component from a tool result in the owner's
existing dot conversation. Link it to the same Rev Eyes account. The owner
explicitly enables “Use glasses here.” Pin that connection; a panel opened in
another chat must not inherit it. Use a short-lived component session, not a
long-lived token in browser storage.

**Wake and continuity:** documented follow-up messaging can post to the current
conversation. Its availability inside a dot conversation, and whether a message
received from the glasses may be submitted without a fresh click in the panel,
are unverified. Do not open a new ordinary Work chat as a substitute.

**Tiny test before building more:**

1. From the existing dot, open a component with a single “Send test” button.
2. Click it to send “Read my next glasses request.” Verify the message and tool
   call appear in that same dot conversation.
3. If that works, enable one explicitly consented pending request from glasses.
   Have the component fetch it once and use the same follow-up method.
4. Close the component, switch chats, and lock the device. It must stop claiming
   to be ready. Test reconnection with the same request without a duplicate send.

The host's follow-up acknowledgement is not the dot's completed answer. Wait
for `publish_reply` to show the result. If remote submission is blocked, retain
a single owner click in ChatGPT; do not replace it with screen automation.

`_meta["openai/session"]` is documented only as an anonymized conversation
correlation value. It is not a thread URL, OAuth credential, or API target.
The extension overview also limits message targets to current/new conversations;
it does not provide arbitrary existing-thread routing.
[Plugin extensions](https://developers.openai.com/plugins/build/extensions).

## Route 3: the owner's messaging channel

**Documented human route:** the owner connects Slack or Teams in their dot's
profile and messages that dot. This reaches the same dot; the channel history
is separate from the ChatGPT conversation. The owner can ask it to fetch the
next glasses request. No second assistant is involved.
[Message your dot](https://learn.chatgpt.com/docs/dots/channels).

**Slack is a concrete automation candidate, not yet proven:**

- Direction: glasses request → Rev Eyes backend → Slack `chat.postMessage` →
  existing owner–dot DM → dot → Rev Eyes reply tool.
- Consent: the owner authorizes a Slack app using user OAuth with `chat:write`,
  in the workspace connected to their dot. Pin the chosen DM. Use a user token,
  not a bot token or a changed display name. Slack documents posting as the
  authorizing user; bot tokens post as bots.
  [Slack tokens](https://docs.slack.dev/authentication/tokens/),
  [chat.postMessage](https://docs.slack.dev/reference/methods/chat.postMessage/).
- Wake: OpenAI says only the owner can direct their dot in Slack. It does not
  establish that API-posted owner messages trigger it. This is the precise
  remaining compatibility test.
  [Dots permissions](https://learn.chatgpt.com/docs/enterprise/dots-admin-guide).
- Test later: compare a manually typed owner message with one consented
  user-token message in the same DM. Require the same dot to fetch the stored
  request and publish a reply. Slack accepting a post is not a passing result.
- Avoid extra inbox permissions initially: return results through the Rev Eyes
  tool. If reading Slack replies is later necessary, DM history requires the
  applicable user history scope, such as `im:history`; threaded replies need
  their own retrieval check. Do not scrape notifications.
  [Conversation history](https://docs.slack.dev/reference/methods/conversations.history/).

If this passes, it could remove the always-open panel. Teams is a supported
owner contact method, but no automated Teams relay is established by this review.
Do not add both platforms before proving either.

## Route 4: official phone handoff

The owner can open ChatGPT, open their dot, and send the retrieval message or
start a call. On supported iPhones, the documented “Open your dot” shortcut has
Messaging, Dictation, and Call modes and uses the signed-in dot account. These
actions target the dot, not an arbitrary delegated task. Keep task selection in
the stored glasses request.

For eligible U.S. Pro users, verified-number iMessage/RCS texting is another
owner route. Android requires RCS enabled. No programmatic personal RCS
send/read contract is established here. Mobile web is unsupported.
[Official mobile actions](https://learn.chatgpt.com/docs/dots/channels).

Android's `ACTION_SENDTO`/`smsto:` can open a messaging composer; that is not a
guarantee of RCS delivery or reading the answer.
[Android intents](https://developer.android.com/guide/components/intents-common#Messaging).
No documented Android equivalent of the dot-specific iOS shortcut was found.
Use a visible “Open ChatGPT” instruction until a real supported handoff is
verified. The Even Hub client must not be assumed able to launch arbitrary
native actions.

The official [ChatGPT Android app](https://play.google.com/store/apps/details?id=com.openai.chatgpt)
is a user access path. An APK supplies no additional supported permissions for
Rev Eyes. No APK installation or inspection is needed for these tests.

## Minimal glasses flow to hand to the UI thread

No UI code changes are proposed in this note. Keep the existing gesture scheme.

- One card: work title, one short description, and its current state.
- Select → tap to speak → show the dictated request for that work.
- Use **“Saved · Open ChatGPT”** when only Rev Eyes has stored it.
- Use **“Sent”** only after the chosen contact path accepts the message;
  **“Working”** only after the dot reports that state.
- Show the reply when the dot publishes it. Use **“Open ChatGPT”** for approvals
  and **“Couldn’t send”** for a confirmed failure. Uncertain delivery needs a
  check before retrying.
- Always provide the existing way back to **Eyes**. Do not silently route a dot
  request to the Eyes model, another account, or a new conversation.

Cards should come from work the dot can identify and continue. Begin with two
chosen items, not “all your chats.” Keep the card reference, owner, request ID,
and selected work bound together through reconnects. Never infer completion
from a chat title, an accepted post, or a stale summary.

## Test order and release gate

1. **Owner + tools:** prove one recorded request and one published answer through
   the original dot. Check the original delegated task if the request changes it.
2. **Open panel:** prove a button follow-up in that same dot, then a request
   arriving from glasses with explicit consent. Stop at the first unsupported
   step; keep the owner action visible.
3. **Optional Slack:** only if the owner already wants that channel, test a
   properly authorized user message. No account access or sending is authorized
   by this research task.
4. **Hardware:** select item A, dictate, and verify item B is untouched. Reconnect
   mid-request, retry once, and require one delivered instruction and one reply.
   Check phone lock and expired sign-in. Run no assistant model in place of dot.

The first success is **one request reaching the existing dot and one answer
returning to the glasses**. A full thread browser and instant unattended wake-up
are not prerequisites for that test. They remain separate promises to prove.

Still needed before a live test: phone OS, availability of a custom plugin in
this dot, two work items the owner wants exposed, and consent for that specific
test. Nothing in this note authorizes sending now.

## Boundaries from the first review

- Sign in with ChatGPT supplies identity and optional eligible inference, not
  access to existing ChatGPT conversations. It is not needed to start this path.
  [Sign-in scopes](https://developers.openai.com/siwc/quickstart).
- API Conversations and Realtime build application-owned assistants. They do
  not establish access to this dot and are not a substitute for it.
  [Conversation state](https://developers.openai.com/api/docs/guides/conversation-state).
- Workspace Agents API triggers published workspace agents, not a documented
  personal-dot endpoint; response content cannot currently be retrieved there.
  [Workspace agent triggers](https://developers.openai.com/workspace-agents/trigger-runs).
- Codex `/export` and Enterprise compliance exports are limited transcript/audit
  paths, not interactive dot connections. Dots coverage in a consumer archive
  was not established. Export is not required for this test.
  [Codex export](https://learn.chatgpt.com/docs/developer-commands),
  [Compliance API](https://learn.chatgpt.com/docs/enterprise/compliance-api).
- A dot can act on supported service events or saved schedules, but installing
  a custom tool does not establish a new event subscription or reliable
  immediate wake-up. Do not start with a scheduler or an endless tool call.
  [Tasks and events](https://learn.chatgpt.com/docs/dots/tasks-and-memory).
