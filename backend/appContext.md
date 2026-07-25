# Even G2 Real-Time Agent Architecture

## Overview

The system continuously streams audio from the Even G2 glasses to a Go backend. Deepgram handles real-time transcription and detects when an utterance is complete. Completed utterances are routed through a cheap classifier, and only actionable requests wake the main tool-calling agent.

```text
Even G2 Audio
    ↓
WebSocket to Go Backend
    ↓
Deepgram Streaming STT
    ↓
Final Utterance / End of Turn
    ↓
Rule-Based Filters
    ↓
Cheap Activity Router
    ├── ignore
    ├── context only
    ├── remember
    └── respond
             ↓
      Main Tool-Calling Agent
             ↓
      Context + Action Tools
             ↓
      Final Glasses Response
```

## Main Components

### Audio Layer

* Maintains the continuous WebSocket connection from the glasses
* Streams PCM audio to Deepgram
* Receives partial and final transcripts
* Uses partial transcripts for live captions only
* Sends finalized utterances into the agent pipeline

### Activity Router

A small, inexpensive model decides whether the assistant should act.

Possible outputs:

```json
{
  "action": "ignore | context | remember | respond",
  "confidence": 0.95,
  "query": "Cleaned standalone user request"
}
```

The router should receive only:

* Current finalized transcript
* A very small amount of recent context
* Minimal routing instructions

It should not receive the full user profile or long-term memories.

### Main Agent

The main agent decides:

* Whether it can answer directly
* Which context it needs
* Which external tools it needs
* Whether multiple tools can run together
* Whether another tool round is necessary
* What final response should appear on the glasses

The agent runs in a loop until it produces a final response or reaches a maximum number of tool rounds.

## Context Layers

### Always Included

* System prompt
* Small core user profile
* Current request
* Recent session context

### Retrieved When Needed

* Detailed user profile sections
* Long-term vector memories
* Information about specific people
* Previous decisions or events
* Project-specific context
* Older conversation history

## Context Tools

Context retrieval is treated like normal tool calling.

Examples:

```text
get_user_profile
get_profile_section
search_memories
get_person_memories
get_recent_context
get_current_location
get_project_context
```

## Action Tools

Examples:

```text
web_search
search_places
get_weather
check_calendar
create_note
send_message
recommend_restaurants
```

All tools use a common interface:

```go
type Tool interface {
    Name() string
    Description() string
    Execute(
        ctx context.Context,
        arguments json.RawMessage,
    ) (ToolResult, error)
}
```

## Multiple Tool Calls

Independent tools run concurrently using goroutines.

Example:

```text
Agent requests:
    ├── get_user_profile
    ├── search_memories
    ├── get_current_location
    └── check_calendar

Go executor runs them concurrently.

All results are returned to the model together.
```

Dependent tools run in separate rounds.

Example:

```text
Round 1:
Retrieve food preferences and current location

Round 2:
Search restaurants using those results

Round 3:
Rank results and return recommendations
```

Use `errgroup` with a concurrency limit:

```go
group, ctx := errgroup.WithContext(ctx)
group.SetLimit(6)
```

## Memory Architecture

Long-term memory should use structured records plus vector search.

Memory types may include:

```text
fact
preference
person
event
decision
task
project
```

A memory record should include:

```go
type Memory struct {
    ID         string
    UserID     string
    Text       string
    Type       string
    Importance float64
    Confidence float64
    CreatedAt  time.Time
    LastUsedAt time.Time
    Source     string
    Embedding  []float32
}
```

Vector retrieval should consider:

```text
semantic similarity
recency
importance
confidence
```

Only the most relevant memories should be inserted into the main agent prompt.
