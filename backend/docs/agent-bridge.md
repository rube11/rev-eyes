# T3 threads on Rev Eyes

Rev Eyes can list T3 threads, select one, display its conversation, and send
explicit typed or tap-to-talk messages to that exact thread. T3 continues the
work with its existing project, model, tools, and permission mode. No audio is
sent to T3; the existing Deepgram transcriber produces the message text.

This implementation is opt-in. Authenticated thread listing was verified against
a live T3 Code 0.1.5 environment over a private Tailscale connection on 2026-10-31.
Application deployment, live voice/send verification, self-service OAuth
enrollment/refresh, and hardware testing remain separate work.

## Interaction

On the phone or web, open **Agents**. Cards show a compact task title, status,
and update time. Click a thread to select it on the glasses and open its
conversation. Tap the glasses to speak, then pause or tap again to submit. The
phone also has a text composer. Load more threads or messages explicitly.

On the glasses:

1. From the idle Eyes screen, scroll to open the thread picker. Background
   microphone capture stops while you browse.
2. Scroll through title/status cards, then tap to select a thread. A second tap
   starts the microphone for that thread only.
3. Speech is submitted once at the first transcription endpoint. T3 receives
   a **queued new turn**, not a steer/restart. A receipt says queued, never
   completed. The mic does not reopen automatically after a T3 receipt.
4. Scroll through the receipt/reply. Scrolling forward past its final page
   explicitly reads the next conversation page. Scroll again to check for new
   messages. Reads are manual; there is no background polling or completion alert.
5. Hold while idle to return to Eyes. Hold in the picker to dismiss it. Double
   tap retains the app's exit behavior. Ordinary Eyes answers still support their
   existing paginated reading and hands-free follow-ups.

Thread titles are the compact descriptions available from T3's list response;
Rev Eyes does not generate semantic summaries or infer completion from titles.
Conversation reads begin at the first page, can advance with the provider's
position cursor, and shorten long messages to 2,000 characters with a visible
notice. The native reader advances its cursor in memory. It is a lightweight
conversation viewer, not a full mirrored T3 transcript.

## Configuration and authorization

Leave the three connection values empty to disable this integration:

```dotenv
AGENT_T3_MCP_URL=
AGENT_T3_MCP_TOKEN=
AGENT_T3_OWNER_USER_ID=
AGENT_T3_CONTROL_ENABLED=false
```

Supply a previously authorized, reachable **HTTPS `/mcp`** endpoint, its
server-side OAuth access token, and the exact Rev Eyes user ID that owns the
connection. Partial connection configuration fails startup. Tokens never go to
the browser, glasses, prompts, URLs, or returned errors. A token is operator
configuration for one owner; this is not self-service multi-user enrollment.

`AGENT_T3_CONTROL_ENABLED=false` permits status only. Set it to `true` to allow
explicit UI conversation reads and messages. The token's independent T3 ceiling
must allow messaging the chosen thread. T3 enforces that ceiling; Rev Eyes does
not raise a thread's permissions, change models, or answer permission requests.
An expired/revoked credential produces an unavailable connection.

