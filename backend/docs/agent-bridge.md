# Agent gateway: first increment

This is an **opt-in status integration**, not a paired laptop agent controller.
It preserves Moonshine/Deepgram → Jev routing/tool selection → strict OpenAI
arguments → registered Go tools → final composer. No audio is sent to T3.

## What is implemented

- `agentbridge.Provider`: explicit capabilities and owner-bound connections;
  trusted `tool.Scope.UserID`, never model arguments, selects the owner.
- `list_agent_threads`: the first **20** T3 threads across projects with status
  metadata and the provider's continuation cursor. This is not a full inventory.
  There is no pagination tool yet; a missing thread must not be called nonexistent.
- A bounded MCP Streamable HTTP client (JSON and SSE responses), initialization,
  tools/schema discovery, read-only annotation check, correlation-ID validation,
  no redirects, bounded response bodies and sanitized errors. Each read discovers
  capabilities again; offline/revoked/incompatible providers fail closed.
- Three-second gateway deadline, no long-running provider wait in the foreground,
  no background goroutine pretending to be a durable job, no automatic retries.
- Generic side-effect-free thread-read boundary for future providers. **T3 read
  is deliberately disabled**: current `t3_thread_read` is annotated non-read-only
  and marks a child's final result delivered. Its schema has no opt-out. Do not
  change `ReadOnly` just because an upstream tool is named “read.”
- A capability-disabled `Dot` placeholder. It makes no claim about a public dot
  messaging API and registers no tools.

Only list/status is currently registered for T3. Sends, launch, interruption,
permission answers, result-delivery acknowledgment and shell execution are absent.
No reminder/watch confirmation, speech admission, turn budget, database schema,
websocket protocol or notification behavior has been changed.
Provider text is labeled untrusted data and cannot grant execution authority.

## Workspace status view

The Agents workspace uses authenticated `GET /workspace/agents`, protected by the
existing Supabase verifier and origin policy. It shares the tool gateway instance
and derives ownership only from the verified identity, never query/body fields.
Responses are `Cache-Control: no-store` and contain `{connections, limit: 20}`.
An empty connection list means not configured for this account, including when
the feature is off; it does not reveal other owners’ configuration. Provider
failures remain sanitized per-connection unavailable results. The endpoint
accepts no target URL, credential, owner override, cursor or action.

The view loads on navigation and offers manual refresh, with no agent polling or
notifications. It displays first-page status only, never a full inventory or
proof of completion. Thread content, sending, starting, interruption and Dot
connections remain unsupported. Laptop pairing is future work; no connection
setup form or browser-side provider credential handling is provided.

## Optional direct HTTPS configuration

Leave **all three variables empty** to disable the feature (the default):

```dotenv
AGENT_T3_MCP_URL=
AGENT_T3_MCP_TOKEN=
AGENT_T3_OWNER_USER_ID=
```

An operator may later supply a **previously authorized, reachable HTTPS `/mcp`**
endpoint, its server-side OAuth access token, and the exact Supabase user ID
owning it. Partial configuration fails startup. Tokens must not be sent to the
frontend or placed in prompts, URLs, repository files, or logs. Use the least
privileged/read-only T3 ceiling. OAuth enrollment, refresh and pairing are NOT
implemented; an expired token means unavailable status, not an automatic fallback.
The owner-to-token mapping is trusted operator configuration, not self-service
multi-user enrollment. Never assign another person's T3 credential to a user.

This path is useful only if an authorized secure route already exists (for
example a validated T3 Connect setup); **localhost on hosted Eyes is not your
laptop**. Do not expose a raw local T3 port, disable TLS, reuse `/mcp` credentials
against internal RPC, or invent a tunnel to make this work. The library-only
`NewLoopback` constructor permits literal loopback HTTP for a future same-machine
connector; the backend environment configuration intentionally cannot enable it.
No listeners or connectors are started by this change.

The installed T3 version is unknown. Live compatibility validation is required:
check OAuth audience/ceiling, MCP protocol `2025-03-26`, discovered list schema,
read-only annotation, and response shape with that version before enabling.
Tests use only mocked providers and httptest, never your live laptop.

## Next increment: authenticated outbound laptop bridge (contract only)

The following is **not implemented**, and is the prerequisite for controlling a
laptop from hosted Eyes without a pre-existing reachable secure endpoint:

1. Pair through an authenticated owner UI with short-lived single-use challenge;
   issue a revocable device credential bound to user, connection and allowed
   capabilities. Laptop holds T3 credentials locally. No model-supplied hosts,
   paths, executables, project names-as-paths or tokens.
2. Laptop initiates outbound TLS to a fixed backend endpoint and authenticates.
   Backend derives ownership from authentication, not frame payloads. Both sides
   negotiate a protocol version and capability ceiling. Expiration/revocation
   denies new commands. Disconnect is **not** cancellation.
3. Use a durable command/outbox record containing command ID, owner, connection,
   session/utterance, explicit target, authorized operation, idempotency key,
   payload digest, timestamps, state and remote correlation. Persist before ACK.
   The utterance returns “queued”, not “finished”; a worker owns all long waits.
4. Persist authorization separately and bind it to one exact target/payload.
   Extend or add a feature-specific confirmer rather than reusing reminder/watch
   “yes”. Context-only speech cannot authorize writes. Provider approvals remain
   in T3 UI; `t3_pending_request` questions are not permission approvals.
5. Lease worker commands; deduplicate receipts and reconcile after restart or
   reconnect. Retry reads with bounded backoff. A send/launch timeout after
   dispatch is **unknown**, not retryable: reconcile provider correlation first,
   and request user resolution when no proof is available. Queue send mode
   explicitly; never silently steer/restart. No exactly-once claim without
   provider support.
6. States must distinguish queued, dispatched, running, waiting-for-user,
   completed, failed, cancel-requested, canceled and unknown. Persist cancel
   intent; completion racing with cancel must retain the actual provider outcome.
   Never classify disconnection, an ACK, or a cancel request as completion.
7. Atomically persist terminal/action-needed notification intent with state;
   deduplicate it, correlate connection/thread/command, and reuse notification
   ACK/replay. Routine progress must not interrupt glasses follow-up listening.
   Add a workspace resource/UI only when backed by authenticated persisted data.

Required tests before shipping those effects: exact authorization and ownership,
quick durable ACK independent of foreground turn, restart/lease recovery,
unknown-outcome reconciliation, reconnect/receipt dedup, cancel/completion races,
notification outbox/ACK/replay and provider permission ceilings. Those tests
cannot honestly pass in this increment because the corresponding components do
not exist. No migrations, live pairing, credential setup, remote execution,
publication, or deployment are part of this implementation.

## Sources and checks

Upstream reviewed 2026-10-29; moving `main` is evidence, not an installed-version
pin. Validate against the laptop version before use:

- [T3 external agents / OAuth MCP](https://github.com/pingdotgg/t3code/blob/main/docs/user/outside-agents.md)
- [Orchestrator tools and read delivery semantics](https://github.com/pingdotgg/t3code/blob/main/apps/server/src/mcp/toolkits/orchestrator/tools.ts)
- [Tool contracts / schemas](https://github.com/pingdotgg/t3code/blob/main/packages/contracts/src/orchestratorMcp.ts)
- [T3 authentication boundaries](https://github.com/pingdotgg/t3code/blob/main/docs/internals/environment-auth.md)

Run `go test ./...` and `go vet ./...` from `backend`. Focused package tests also
run with `go test -race ./internal/agentbridge/... ./internal/web`. Run frontend
`pnpm test`, `pnpm lint` and `pnpm build` for the Agents workspace integration.
