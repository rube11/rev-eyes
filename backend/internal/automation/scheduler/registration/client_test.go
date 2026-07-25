package registration

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestClientRegistersReminder(t *testing.T) {
	t.Parallel()

	var gotMethod string
	server := httptest.NewTLSServer(http.HandlerFunc(func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		gotMethod = r.Method
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client, err := NewClient(server.URL, server.Client())
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	dueAt := time.Now().Add(time.Hour)
	err = client.Register(context.Background(), Registration{
		ID:         "registration-id",
		Kind:       KindReminder,
		ResourceID: "resource-id",
		ScheduleAt: &dueAt,
	})
	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Fatalf("method = %q", gotMethod)
	}
}

func TestClientRejectsInvalidConfiguration(t *testing.T) {
	t.Parallel()

	if _, err := NewClient("", http.DefaultClient); !errors.Is(err, ErrRegistrarURLRequired) {
		t.Fatalf("empty URL error = %v", err)
	}
	if _, err := NewClient("http://example.com", http.DefaultClient); !errors.Is(err, ErrRegistrarURLInvalid) {
		t.Fatalf("HTTP URL error = %v", err)
	}
	if _, err := NewClient("https://example.com", nil); !errors.Is(err, ErrHTTPClientRequired) {
		t.Fatalf("nil client error = %v", err)
	}
}

func TestClientReturnsRegistrarError(t *testing.T) {
	t.Parallel()

	server := httptest.NewTLSServer(http.HandlerFunc(func(
		w http.ResponseWriter,
		_ *http.Request,
	) {
		http.Error(w, "invalid schedule", http.StatusBadRequest)
	}))
	defer server.Close()

	client, _ := NewClient(server.URL, server.Client())
	endAt := time.Now().Add(24 * time.Hour)
	err := client.Register(context.Background(), Registration{
		ID:              "registration-id",
		Kind:            KindWatch,
		ResourceID:      "resource-id",
		IntervalMinutes: 60,
		EndAt:           &endAt,
	})
	if err == nil {
		t.Fatal("Register() error = nil")
	}
}
