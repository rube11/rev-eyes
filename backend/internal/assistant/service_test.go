package assistant

import (
	"context"
	"errors"
	"testing"

	"github.com/rube11/rev-eyes/backend/internal/tool"
)

type routerFunc func(ctx context.Context, utterance string) (Decision, error)

func (f routerFunc) Route(ctx context.Context, utterance string) (Decision, error) {
	return f(ctx, utterance)
}

type agentFunc func(ctx context.Context, scope tool.Scope, query string) (string, error)

func (f agentFunc) Respond(ctx context.Context, scope tool.Scope, query string) (string, error) {
	return f(ctx, scope, query)
}

func TestHandleUtteranceRespondsWithRoutedQueryAndTrustedScope(t *testing.T) {
	t.Parallel()

	wantScope := tool.Scope{UserID: "user-123", SessionID: "session-456"}
	agentCalled := false
	service, err := NewService(
		routerFunc(func(context.Context, string) (Decision, error) {
			return Decision{
				Action: ActionRespond,
				Query:  "What is nearby?",
			}, nil
		}),
		agentFunc(func(_ context.Context, scope tool.Scope, query string) (string, error) {
			agentCalled = true
			if scope != wantScope {
				t.Fatalf("Respond() scope = %#v, want %#v", scope, wantScope)
			}
			if query != "What is nearby?" {
				t.Fatalf("Respond() query = %q", query)
			}
			return "  There is a cafe nearby.  ", nil
		}),
	)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	outcome, err := service.HandleUtterance(
		context.Background(),
		wantScope,
		"what's around here",
	)
	if err != nil {
		t.Fatalf("HandleUtterance() error = %v", err)
	}
	if !agentCalled {
		t.Fatal("Respond() was not called")
	}
	if outcome.Decision.Action != ActionRespond {
		t.Fatalf("outcome action = %q", outcome.Decision.Action)
	}
	if outcome.Response != "There is a cafe nearby." {
		t.Fatalf("outcome response = %q", outcome.Response)
	}
}

func TestHandleUtteranceDoesNotCallAgentForNonResponseAction(t *testing.T) {
	t.Parallel()

	service, err := NewService(
		routerFunc(func(context.Context, string) (Decision, error) {
			return Decision{Action: ActionStateUpdate}, nil
		}),
		agentFunc(func(context.Context, tool.Scope, string) (string, error) {
			t.Fatal("Respond() called for state update")
			return "", nil
		}),
	)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	outcome, err := service.HandleUtterance(context.Background(), tool.Scope{}, "I'm at work")
	if err != nil {
		t.Fatalf("HandleUtterance() error = %v", err)
	}
	if outcome.Decision.Action != ActionStateUpdate {
		t.Fatalf("outcome action = %q", outcome.Decision.Action)
	}
	if outcome.Response != "" {
		t.Fatalf("outcome response = %q, want empty", outcome.Response)
	}
}

func TestHandleUtteranceUsesOriginalUtteranceWhenQueryIsEmpty(t *testing.T) {
	t.Parallel()

	service, err := NewService(
		routerFunc(func(context.Context, string) (Decision, error) {
			return Decision{Action: ActionRespond}, nil
		}),
		agentFunc(func(_ context.Context, _ tool.Scope, query string) (string, error) {
			if query != "what time is it" {
				t.Fatalf("Respond() query = %q", query)
			}
			return "It is noon.", nil
		}),
	)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	if _, err := service.HandleUtterance(
		context.Background(),
		tool.Scope{},
		"  what time is it  ",
	); err != nil {
		t.Fatalf("HandleUtterance() error = %v", err)
	}
}

func TestHandleUtteranceWrapsDependencyErrors(t *testing.T) {
	t.Parallel()

	routeErr := errors.New("route failed")
	service, err := NewService(
		routerFunc(func(context.Context, string) (Decision, error) {
			return Decision{}, routeErr
		}),
		agentFunc(func(context.Context, tool.Scope, string) (string, error) {
			return "", nil
		}),
	)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	if _, err := service.HandleUtterance(context.Background(), tool.Scope{}, "hello"); !errors.Is(err, routeErr) {
		t.Fatalf("HandleUtterance() error = %v, want wrapped route error", err)
	}

	agentErr := errors.New("agent failed")
	service, err = NewService(
		routerFunc(func(context.Context, string) (Decision, error) {
			return Decision{Action: ActionRespond, Query: "hello"}, nil
		}),
		agentFunc(func(context.Context, tool.Scope, string) (string, error) {
			return "", agentErr
		}),
	)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	if _, err := service.HandleUtterance(context.Background(), tool.Scope{}, "hello"); !errors.Is(err, agentErr) {
		t.Fatalf("HandleUtterance() error = %v, want wrapped agent error", err)
	}
}

func TestNewServiceRequiresDependencies(t *testing.T) {
	t.Parallel()

	agent := agentFunc(func(context.Context, tool.Scope, string) (string, error) {
		return "", nil
	})
	activityRouter := routerFunc(func(context.Context, string) (Decision, error) {
		return Decision{}, nil
	})

	if _, err := NewService(nil, agent); !errors.Is(err, ErrRouterRequired) {
		t.Fatalf("NewService(nil, agent) error = %v", err)
	}
	if _, err := NewService(activityRouter, nil); !errors.Is(err, ErrAgentRequired) {
		t.Fatalf("NewService(router, nil) error = %v", err)
	}
}
