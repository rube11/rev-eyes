package memory

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rube11/rev-eyes/backend/internal/tool"
)

const (
	workspaceTestUserID   = "4e1c73d9-1641-48c1-8a00-84f73e2297cf"
	workspaceTestMemoryID = "28e8ec7c-dda4-4b92-ad47-7307a5294084"
)

type workspaceEditorStub struct {
	edit func(context.Context, tool.Scope, string, WorkspaceEdit) error
}

func (s workspaceEditorStub) EditByID(ctx context.Context, scope tool.Scope, id string, edit WorkspaceEdit) error {
	return s.edit(ctx, scope, id, edit)
}

func verifyTestToken(t *testing.T) func(context.Context, string) (string, error) {
	return func(_ context.Context, token string) (string, error) {
		if token != "access-token" {
			return "", errors.New("unknown token")
		}
		return workspaceTestUserID, nil
	}
}

func patchMemory(handler http.Handler, body string, token string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPatch, "/workspace/memories/"+workspaceTestMemoryID, strings.NewReader(body))
	request.SetPathValue("memory_id", workspaceTestMemoryID)
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

func TestWorkspaceHandlerAppliesEditAndNotifies(t *testing.T) {
	t.Parallel()

	var changed string
	var applied WorkspaceEdit
	handler, err := NewWorkspaceHandler(
		verifyTestToken(t),
		workspaceEditorStub{edit: func(_ context.Context, scope tool.Scope, id string, edit WorkspaceEdit) error {
			if scope.UserID != workspaceTestUserID || id != workspaceTestMemoryID {
				t.Fatalf("edit = %q, %q", scope.UserID, id)
			}
			applied = edit
			return nil
		}},
		func(userID string) { changed = userID },
	)
	if err != nil {
		t.Fatal(err)
	}

	recorder := patchMemory(handler, `{"action":"update","title":"Maya leads product","summary":"Maya leads product at Arcline."}`, "access-token")
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body %s", recorder.Code, recorder.Body.String())
	}
	if applied.Action != WorkspaceUpdate || applied.Title != "Maya leads product" {
		t.Fatalf("applied = %+v", applied)
	}
	if changed != workspaceTestUserID {
		t.Fatalf("workspaceChanged = %q", changed)
	}
}

func TestWorkspaceHandlerRejectsBadInput(t *testing.T) {
	t.Parallel()

	handler, err := NewWorkspaceHandler(
		verifyTestToken(t),
		workspaceEditorStub{edit: func(_ context.Context, _ tool.Scope, _ string, edit WorkspaceEdit) error {
			_, err := edit.Normalize()
			return err
		}},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}

	cases := map[string]struct {
		body, token string
		status      int
	}{
		"missing token":   {`{"action":"forget"}`, "", http.StatusUnauthorized},
		"unknown field":   {`{"action":"forget","extra":1}`, "access-token", http.StatusBadRequest},
		"unknown action":  {`{"action":"shout"}`, "access-token", http.StatusBadRequest},
		"update no title": {`{"action":"update","summary":"x"}`, "access-token", http.StatusBadRequest},
		"pin with text":   {`{"action":"pin","title":"x"}`, "access-token", http.StatusBadRequest},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			recorder := patchMemory(handler, testCase.body, testCase.token)
			if recorder.Code != testCase.status {
				t.Fatalf("status = %d, want %d (%s)", recorder.Code, testCase.status, recorder.Body.String())
			}
		})
	}
}

func TestWorkspaceHandlerReportsMissingMemory(t *testing.T) {
	t.Parallel()

	notified := false
	handler, err := NewWorkspaceHandler(
		verifyTestToken(t),
		workspaceEditorStub{edit: func(context.Context, tool.Scope, string, WorkspaceEdit) error {
			return ErrMemoryNotFound
		}},
		func(string) { notified = true },
	)
	if err != nil {
		t.Fatal(err)
	}
	recorder := patchMemory(handler, `{"action":"forget"}`, "access-token")
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d", recorder.Code)
	}
	if !strings.Contains(recorder.Body.String(), `"not_found"`) {
		t.Fatalf("body = %s", recorder.Body.String())
	}
	if notified {
		t.Fatal("workspaceChanged fired for a failed edit")
	}
}

func TestWorkspaceEditNormalize(t *testing.T) {
	t.Parallel()

	if _, err := (WorkspaceEdit{Action: WorkspaceUpdate, Title: "  A   title ", Summary: " body "}).Normalize(); err != nil {
		t.Fatal(err)
	}
	edit, _ := (WorkspaceEdit{Action: WorkspaceUpdate, Title: "  A   title ", Summary: " body "}).Normalize()
	if edit.Title != "A title" || edit.Summary != "body" {
		t.Fatalf("normalized = %+v", edit)
	}
	if _, err := (WorkspaceEdit{Action: WorkspaceUpdate, Title: strings.Repeat("x", 121), Summary: "y"}).Normalize(); !errors.Is(err, ErrInvalidMemoryEdit) {
		t.Fatalf("long title err = %v", err)
	}
	if _, err := (WorkspaceEdit{Action: WorkspaceRestore}).Normalize(); err != nil {
		t.Fatal(err)
	}
}
