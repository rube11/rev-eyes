package scheduler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const (
	validEventID    = "b6a61f13-a12d-0410-f274-9c228037c2b2"
	validResourceID = "bc7c2fc8-2d82-4bc4-bb44-e244c788e978"
)

type recorderFunc func(context.Context, ScheduledEvent) error

func (f recorderFunc) Enqueue(ctx context.Context, event ScheduledEvent) error {
	return f(ctx, event)
}

func TestHandlerAuthenticatesAndRecordsEvent(t *testing.T) {
	t.Parallel()

	var got ScheduledEvent
	handler, err := NewHandler("secret", recorderFunc(func(
		_ context.Context,
		event ScheduledEvent,
	) error {
		got = event
		return nil
	}))
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}

	request := scheduledRequest(
		`{"id":"` + validEventID + `","source":"rev-eyes.scheduler",` +
			`"detail-type":"ReminderDue","detail":{"kind":"reminder",` +
			`"resource_id":"` + validResourceID + `"}}`,
	)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusAccepted {
		t.Fatalf("status = %d", response.Code)
	}
	if got.ID != validEventID ||
		got.Job != JobReminder ||
		got.ResourceID != validResourceID {
		t.Fatalf("event = %#v", got)
	}
}

func TestHandlerRejectsUnauthorizedRequest(t *testing.T) {
	t.Parallel()

	handler, _ := NewHandler("secret", recorderFunc(func(
		context.Context,
		ScheduledEvent,
	) error {
		t.Fatal("Enqueue() was called")
		return nil
	}))
	request := httptest.NewRequest(http.MethodPost, "/internal/scheduler/run", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", response.Code)
	}
}

func TestNewHandlerRequiresDependencies(t *testing.T) {
	t.Parallel()

	recorder := recorderFunc(func(context.Context, ScheduledEvent) error { return nil })
	if _, err := NewHandler("", recorder); !errors.Is(err, ErrSecretRequired) {
		t.Fatalf("empty secret error = %v", err)
	}
	if _, err := NewHandler("secret", nil); !errors.Is(err, ErrRecorderRequired) {
		t.Fatalf("missing recorder error = %v", err)
	}
}

func TestHandlerRoutesWatchEvent(t *testing.T) {
	t.Parallel()

	var gotJob Job
	handler, _ := NewHandler("secret", recorderFunc(func(
		_ context.Context,
		event ScheduledEvent,
	) error {
		gotJob = event.Job
		return nil
	}))
	request := scheduledRequest(
		`{"id":"` + validEventID + `","source":"rev-eyes.scheduler",` +
			`"detail-type":"WatchDue","detail":{"kind":"watch",` +
			`"resource_id":"` + validResourceID + `"}}`,
	)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusAccepted || gotJob != JobWatch {
		t.Fatalf("status = %d, job = %q", response.Code, gotJob)
	}
}

func TestHandlerRejectsMismatchedEvent(t *testing.T) {
	t.Parallel()

	handler, _ := NewHandler("secret", recorderFunc(func(
		context.Context,
		ScheduledEvent,
	) error {
		t.Fatal("Enqueue() was called")
		return nil
	}))
	request := scheduledRequest(
		`{"id":"` + validEventID + `","source":"rev-eyes.scheduler",` +
			`"detail-type":"ReminderDue","detail":{"kind":"watch",` +
			`"resource_id":"` + validResourceID + `"}}`,
	)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", response.Code)
	}
}

func TestHandlerRejectsMalformedEventBridgeID(t *testing.T) {
	t.Parallel()

	handler, _ := NewHandler("secret", recorderFunc(func(
		context.Context,
		ScheduledEvent,
	) error {
		t.Fatal("Enqueue() was called")
		return nil
	}))
	request := scheduledRequest(
		`{"id":"not-an-event-id","source":"rev-eyes.scheduler",` +
			`"detail-type":"ReminderDue","detail":{"kind":"reminder",` +
			`"resource_id":"` + validResourceID + `"}}`,
	)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", response.Code)
	}
}

func TestHandlerReturnsRetryableErrorWhenInboxFails(t *testing.T) {
	t.Parallel()

	handler, _ := NewHandler("secret", recorderFunc(func(
		context.Context,
		ScheduledEvent,
	) error {
		return errors.New("database unavailable")
	}))
	request := scheduledRequest(
		`{"id":"` + validEventID + `","source":"rev-eyes.scheduler",` +
			`"detail-type":"WatchDue","detail":{"kind":"watch",` +
			`"resource_id":"` + validResourceID + `"}}`,
	)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d", response.Code)
	}
}

func scheduledRequest(body string) *http.Request {
	request := httptest.NewRequest(
		http.MethodPost,
		"/internal/scheduler/run",
		strings.NewReader(body),
	)
	request.Header.Set(SecretHeader, "secret")
	return request
}
