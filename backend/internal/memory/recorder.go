package memory

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rube11/rev-eyes/backend/internal/tool"
)

const (
	defaultRecorderQueueSize = 64
	defaultCaptureTimeout    = 30 * time.Second
)

var (
	ErrExtractorRequired  = errors.New("memory extractor is required")
	ErrWriterRequired     = errors.New("memory candidate writer is required")
	ErrNoMemoryCandidates = errors.New("utterance contained no reusable memory")
)

// Extractor proposes zero or more atomic memories from one finalized user
// utterance. It must not infer facts that are absent from the supplied text.
type Extractor interface {
	Extract(context.Context, string) ([]Candidate, error)
}

// Writer persists a complete candidate batch for one trusted source.
type Writer interface {
	RememberCandidates(
		context.Context,
		tool.Scope,
		string,
		[]Candidate,
	) (int, error)
}

type captureJob struct {
	scope      tool.Scope
	sourceID   string
	text       string
	capturedAt time.Time
}

// Recorder owns a bounded, process-local background queue. Run must receive
// the application lifecycle context, not a WebSocket or request context, so a
// disconnected client does not cancel accepted memory work.
type Recorder struct {
	extractor Extractor
	writer    Writer
	jobs      chan captureJob
	timeout   time.Duration
	now       func() time.Time

	started atomic.Bool
	closed  atomic.Bool
	stateMu sync.RWMutex

	callbackMu sync.RWMutex
	onStored   func(userID string)
}

// SetOnStored registers a best-effort refresh hook for background writes.
// Explicit writes are reported by the synchronous utterance result instead.
func (r *Recorder) SetOnStored(callback func(userID string)) {
	if r == nil {
		return
	}
	r.callbackMu.Lock()
	r.onStored = callback
	r.callbackMu.Unlock()
}

func NewRecorder(extractor Extractor, writer Writer) (*Recorder, error) {
	return newRecorder(
		extractor,
		writer,
		defaultRecorderQueueSize,
		defaultCaptureTimeout,
	)
}

func newRecorder(
	extractor Extractor,
	writer Writer,
	queueSize int,
	timeout time.Duration,
) (*Recorder, error) {
	if extractor == nil {
		return nil, ErrExtractorRequired
	}
	if writer == nil {
		return nil, ErrWriterRequired
	}
	if queueSize <= 0 {
		queueSize = defaultRecorderQueueSize
	}
	if timeout <= 0 {
		timeout = defaultCaptureTimeout
	}
	return &Recorder{
		extractor: extractor,
		writer:    writer,
		jobs:      make(chan captureJob, queueSize),
		timeout:   timeout,
		now:       time.Now,
	}, nil
}

// Capture queues one finalized user utterance without waiting for extraction,
// embedding, or persistence. It returns false for invalid work, a full queue,
// or a recorder whose Run loop has stopped.
func (r *Recorder) Capture(scope tool.Scope, sourceID string, text string) bool {
	if r == nil {
		return false
	}
	scope.UserID = strings.TrimSpace(scope.UserID)
	scope.SessionID = strings.TrimSpace(scope.SessionID)
	sourceID = strings.TrimSpace(sourceID)
	text = strings.TrimSpace(text)
	if scope.UserID == "" || scope.SessionID == "" || sourceID == "" || text == "" {
		return false
	}

	r.stateMu.RLock()
	defer r.stateMu.RUnlock()
	if r.closed.Load() {
		return false
	}
	select {
	case r.jobs <- captureJob{
		scope: scope, sourceID: sourceID, text: text, capturedAt: r.now().UTC(),
	}:
		return true
	default:
		return false
	}
}

// RememberExplicit runs the same atomic extraction pipeline for
// an explicit remember request, but waits for persistence so the caller can
// acknowledge only after the memory is durable.
func (r *Recorder) RememberExplicit(
	ctx context.Context,
	scope tool.Scope,
	sourceID string,
	text string,
) error {
	if r == nil {
		return ErrWriterRequired
	}
	jobCtx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	_, err := r.record(jobCtx, captureJob{
		scope:      scope,
		sourceID:   strings.TrimSpace(sourceID),
		text:       strings.TrimSpace(text),
		capturedAt: r.now().UTC(),
	}, true)
	return err
}

// Run processes accepted captures serially until the application context is
// canceled. A single worker preserves each user's utterance order in the first
// implementation and bounds model/API concurrency. Additional workers should
// only be introduced with per-user ordering at the durable queue layer.
func (r *Recorder) Run(ctx context.Context) {
	if r == nil || !r.started.CompareAndSwap(false, true) {
		return
	}
	defer func() {
		r.stateMu.Lock()
		r.closed.Store(true)
		r.stateMu.Unlock()
	}()

	for {
		select {
		case <-ctx.Done():
			return
		case job := <-r.jobs:
			if ctx.Err() != nil {
				return
			}
			r.process(ctx, job)
		}
	}
}

func (r *Recorder) process(ctx context.Context, job captureJob) {
	jobCtx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	stored, err := r.record(jobCtx, job, false)
	if err != nil {
		if jobCtx.Err() == nil {
			slog.WarnContext(jobCtx, "memory recording failed", "error", err)
		}
		return
	}
	if stored > 0 {
		r.notifyStored(job.scope.UserID)
	}
}

func (r *Recorder) record(
	ctx context.Context,
	job captureJob,
	explicit bool,
) (int, error) {
	candidates, err := r.extractor.Extract(ctx, job.text)
	if err != nil {
		return 0, err
	}
	if len(candidates) == 0 {
		if explicit {
			return 0, ErrNoMemoryCandidates
		}
		return 0, nil
	}
	now := r.now().UTC()
	ready := make([]Candidate, 0, len(candidates))
	for index := range candidates {
		candidate := candidates[index]
		if candidate.Retention == RetentionTemporary {
			expiresAt := job.capturedAt.Add(TemporaryMemoryLifetime)
			if !expiresAt.After(now) {
				continue
			}
			candidate.ExpiresAt = &expiresAt
		}
		candidate = candidate.Normalize()
		if candidate.Retention == RetentionTemporary && candidate.ExpiresAt == nil {
			return 0, fmt.Errorf("%w: temporary memory requires an expiration", ErrCandidateInvalid)
		}
		if err := candidate.Validate(); err != nil {
			return 0, err
		}
		ready = append(ready, candidate)
	}
	if len(ready) == 0 {
		if explicit {
			return 0, ErrNoMemoryCandidates
		}
		return 0, nil
	}
	return r.writer.RememberCandidates(ctx, job.scope, job.sourceID, ready)
}

func (r *Recorder) notifyStored(userID string) {
	r.callbackMu.RLock()
	callback := r.onStored
	r.callbackMu.RUnlock()
	if callback != nil {
		callback(userID)
	}
}
