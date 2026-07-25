package tool

import (
	"context"
	"encoding/json"
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
}

// Result is the normalized output returned by a tool.
type Result struct {
	Content string
}