The public remote route is **T3 Connect**, documented in T3's
[outside-agent guide](https://github.com/pingdotgg/t3code/blob/main/docs/user/outside-agents.md).
In T3, use **Settings → Environments → Copy MCP URL**. Hosted Rev Eyes needs a
route reachable from its server. A private Tailscale HTTPS endpoint also works
when the hosted API backend is an approved member of that tailnet and its access
policy permits the connection. Tailscale membership and T3 OAuth are separate
authorizations. Its localhost is not your laptop. Do not expose
a raw local T3 port or reuse MCP tokens against internal RPC. The library-only
`NewLoopback` constructor remains for same-machine adapters; backend environment
configuration requires HTTPS.

T3's documented OAuth flow discovers authorization metadata, opens an approval
page, and lets the owner choose an access ceiling. Rev Eyes does not yet
implement that enrollment or token refresh. A production setup needs a dedicated
registered client, PKCE/state validation, secure credential persistence,
revocation handling, and audience/version validation. Do not borrow another
client's credentials to skip enrollment.

## API and execution boundary

All HTTP workspace routes require the existing verified bearer identity. The
backend derives ownership from authentication, never body/query owner fields.
The existing origin policy wraps these endpoints; responses are `no-store`.

- `GET /workspace/agents`: first 20 threads per configured connection.
- `GET /workspace/agents?connection_id=…&cursor=…`: subsequent bounded page.
- `POST /workspace/agents/thread`: exact `connection_id`, `thread_id`, and optional
  nonnegative `after_position`; bounded conversation page.
- `POST /workspace/agents/messages`: exact `connection_id`, `thread_id`, `text`,
  and `request_id`; forwards a queued message and returns T3's correlated receipt.
- `GET /agents/voice`: consumes the existing one-use WebSocket ticket and binds
  connection/thread IDs for that socket's entire lifetime. Each tap starts one
  manual transcription. Ambient/candidate handlers are absent. Changing targets
  tears down the old runtime before a new socket is adopted.

`list_agent_threads` remains the only T3 model tool. It is side-effect free and
first-page-only. Explicit actions are outside the Jev/model tool registry, so
background speech, memory review, and provider text cannot acquire send authority.
Context-only/other-speaker speech is rejected by the voice handler. Device speaker
labels are still disabled, so this is explicit tap intent, not biometric identity.

`Capabilities.Open` is separate from read-only `Read`. Upstream `t3_thread_read`
can acknowledge delivery of child results. It is invoked only by a user's
selection/refresh/scroll, or refresh following their send. It is never exposed as
a read-only model tool. Provider strings are untrusted display data, not HTML,
commands, endpoint configuration, or permission grants.

The MCP client refreshes discovery for every invocation, validates the operation's
required fields/types, and requires `clientRequestId` plus `mode: queue` support
before sending. It supports bounded JSON/SSE responses, correlates RPC IDs, rejects
redirects, and sanitizes errors. It offers MCP `2025-06-18`, accepts that version
or legacy `2025-03-26`, and uses the negotiated version in every subsequent
request and session-close header. Gateway reads have a three-second deadline;
control calls have a six-second deadline with a five-second MCP deadline.

## Delivery and recovery

There is no Rev Eyes background command queue or promise of exactly-once delivery.
The foreground request waits only for T3's acceptance receipt; T3 owns the actual
run. A receipt must match the selected thread and contain message/run IDs plus a
started/queued delivery state. It does not mean the agent finished.

Typed sends use a client request ID scoped by the backend to owner/connection.
A manual retry of the same unchanged draft while the view remains mounted reuses
the key. T3's idempotency support is required. After dispatch, transport failure,
malformed responses, mismatched receipts, and timeouts become **delivery unknown**.
There are no automatic send retries. Navigation/reload loses the in-memory typed
retry key; check the T3 conversation before repeating a message. Voice commands
have a fresh key per explicit tap and are never replayed on reconnect.

Disconnecting is not cancellation of work already accepted by T3. The glasses
warn about uncertain delivery after an interrupted turn. Do not claim success or
failure without a correlated receipt. Provider approval prompts remain in T3.

## Remaining work

The implemented slice continues existing threads. Full T3 UI parity still needs
project selection/new-thread launch, explicit interruption, permission-request
handoff, attachments/artifacts, live result updates, and richer recent summaries.
Self-service OAuth connection setup is required before this can be offered as an
account-linking feature. Live listing and discovery compatibility are verified for T3 0.1.5; live
message delivery, thread continuation, and permission-ceiling behavior remain
unverified.

For reliable offline/background control, build an authenticated outbound device
bridge and durable command/outbox records. Bind authorization to exact operations,
lease work, reconcile uncertain outcomes using provider request IDs, persist cancel
intent, and deduplicate notifications. Those components and database migrations
are intentionally absent from this foreground implementation.

Dots research is documented separately in
[ChatGPT Dots integration research](chatgpt-dots-integration-research.md). The Dots
placeholder remains disconnected; no public inbound Dots API is assumed.

## Validation

Run backend `go test ./...`, `go vet ./...`, and
`go test -race ./internal/agentbridge/... ./internal/realtime`. Tests cover strict
HTTP arguments, ownership, opaque target IDs, capability defaults, queued mode,
idempotency keys, unknown/mismatched outcomes, and real WebSocket transcription
with a fake provider, including one send per tap and context-only rejection.

Run frontend `pnpm test`, `pnpm lint`, and `pnpm build`. Tests exercise the real
runtime with fake SDK/socket boundaries: picker selection, bound target, ambient
capture stopping, no automatic follow-up mic, reply reading, return to Eyes, and
existing speech/display behavior. Status tests cover pagination, stale/disposed
requests, token changes, and untrusted text. Live authenticated list/discovery was checked separately against T3 0.1.5.
No live message send, physical glasses, self-service OAuth enrollment, or
interactive browser QA is claimed. Production builds retain
the existing bundle-size warning above 500 kB.

Upstream documentation and contracts reviewed 2026-10-30:

- [Outside agents / OAuth MCP](https://github.com/pingdotgg/t3code/blob/main/docs/user/outside-agents.md)
- [Orchestrator schemas](https://github.com/pingdotgg/t3code/blob/main/packages/contracts/src/orchestratorMcp.ts)
- [Tool annotations and semantics](https://github.com/pingdotgg/t3code/blob/main/apps/server/src/mcp/toolkits/orchestrator/tools.ts)
