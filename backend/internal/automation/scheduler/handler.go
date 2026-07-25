package scheduler

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
)

const (
	SecretHeader  = "X-Rev-Eyes-Scheduler-Key"
	EventSource   = "rev-eyes.scheduler"
	maxEventBytes = 64 * 1024
)

type Job string

const (
	JobReminder Job = "reminder"
	JobWatch    Job = "watch"
)

const (
	DetailTypeReminderDue = "ReminderDue"
	DetailTypeWatchDue    = "WatchDue"
)

var (
	ErrSecretRequired   = errors.New("SCHEDULER_SECRET is required")
	ErrRecorderRequired = errors.New("scheduled event recorder is required")
	ErrEventInvalid     = errors.New("scheduled event is invalid")
	uuidPattern         = regexp.MustCompile(
		`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`,
	)
)

type ScheduledEvent struct {
	ID         string
	Job        Job
	ResourceID string
	Attempts   int
}

func (e ScheduledEvent) validate() error {
	if !uuidPattern.MatchString(strings.TrimSpace(e.ID)) ||
		!uuidPattern.MatchString(strings.TrimSpace(e.ResourceID)) {
		return ErrEventInvalid
	}
	if e.Job != JobReminder && e.Job != JobWatch {
		return ErrEventInvalid
	}
	return nil
}

type Recorder interface {
	Enqueue(context.Context, ScheduledEvent) error
}

// Handler authenticates and durably records EventBridge deliveries.
type Handler struct {
	secret   string
	recorder Recorder
}

func NewHandler(secret string, recorder Recorder) (*Handler, error) {
	secret = strings.TrimSpace(secret)
	if secret == "" {
		return nil, ErrSecretRequired
	}
	if recorder == nil {
		return nil, ErrRecorderRequired
	}
	return &Handler{secret: secret, recorder: recorder}, nil
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !h.authorized(r.Header.Get(SecretHeader)) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	event, err := scheduledEvent(w, r)
	if err != nil {
		http.Error(w, "invalid scheduled event", http.StatusBadRequest)
		return
	}
	if err := h.recorder.Enqueue(r.Context(), event); err != nil {
		slog.ErrorContext(
			r.Context(),
			"record scheduled event failed",
			"event_id",
			event.ID,
			"job",
			event.Job,
			"resource_id",
			event.ResourceID,
			"error",
			err,
		)
		http.Error(w, "scheduled event unavailable", http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

func (h *Handler) authorized(secret string) bool {
	return subtle.ConstantTimeCompare([]byte(secret), []byte(h.secret)) == 1
}

func scheduledEvent(w http.ResponseWriter, r *http.Request) (ScheduledEvent, error) {
	var payload struct {
		ID         string `json:"id"`
		Source     string `json:"source"`
		DetailType string `json:"detail-type"`
		Detail     struct {
			Kind       string `json:"kind"`
			ResourceID string `json:"resource_id"`
		} `json:"detail"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxEventBytes))
	if err := decoder.Decode(&payload); err != nil {
		return ScheduledEvent{}, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return ScheduledEvent{}, errors.New("expected one scheduled event")
	}
	if strings.TrimSpace(payload.Source) != EventSource {
		return ScheduledEvent{}, ErrEventInvalid
	}

	event := ScheduledEvent{
		ID:         strings.TrimSpace(payload.ID),
		ResourceID: strings.TrimSpace(payload.Detail.ResourceID),
	}
	switch {
	case payload.DetailType == DetailTypeReminderDue &&
		payload.Detail.Kind == string(JobReminder):
		event.Job = JobReminder
	case payload.DetailType == DetailTypeWatchDue &&
		payload.Detail.Kind == string(JobWatch):
		event.Job = JobWatch
	default:
		return ScheduledEvent{}, ErrEventInvalid
	}
	if err := event.validate(); err != nil {
		return ScheduledEvent{}, err
	}
	return event, nil
}
