package registration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const maxErrorBodyBytes = 4096

type Kind string
type Operation string

const (
	KindReminder Kind = "reminder"
	KindWatch    Kind = "watch"

	OperationRegister Operation = "register"
	OperationCancel   Operation = "cancel"
)

var (
	ErrRegistrarURLRequired = errors.New("SCHEDULE_REGISTRAR_URL is required")
	ErrRegistrarURLInvalid  = errors.New("SCHEDULE_REGISTRAR_URL must be an HTTPS URL")
	ErrHTTPClientRequired   = errors.New("schedule registrar HTTP client is required")
	ErrRegistrationInvalid  = errors.New("schedule registration is invalid")
)

type Registration struct {
	ID              string     `json:"-"`
	Operation       Operation  `json:"operation"`
	Kind            Kind       `json:"kind"`
	ResourceID      string     `json:"resource_id"`
	ScheduleAt      *time.Time `json:"schedule_at,omitempty"`
	IntervalMinutes int        `json:"interval_minutes,omitempty"`
	EndAt           *time.Time `json:"end_at,omitempty"`
	Attempts        int        `json:"-"`
}

func (r Registration) validate() error {
	if strings.TrimSpace(r.ID) == "" || strings.TrimSpace(r.ResourceID) == "" {
		return ErrRegistrationInvalid
	}
	if r.Operation == OperationCancel {
		if r.Kind != KindReminder && r.Kind != KindWatch {
			return ErrRegistrationInvalid
		}
		if r.ScheduleAt != nil || r.IntervalMinutes != 0 || r.EndAt != nil {
			return ErrRegistrationInvalid
		}
		return nil
	}
	if r.Operation != OperationRegister {
		return ErrRegistrationInvalid
	}
	switch r.Kind {
	case KindReminder:
		if r.ScheduleAt == nil || r.IntervalMinutes != 0 || r.EndAt != nil {
			return ErrRegistrationInvalid
		}
	case KindWatch:
		if r.ScheduleAt != nil ||
			r.IntervalMinutes < 60 ||
			r.IntervalMinutes > 1440 ||
			r.EndAt == nil {
			return ErrRegistrationInvalid
		}
	default:
		return ErrRegistrationInvalid
	}
	return nil
}

type Registrar interface {
	Register(context.Context, Registration) error
}

type Client struct {
	endpoint   string
	httpClient *http.Client
}

func NewClient(rawURL string, httpClient *http.Client) (*Client, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return nil, ErrRegistrarURLRequired
	}
	parsed, err := url.Parse(rawURL)
	if err != nil ||
		parsed.Scheme != "https" ||
		parsed.Host == "" ||
		parsed.User != nil ||
		parsed.RawQuery != "" ||
		parsed.Fragment != "" {
		return nil, ErrRegistrarURLInvalid
	}
	if httpClient == nil {
		return nil, ErrHTTPClientRequired
	}
	return &Client{endpoint: parsed.String(), httpClient: httpClient}, nil
}

func (c *Client) Register(ctx context.Context, registration Registration) error {
	if err := registration.validate(); err != nil {
		return err
	}
	body, err := json.Marshal(registration)
	if err != nil {
		return fmt.Errorf("encode schedule registration: %w", err)
	}
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		c.endpoint,
		bytes.NewReader(body),
	)
	if err != nil {
		return fmt.Errorf("create schedule registration request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")

	response, err := c.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("send schedule registration request: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode >= http.StatusOK &&
		response.StatusCode < http.StatusMultipleChoices {
		_, _ = io.Copy(io.Discard, response.Body)
		return nil
	}
	errorBody, _ := io.ReadAll(io.LimitReader(response.Body, maxErrorBodyBytes))
	message := strings.TrimSpace(string(errorBody))
	if message == "" {
		message = http.StatusText(response.StatusCode)
	}
	return fmt.Errorf(
		"schedule registration returned HTTP %d: %s",
		response.StatusCode,
		message,
	)
}
