package reminder

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

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
      "type": "string",
      "description": "The timing stated by the user."
    },
    "due_at": {
      "type": "string",
      "description": "The resolved future time in RFC3339 format."
    }
  },
  "required": ["title", "schedule", "due_at"],
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
		Description: "Propose one timed reminder when the user explicitly requests one or implies a concrete future action with usable timing. The proposal remains inactive until the user confirms it.",
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
		Title    string `json:"title"`
		Schedule string `json:"schedule"`
		DueAt    string `json:"due_at"`
	}
	decoder := json.NewDecoder(bytes.NewReader(arguments))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		return tool.Result{}, fmt.Errorf("decode task proposal: %w", err)
	}

	dueAt, err := time.Parse(time.RFC3339, strings.TrimSpace(input.DueAt))
	if err != nil {
		return tool.Result{}, fmt.Errorf("parse task due time: %w", err)
	}
	proposal := Proposal{Title: input.Title, Schedule: input.Schedule, DueAt: dueAt}
	proposal = proposal.normalize()
	if err := proposal.validate(); err != nil {
		return tool.Result{}, err
	}
	if !proposal.DueAt.After(time.Now().UTC()) {
		return tool.Result{}, fmt.Errorf("%w: due time must be in the future", ErrProposalInvalid)
	}
	if err := t.proposer.Propose(ctx, scope, proposal); err != nil {
		return tool.Result{}, err
	}
	return tool.Result{Content: `{"status":"proposed"}`}, nil
}
