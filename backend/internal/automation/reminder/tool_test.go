package reminder

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/rube11/rev-eyes/backend/internal/tool"
)

type proposerFunc func(context.Context, tool.Scope, Proposal) error

func (f proposerFunc) Propose(
	ctx context.Context,
	scope tool.Scope,
	proposal Proposal,
) error {
	return f(ctx, scope, proposal)
}

func TestToolCreatesPendingReminderProposal(t *testing.T) {
	t.Parallel()

	wantScope := tool.Scope{UserID: "user-1", SessionID: "session-1"}
	wantProposal := Proposal{
		Title:    "Call the dentist",
		Schedule: "tomorrow",
		DueAt:    time.Date(2099, time.July, 22, 16, 0, 0, 0, time.UTC),
	}
	proposalTool, err := NewTool(proposerFunc(func(
		_ context.Context,
		scope tool.Scope,
		proposal Proposal,
	) error {
		if scope != wantScope || !reflect.DeepEqual(proposal, wantProposal) {
			t.Fatalf("Propose() scope = %#v, proposal = %#v", scope, proposal)
		}
		return nil
	}))
	if err != nil {
		t.Fatalf("NewTool() error = %v", err)
	}

	result, err := proposalTool.Execute(
		context.Background(),
		wantScope,
		json.RawMessage(`{"title":" Call the dentist ","schedule":" tomorrow ","due_at":"2099-07-22T09:00:00-07:00"}`),
	)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Content != `{"status":"proposed"}` {
		t.Fatalf("result = %q", result.Content)
	}
	if proposalTool.Spec().Name != "propose_task" || proposalTool.Spec().ReadOnly {
		t.Fatalf("spec = %#v", proposalTool.Spec())
	}
	if !strings.Contains(proposalTool.Spec().Description, "explicitly requests one") {
		t.Fatalf("description = %q", proposalTool.Spec().Description)
	}
}

func TestToolRejectsInvalidArguments(t *testing.T) {
	t.Parallel()

	proposalTool, err := NewTool(proposerFunc(func(
		context.Context,
		tool.Scope,
		Proposal,
	) error {
		t.Fatal("Propose() was called")
		return nil
	}))
	if err != nil {
		t.Fatalf("NewTool() error = %v", err)
	}

	for _, arguments := range []string{
		`{"title":"","schedule":"tomorrow","due_at":"2099-07-22T09:00:00-07:00"}`,
		`{"title":"Call","schedule":"tomorrow","due_at":"not-a-time"}`,
		`{"title":"Call","schedule":"tomorrow","due_at":"2020-01-01T09:00:00Z"}`,
		`{"title":"Call","schedule":"tomorrow","due_at":"2099-07-22T09:00:00Z","extra":true}`,
	} {
		if _, err := proposalTool.Execute(
			context.Background(),
			tool.Scope{},
			json.RawMessage(arguments),
		); err == nil {
			t.Fatalf("Execute(%s) error = nil", arguments)
		}
	}
}

func TestNewToolRequiresProposer(t *testing.T) {
	t.Parallel()

	if _, err := NewTool(nil); !errors.Is(err, ErrProposerRequired) {
		t.Fatalf("NewTool(nil) error = %v", err)
	}
}
