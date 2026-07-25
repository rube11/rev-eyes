package watch

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/rube11/rev-eyes/backend/internal/tool"
)

type proposerFunc func(context.Context, tool.Scope, Proposal) error

func (f proposerFunc) Propose(ctx context.Context, scope tool.Scope, proposal Proposal) error {
	return f(ctx, scope, proposal)
}

func TestToolCreatesPendingWatchProposal(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.July, 21, 18, 0, 0, 0, time.UTC)
	want := Proposal{
		Query:           "Did Nintendo announce its next console?",
		Condition:       "Nintendo announces its next console",
		IntervalMinutes: 360,
		ExpiresAt:       now.Add(7 * 24 * time.Hour),
	}
	proposalTool, err := NewTool(proposerFunc(func(
		_ context.Context,
		_ tool.Scope,
		proposal Proposal,
	) error {
		if !reflect.DeepEqual(proposal, want) {
			t.Fatalf("proposal = %#v, want %#v", proposal, want)
		}
		return nil
	}))
	if err != nil {
		t.Fatalf("NewTool() error = %v", err)
	}
	proposalTool.now = func() time.Time { return now }

	result, err := proposalTool.Execute(
		context.Background(),
		tool.Scope{},
		json.RawMessage(`{
			"query":" Did Nintendo announce its next console? ",
			"condition":" Nintendo announces its next console ",
			"interval_minutes":360,
			"expires_at":"2026-07-28T11:00:00-07:00"
		}`),
	)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Content != `{"status":"proposed"}` || proposalTool.Spec().Name != "propose_watch" {
		t.Fatalf("result = %q, spec = %#v", result.Content, proposalTool.Spec())
	}
}

func TestToolRejectsUnsafeSchedule(t *testing.T) {
	t.Parallel()

	proposalTool, _ := NewTool(proposerFunc(func(context.Context, tool.Scope, Proposal) error {
		t.Fatal("Propose() was called")
		return nil
	}))
	proposalTool.now = func() time.Time {
		return time.Date(2026, time.July, 21, 0, 0, 0, 0, time.UTC)
	}

	for _, arguments := range []string{
		`{"query":"news","condition":"an update","interval_minutes":1,"expires_at":"2026-07-22T00:00:00Z"}`,
		`{"query":"news","condition":"an update","interval_minutes":60,"expires_at":"2026-09-01T00:00:00Z"}`,
		`{"query":"news","condition":"an update","interval_minutes":60,"expires_at":"invalid"}`,
		`{"query":"news","condition":"an update","interval_minutes":60,"expires_at":"2026-07-22T00:00:00Z","extra":true}`,
	} {
		if _, err := proposalTool.Execute(context.Background(), tool.Scope{}, json.RawMessage(arguments)); err == nil {
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
