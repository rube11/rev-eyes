package proposal

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const (
	workspaceTestUserID     = "4e1c73d9-1641-48c1-8a00-84f73e2297cf"
	workspaceTestResourceID = "28e8ec7c-dda4-4b92-ad47-7307a5294084"
)

type workspaceCommanderStub struct {
	resolve func(context.Context, string, Kind, string, Status) (Resolution, error)
	delete  func(context.Context, string, Kind, string) (bool, error)
}

func (s workspaceCommanderStub) ResolveByID(
	ctx context.Context,
	userID string,
	kind Kind,
	resourceID string,
	status Status,
) (Resolution, error) {
	return s.resolve(ctx, userID, kind, resourceID, status)
}

func (s workspaceCommanderStub) DeleteByID(
	ctx context.Context,
	userID string,
	kind Kind,
	resourceID string,
) (bool, error) {
	return s.delete(ctx, userID, kind, resourceID)
}

func TestWorkspaceHandlerApprovesProposalAndTriggersRegistration(t *testing.T) {
	t.Parallel()

	triggered := false
	handler, err := NewWorkspaceHandler(
		func(_ context.Context, token string) (string, error) {
			if token != "access-token" {
				t.Fatalf("token = %q", token)
			}
			return workspaceTestUserID, nil
		},
		workspaceCommanderStub{
			resolve: func(
				_ context.Context,
				userID string,
				kind Kind,
				resourceID string,
				status Status,
			) (Resolution, error) {
				if userID != workspaceTestUserID ||
					kind != KindReminder ||
					resourceID != workspaceTestResourceID ||
					status != StatusAccepted {
					t.Fatalf(
						"command = %q, %q, %q, %q",
						userID,
						kind,
						resourceID,
						status,
					)
				}
				return Resolution{Kind: kind, Status: status}, nil
			},
			delete: func(context.Context, string, Kind, string) (bool, error) {
				t.Fatal("DeleteByID() was called")
				return false, nil
			},
		},
		func() { triggered = true },
	)
	if err != nil {
		t.Fatalf("NewWorkspaceHandler() error = %v", err)
	}
	var (
		changedUser string
		changedKind Kind
	)
	handler.SetWorkspaceChanged(func(userID string, kind Kind) {
		changedUser = userID
		changedKind = kind
	})

	mux := http.NewServeMux()
	mux.Handle(
		"POST /workspace/automations/{kind}/{resource_id}/decision",
		handler,
	)
	request := httptest.NewRequest(
		http.MethodPost,
		"/workspace/automations/reminder/"+workspaceTestResourceID+"/decision",
		strings.NewReader(`{"decision":"accepted"}`),
	)
	request.Header.Set("Authorization", "Bearer access-token")
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)

	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body = %q", response.Code, response.Body.String())
	}
	if !triggered {
		t.Fatal("schedule registration was not triggered")
	}
	if changedUser != workspaceTestUserID || changedKind != KindReminder {
		t.Fatalf("workspace change = %q, %q", changedUser, changedKind)
	}
}

func TestWorkspaceHandlerDeletesAndTriggersCancellation(t *testing.T) {
	t.Parallel()

	triggered := false
	handler, err := NewWorkspaceHandler(
		func(context.Context, string) (string, error) {
			return workspaceTestUserID, nil
		},
		workspaceCommanderStub{
			resolve: func(
				context.Context,
				string,
				Kind,
				string,
				Status,
			) (Resolution, error) {
				t.Fatal("ResolveByID() was called")
				return Resolution{}, nil
			},
			delete: func(
				_ context.Context,
				userID string,
				kind Kind,
				resourceID string,
			) (bool, error) {
				if userID != workspaceTestUserID ||
					kind != KindWatch ||
					resourceID != workspaceTestResourceID {
					t.Fatalf("command = %q, %q, %q", userID, kind, resourceID)
				}
				return true, nil
			},
		},
		func() { triggered = true },
	)
	if err != nil {
		t.Fatalf("NewWorkspaceHandler() error = %v", err)
	}
	var (
		changedUser string
		changedKind Kind
	)
	handler.SetWorkspaceChanged(func(userID string, kind Kind) {
		changedUser = userID
		changedKind = kind
	})

	mux := http.NewServeMux()
	mux.Handle(
		"DELETE /workspace/automations/{kind}/{resource_id}",
		handler,
	)
	request := httptest.NewRequest(
		http.MethodDelete,
		"/workspace/automations/watch/"+workspaceTestResourceID,
		nil,
	)
	request.Header.Set("Authorization", "Bearer access-token")
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)

	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body = %q", response.Code, response.Body.String())
	}
	if !triggered {
		t.Fatal("schedule cancellation was not triggered")
	}
	if changedUser != workspaceTestUserID || changedKind != KindWatch {
		t.Fatalf("workspace change = %q, %q", changedUser, changedKind)
	}
}

