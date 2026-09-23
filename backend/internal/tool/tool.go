package tool

import (
	"context"
	"encoding/json"
	"github.com/rube11/rev-eyes/backend/internal/speech"
)

// Tool describes a capability the agent can invoke.
type Tool interface {
	Spec() Spec
	Execute(ctx context.Context, scope Scope, arguments json.RawMessage) (Result, error)
}

// Spec describes a tool to the agent.
type Spec struct {
	Name        string
	Description string
	Parameters  json.RawMessage
	ReadOnly    bool // Safe to execute concurrently with other read-only tools.
}

// Scope contains trusted request information supplied by the backend.
type Scope struct {
	UserID      string
	SessionID   string
	UtteranceID string
	TimeZone    string
	// AlwaysRespond is set by the authenticated text-chat endpoint, never by audio.
	AlwaysRespond bool
	// MemoryReview is set only by the service: answering from stored context,
	// with no agent tools allowed during this turn.
	MemoryReview bool
	// Speech is observed audio attribution, not authentication or speaker identity.
	// Nil identifies intentional text input; audio without labels is Unknown.
	Speech *speech.Utterance
}

// Result is the normalized output returned by a tool.
type Result struct {
	Content string
}
