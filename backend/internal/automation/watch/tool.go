package watch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/rube11/rev-eyes/backend/internal/tool"
)

const parametersSchema = `{
  "type": "object",
  "properties": {
    "query": {
      "type": "string",
      "description": "A precise news search question that should return results only when the condition occurs. Preserve names, dates, and locations."
    },
    "condition": {
      "type": "string",
      "description": "A short user-facing description of the update to watch for."
    },
    "interval_minutes": {
      "type": "integer",
      "minimum": 60,
      "maximum": 1440,
      "description": "How often to check, from hourly to daily."
    },
    "expires_at": {
      "type": "string",
      "description": "When to stop watching, in RFC3339 format and no more than 30 days away."
    }
  },
  "required": ["query", "condition", "interval_minutes", "expires_at"],
  "additionalProperties": false
}`

var ErrProposerRequired = errors.New("watch proposer is required")

type Proposer interface {
	Propose(context.Context, tool.Scope, Proposal) error
}

// Tool creates an inactive watch proposal for explicit confirmation.
type Tool struct {
	proposer Proposer
	now      func() time.Time
}

func NewTool(proposer Proposer) (*Tool, error) {
	if proposer == nil {
		return nil, ErrProposerRequired
	}
	return &Tool{proposer: proposer, now: time.Now}, nil
}

func (t *Tool) Spec() tool.Spec {
	return tool.Spec{
		Name:        "propose_watch",
		Description: "Propose a temporary background watch for a future public update. It remains inactive until the user confirms it.",
		Parameters:  json.RawMessage(parametersSchema),
		ReadOnly:    false,
	}
}

func (t *Tool) Execute(
	ctx context.Context,
	scope tool.Scope,
	arguments json.RawMessage,
) (tool.Result, error) {
	var input struct {
		Query           string `json:"query"`
		Condition       string `json:"condition"`
		IntervalMinutes int    `json:"interval_minutes"`
		ExpiresAt       string `json:"expires_at"`
	}
	decoder := json.NewDecoder(bytes.NewReader(arguments))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		return tool.Result{}, fmt.Errorf("decode watch proposal: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return tool.Result{}, errors.New("decode watch proposal: expected one JSON object")
	}

	expiresAt, err := time.Parse(time.RFC3339, strings.TrimSpace(input.ExpiresAt))
	if err != nil {
		return tool.Result{}, fmt.Errorf("parse watch expiration: %w", err)
	}
	proposal := Proposal{
		Query:           input.Query,
		Condition:       input.Condition,
		IntervalMinutes: input.IntervalMinutes,
		ExpiresAt:       expiresAt,
	}.normalize()
	if err := proposal.validate(); err != nil {
		return tool.Result{}, err
	}

	now := t.now().UTC()
	if !proposal.ExpiresAt.After(now) || proposal.ExpiresAt.After(now.Add(maxWatchDuration)) {
		return tool.Result{}, fmt.Errorf("%w: expiration must be within the next 30 days", ErrProposalInvalid)
	}
	if err := t.proposer.Propose(ctx, scope, proposal); err != nil {
		return tool.Result{}, err
	}
	return tool.Result{Content: `{"status":"proposed"}`}, nil
}
