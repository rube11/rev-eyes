# Persistent Memory Architecture

## Goal

Give the assistant useful continuity across conversations without treating every
WebSocket connection as a new conversation or placing an entire transcript into
every model prompt.

The system uses three separate forms of context:

1. **Session context** for the current conversation.
2. **Long-term memories** for durable facts, preferences, relationships, events,
   and decisions.
3. **Raw transcripts** as the searchable source of truth when a summary or
   extracted memory is incomplete.

## Core Principle

Extracted memories help the assistant locate information. The original
transcript verifies what was actually said.

```text
User
  |-- Sessions
  |    |-- WebSocket connections
  |    |-- Transcript utterances
  |    `-- Session summaries
  `-- Long-term memories
       `-- Source utterance references
```

## Sessions and WebSocket Connections

A WebSocket is a temporary transport connection. It does not define the
lifetime of a session.

The client keeps its current session ID and provides it whenever it reconnects.
The server resumes that session if it is still active. A dropped or intentionally
stopped WebSocket does not immediately end the session.

Initial session rules:

- Create a session when streaming begins and there is no resumable session.
- Resume the same session across WebSocket reconnections.
- Update session activity when a finalized utterance is received.
- End a session after approximately 15-30 minutes without a finalized utterance.
- Allow the client to explicitly end a session.
- Apply a maximum session length, initially 8-12 hours.
- Run final session summarization and memory processing when a session ends.

Every finalized utterance must retain trusted scope instead of entering the
pipeline as an unscoped string:

```go
type CompletedUtterance struct {
	UserID    string
	SessionID string
	Text      string
	StartedAt time.Time
	EndedAt   time.Time
}
```

## Transcript Storage

Save every finalized utterance immediately, before memory extraction. A
transcript record should contain at least:

```text
id
user_id
session_id
speaker
text
started_at
ended_at
```

Speaker identity is important for questions such as, "What did my boss say?"
The system will eventually need diarization, known-speaker identification, or
user corrections. Until then, speaker identity may be unknown or inferred with
an explicit confidence value.

## Activity Router Responsibilities

Keep the router's `remember` action, but narrow it to explicit user requests:

```text
"Remember that my boss is Maya."
"Don't forget that I prefer morning meetings."
```

The router should not use `remember` for every statement that might become a
durable memory. Its responsibility is immediate, deliberate capture requested
by the user.

An explicit memory is stored immediately as confirmed and linked to its source
utterance:

```json
{
  "id": "mem_123",
  "type": "person",
  "text": "The user's boss is Maya.",
  "confidence": 1.0,
  "confirmed": true,
  "source_utterance_ids": ["utt_456"]
}
```

The assistant should acknowledge successful explicit memory capture.

## Implicit Memory Extraction

Implicit information does not need to pass through the router as `remember`.
The transcript is saved first, and a background process may extract useful
memories from it.

Run extraction after approximately five minutes of new conversation, after a
natural pause, and when a session ends. Five minutes is a processing trigger,
not a hard session or conversation boundary.

The extraction worker processes only utterances after its stored cursor. It
should produce structured operations rather than blindly inserting records:

```json
{
  "operations": [
    {
      "operation": "create",
      "memory_id": null,
      "type": "work_commitment",
      "text": "The user's boss requested the API deliverable by Wednesday.",
      "people": ["boss"],
      "topics": ["API", "deliverable"],
      "occurred_at": "2026-07-14T09:15:00Z",
      "importance": 0.8,
      "confidence": 0.92,
      "source_utterance_ids": ["utt_123", "utt_124"]
    }
  ]
}
```

Supported operations should eventually include `create`, `update`, `merge`, and
`skip`. Existing memories and source utterance IDs must be considered so that
explicit and implicit processing do not create duplicates.

## Memory Retrieval and Model Context

Memories remain in persistent storage. They are not permanently placed inside
the model. Relevant records are retrieved and temporarily added to the main
agent's context.

Use two retrieval layers:

1. **Core profile:** a small set of stable facts and preferences available on
   most requests.
2. **Relevant retrieval:** memories, session summaries, and transcript excerpts
   searched when the current request refers to prior information.

Example:

```text
Current request:
What did my boss say last week about that deliverable?

Retrieved context:
- The user's boss is Maya. [confirmed]
- Maya requested that the API deliverable be ready Wednesday.
- Original transcript excerpts and timestamps linked to this memory.
```

The main agent should have a `search_memories` context tool. Retrieval should
support:

- Text or keyword search
- Person and topic filters
- Date ranges
- Memory type
- Confidence and importance
- Original transcript lookup through source utterance IDs

Start with PostgreSQL full-text search and structured filters. Add semantic
vector search when testing shows keyword search misses paraphrased or indirect
references. A future hybrid search can rank semantic similarity, keyword match,
recency, importance, and confidence together.

High confidence affects ranking and conflict resolution; it does not mean a
memory should be included in every prompt. Confirmed user memories should take
precedence over weaker inferred memories, while the transcript remains
available for verification.

## Initial Implementation Order

1. Add durable users and sessions.
2. Scope completed utterances with user and session IDs.
3. Persist every finalized transcript utterance.
4. Connect explicit router `remember` decisions to confirmed memory storage.
5. Add `search_memories` over memories and transcripts.
6. Add session summaries and background implicit-memory extraction.
7. Evaluate misses and then add semantic vector search if needed.

This order enables testing persistence and historical recall before introducing
a second LLM processing pipeline.
