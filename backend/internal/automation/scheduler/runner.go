package scheduler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"
)

const (
	eventBatchSize          = 10
	reconciliationPeriod    = time.Minute
	terminalRetention       = 7 * 24 * time.Hour
	terminalCleanupInterval = time.Hour
	maxRetryDelay           = 15 * time.Minute
	maxReminderAttempts     = 96
	maxWatchAttempts        = 8
)

var (
	ErrRepositoryRequired     = errors.New("scheduled event repository is required")
	ErrReminderRunnerRequired = errors.New("scheduled reminder runner is required")
	ErrWatchRunnerRequired    = errors.New("scheduled watch runner is required")
)

type ResourceRunner interface {
	RunResource(context.Context, string) error
}

type Repository interface {
	Recorder
	Claim(context.Context, int) ([]ScheduledEvent, error)
	MarkProcessed(context.Context, string) error
	MarkFailed(context.Context, string, time.Time, string) error
	MarkAbandoned(context.Context, string, string) error
	PurgeTerminal(context.Context, time.Time) error
}

// Dispatcher drains a durable inbox. The wake channel only reduces latency;
// the database remains the source of truth if wakes are coalesced or lost.
type Dispatcher struct {
	repository     Repository
	reminderRunner ResourceRunner
	watchRunner    ResourceRunner
	triggers       chan struct{}
	now            func() time.Time
	nextCleanup    time.Time
}

func NewDispatcher(
	repository Repository,
	reminderRunner ResourceRunner,
	watchRunner ResourceRunner,
) (*Dispatcher, error) {
	if repository == nil {
		return nil, ErrRepositoryRequired
	}
	if reminderRunner == nil {
		return nil, ErrReminderRunnerRequired
	}
	if watchRunner == nil {
		return nil, ErrWatchRunnerRequired
	}
	return &Dispatcher{
		repository:     repository,
		reminderRunner: reminderRunner,
		watchRunner:    watchRunner,
		triggers:       make(chan struct{}, 1),
		now:            time.Now,
	}, nil
}

func (d *Dispatcher) Enqueue(ctx context.Context, event ScheduledEvent) error {
	if err := d.repository.Enqueue(ctx, event); err != nil {
		return err
	}
	d.Trigger()
	return nil
}

func (d *Dispatcher) Trigger() {
	select {
	case d.triggers <- struct{}{}:
	default:
	}
}

func (d *Dispatcher) Run(ctx context.Context) {
	ticker := time.NewTicker(reconciliationPeriod)
	defer ticker.Stop()
	d.Trigger()

	for {
		select {
		case <-ctx.Done():
			return
		case <-d.triggers:
			if err := d.drain(ctx); err != nil {
				slog.ErrorContext(ctx, "scheduled event drain failed", "error", err)
			}
		case <-ticker.C:
			if err := d.drain(ctx); err != nil {
				slog.ErrorContext(ctx, "scheduled event reconciliation failed", "error", err)
			}
		}
	}
}

func (d *Dispatcher) drain(ctx context.Context) error {
	var drainErr error
	now := d.now()
	if d.nextCleanup.IsZero() || !now.Before(d.nextCleanup) {
		if err := d.repository.PurgeTerminal(ctx, now.Add(-terminalRetention)); err != nil {
			drainErr = errors.Join(drainErr, fmt.Errorf("purge scheduled events: %w", err))
		} else {
			d.nextCleanup = now.Add(terminalCleanupInterval)
		}
	}

	for {
		events, err := d.repository.Claim(ctx, eventBatchSize)
		if err != nil {
			return errors.Join(drainErr, fmt.Errorf("claim scheduled events: %w", err))
		}
		if len(events) == 0 {
			return drainErr
		}

		for _, event := range events {
			if err := d.process(ctx, event); err != nil {
				if ctx.Err() != nil {
					return errors.Join(drainErr, ctx.Err())
				}
				drainErr = errors.Join(drainErr, d.recordFailure(ctx, event, err))
				continue
			}
			if err := d.repository.MarkProcessed(ctx, event.ID); err != nil {
				drainErr = errors.Join(
					drainErr,
					fmt.Errorf("mark scheduled event %s processed: %w", event.ID, err),
				)
			}
		}
		if len(events) < eventBatchSize {
			return drainErr
		}
	}
}

func (d *Dispatcher) process(ctx context.Context, event ScheduledEvent) error {
	switch event.Job {
	case JobReminder:
		return d.reminderRunner.RunResource(ctx, event.ResourceID)
	case JobWatch:
		return d.watchRunner.RunResource(ctx, event.ResourceID)
	default:
		return fmt.Errorf("unknown scheduled job %q", event.Job)
	}
}

func (d *Dispatcher) recordFailure(
	ctx context.Context,
	event ScheduledEvent,
	processErr error,
) error {
	if event.Attempts >= maxAttempts(event.Job) {
		if err := d.repository.MarkAbandoned(ctx, event.ID, processErr.Error()); err != nil {
			return fmt.Errorf("abandon scheduled event %s: %w", event.ID, err)
		}
		slog.ErrorContext(
			ctx,
			"scheduled event abandoned",
			"event_id",
			event.ID,
			"job",
			event.Job,
			"resource_id",
			event.ResourceID,
			"attempts",
			event.Attempts,
			"error",
			processErr,
		)
		return processErr
	}
	retryAt := d.now().Add(processingRetryDelay(event.Attempts))
	if err := d.repository.MarkFailed(
		ctx,
		event.ID,
		retryAt,
		processErr.Error(),
	); err != nil {
		return errors.Join(
			processErr,
			fmt.Errorf("record scheduled event %s failure: %w", event.ID, err),
		)
	}
	return processErr
}

func maxAttempts(job Job) int {
	if job == JobWatch {
		return maxWatchAttempts
	}
	return maxReminderAttempts
}

func processingRetryDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	delay := 5 * time.Second
	for current := 1; current < attempt && delay < maxRetryDelay; current++ {
		delay *= 2
	}
	if delay > maxRetryDelay {
		return maxRetryDelay
	}
	return delay
}