func TestWorkspaceHandlerReturnsStableCommandErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		commandErr error
		wantStatus int
		wantCode   string
	}{
		{"not found", ErrAutomationNotFound, http.StatusNotFound, "not_found"},
		{"past reminder", ErrReminderTimePassed, http.StatusConflict, "reminder_time_passed"},
		{"expired watch", ErrWatchExpired, http.StatusConflict, "watch_expired"},
		{"watch limit", ErrWatchLimitReached, http.StatusConflict, "watch_limit_reached"},
		{"invalid decision", ErrStatusInvalid, http.StatusBadRequest, "invalid_request"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			handler, err := NewWorkspaceHandler(
				func(context.Context, string) (string, error) {
					return workspaceTestUserID, nil
				},
				workspaceCommanderStub{
					resolve: func(
						context.Context,
						string,
						Kind,
						string,
						Status,
					) (Resolution, error) {
						return Resolution{}, test.commandErr
					},
					delete: func(
						context.Context,
						string,
						Kind,
						string,
					) (bool, error) {
						return false, test.commandErr
					},
				},
				func() { t.Fatal("schedule dispatcher was triggered") },
			)
			if err != nil {
				t.Fatalf("NewWorkspaceHandler() error = %v", err)
			}

			request := httptest.NewRequest(
				http.MethodPost,
				"/workspace/automations/reminder/"+workspaceTestResourceID+"/decision",
				strings.NewReader(`{"decision":"accepted"}`),
			)
			request.SetPathValue("kind", "reminder")
			request.SetPathValue("resource_id", workspaceTestResourceID)
			request.Header.Set("Authorization", "Bearer access-token")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)

			if response.Code != test.wantStatus ||
				!strings.Contains(response.Body.String(), test.wantCode) {
				t.Fatalf(
					"response = %d, %q; want %d containing %q",
					response.Code,
					response.Body.String(),
					test.wantStatus,
					test.wantCode,
				)
			}
		})
	}
}

func TestWorkspaceHandlerRequiresBearerAuthentication(t *testing.T) {
	t.Parallel()

	handler, err := NewWorkspaceHandler(
		func(context.Context, string) (string, error) {
			t.Fatal("token verifier was called")
			return "", errors.New("unexpected")
		},
		workspaceCommanderStub{
			resolve: func(
				context.Context,
				string,
				Kind,
				string,
				Status,
			) (Resolution, error) {
				t.Fatal("ResolveByID() was called")
				return Resolution{}, nil
			},
			delete: func(context.Context, string, Kind, string) (bool, error) {
				t.Fatal("DeleteByID() was called")
				return false, nil
			},
		},
		func() { t.Fatal("schedule dispatcher was triggered") },
	)
	if err != nil {
		t.Fatalf("NewWorkspaceHandler() error = %v", err)
	}

	response := httptest.NewRecorder()
	handler.ServeHTTP(
		response,
		httptest.NewRequest(http.MethodDelete, "/workspace/automations/watch/id", nil),
	)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusUnauthorized)
	}
}

func TestValidateWorkspaceCommandRejectsInvalidScope(t *testing.T) {
	t.Parallel()

	if _, _, err := validateWorkspaceCommand(
		"not-a-user",
		KindReminder,
		workspaceTestResourceID,
	); !errors.Is(err, ErrUserIDInvalid) {
		t.Fatalf("invalid user error = %v", err)
	}
	if _, _, err := validateWorkspaceCommand(
		workspaceTestUserID,
		Kind("other"),
		workspaceTestResourceID,
	); !errors.Is(err, ErrKindInvalid) {
		t.Fatalf("invalid kind error = %v", err)
	}
	if _, _, err := validateWorkspaceCommand(
		workspaceTestUserID,
		KindReminder,
		"not-a-resource",
	); !errors.Is(err, ErrResourceIDInvalid) {
		t.Fatalf("invalid resource error = %v", err)
	}
}
