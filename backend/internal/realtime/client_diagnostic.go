package realtime

import (
	"context"
	"errors"
	"strings"
	"time"
)

const (
	moonshineDiagnosticMessageType = "moonshine_diagnostic"
	maxDiagnosticTextBytes         = 2_048
	maxDiagnosticLabelBytes        = 64
	diagnosticRateLimit            = 40
	diagnosticRateWindow           = 10 * time.Second
)

var errClientDiagnosticInvalid = errors.New("client diagnostic is invalid")

// ClientDiagnostic is local-development telemetry from the Moonshine WebView
// path. It is deliberately separate from transcripts that enter the assistant.
type ClientDiagnostic struct {
	Event             string  `json:"event"`
	Kind              string  `json:"kind,omitempty"`
	Text              string  `json:"text,omitempty"`
	Name              string  `json:"name,omitempty"`
	Category          string  `json:"category,omitempty"`
	Confidence        float64 `json:"confidence,omitempty"`
	SampleOffset      int64   `json:"sample_offset,omitempty"`
	Reason            string  `json:"reason,omitempty"`
	ByteLength        int     `json:"byte_length,omitempty"`
	StartSampleOffset int64   `json:"start_sample_offset,omitempty"`
	EndSampleOffset   int64   `json:"end_sample_offset,omitempty"`
	Submitted         bool    `json:"submitted,omitempty"`
}

type ClientDiagnosticHandler func(ctx context.Context, diagnostic ClientDiagnostic)

func (diagnostic ClientDiagnostic) normalized() (ClientDiagnostic, error) {
	diagnostic.Event = strings.TrimSpace(diagnostic.Event)
	diagnostic.Kind = strings.TrimSpace(diagnostic.Kind)
	diagnostic.Text = strings.TrimSpace(diagnostic.Text)
	diagnostic.Name = strings.TrimSpace(diagnostic.Name)
	diagnostic.Category = strings.TrimSpace(diagnostic.Category)
	diagnostic.Reason = strings.TrimSpace(diagnostic.Reason)

	switch diagnostic.Event {
	case "transcript":
		if (diagnostic.Kind != "partial" && diagnostic.Kind != "committed") ||
			diagnostic.Text == "" ||
			len(diagnostic.Text) > maxDiagnosticTextBytes {
			return ClientDiagnostic{}, errClientDiagnosticInvalid
		}
	case "lifecycle":
		if !validDiagnosticLabel(diagnostic.Name) {
			return ClientDiagnostic{}, errClientDiagnosticInvalid
		}
	case "candidate_trigger":
		if !validGateCategory(diagnostic.Category) ||
			diagnostic.Confidence < 0 ||
			diagnostic.Confidence > 1 ||
			diagnostic.SampleOffset < 0 {
			return ClientDiagnostic{}, errClientDiagnosticInvalid
		}
	case "candidate_finalized":
		if !validDiagnosticLabel(diagnostic.Reason) ||
			!validGateCategory(diagnostic.Category) ||
			diagnostic.ByteLength <= 0 ||
			diagnostic.ByteLength > maxCandidateAudioBytes ||
			diagnostic.StartSampleOffset < 0 ||
			diagnostic.EndSampleOffset <= diagnostic.StartSampleOffset {
			return ClientDiagnostic{}, errClientDiagnosticInvalid
		}
	default:
		return ClientDiagnostic{}, errClientDiagnosticInvalid
	}
	return diagnostic, nil
}

func validDiagnosticLabel(value string) bool {
	return value != "" && len(value) <= maxDiagnosticLabelBytes &&
		!strings.ContainsAny(value, "\r\n")
}

type clientDiagnosticLimiter struct {
	windowStart time.Time
	count       int
}

func (limiter *clientDiagnosticLimiter) allow(now time.Time) bool {
	if limiter.windowStart.IsZero() ||
		now.Sub(limiter.windowStart) >= diagnosticRateWindow ||
		now.Before(limiter.windowStart) {
		limiter.windowStart = now
		limiter.count = 0
	}
	if limiter.count >= diagnosticRateLimit {
		return false
	}
	limiter.count++
	return true
}
