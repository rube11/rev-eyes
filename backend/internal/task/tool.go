package task

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/rube11/rev-eyes/backend/internal/tool"
)

const parametersSchema = `{
  "type": "object",
  "properties": {
    "title": {
      "type": "string",
      "description": "A short action-oriented reminder title."
    },
    "schedule": {
      "anyOf": [
        {"type": "string"},
        {"type": "null"}
      ],
      "description": "The timing stated by the user, or null when none was stated."
    }
  },
  "required": ["title", "schedule"],
  "additionalProperties": false
}`

var ErrProposerRequired = errors.New("task proposer is required")

type Proposer interface {
	Propose(context.Context, tool.Scope, Proposal) error
}

// Tool creates a pending reminder proposal; it never activates the reminder.
type Tool struct {
	proposer Proposer
}

func NewTool(proposer Proposer) (*Tool, error) {
	if proposer == nil {
		return nil, ErrProposerRequired
	}
	return &Tool{proposer: proposer}, nil
}

func (t *Tool) Spec() tool.Spec {
	return tool.Spec{
		Name:        "propose_task",
		Description: "Propose one reminder when the user implies a concrete future action but has not explicitly asked to create it. The proposal remains inactive until the user confirms it.",
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
		Title    string  `json:"title"`
		Schedule *string `json:"schedule"`
	}
	decoder := json.NewDecoder(bytes.NewReader(arguments))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		return tool.Result{}, fmt.Errorf("decode task proposal: %w", err)
	}

	proposal := Proposal{Title: input.Title}
	if input.Schedule != nil {
		proposal.Schedule = *input.Schedule
	}
	proposal = proposal.normalize()
	if err := proposal.validate(); err != nil {
		return tool.Result{}, err
	}
	if err := t.proposer.Propose(ctx, scope, proposal); err != nil {
		return tool.Result{}, err
	}
	return tool.Result{Content: `{"status":"proposed"}`}, nil
}
