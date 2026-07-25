package registration

import (
	"context"
	"errors"
	"log/slog"
	"time"
)

const (
	registrationBatchSize = 50
	reconciliationPeriod  = time.Minute
	maxRetryDelay         = 15 * time.Minute
)

var (
	ErrRepositoryRequired = errors.New("schedule registration repository is required")
	ErrRegistrarRequired  = errors.New("schedule registrar is required")
)

type Dispatcher struct {
	repository Repository
	registrar  Registrar
	triggers   chan struct{}
	now        func() time.Time
}

func NewDispatcher(repository Repository, registrar Registrar) (*Dispatcher, error) {
	if repository == nil {
		return nil, ErrRepositoryRequired
	}
	if registrar == nil {
		return nil, ErrRegistrarRequired
	}
	return &Dispatcher{
		repository: repository,
		registrar:  registrar,
		triggers:   make(chan struct{}, 1),
		now:        time.Now,
	}, nil
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
			d.drain(ctx)
		case <-ticker.C:
			d.drain(ctx)
		}
	}
}

func (d *Dispatcher) drain(ctx context.Context) {
	for {
		registrations, err := d.repository.Claim(ctx, registrationBatchSize)
		if err != nil {
			slog.ErrorContext(ctx, "claim schedule registrations failed", "error", err)
			return
		}
		if len(registrations) == 0 {
			return
		}
		for _, registration := range registrations {
			if err := d.registrar.Register(ctx, registration); err != nil {
				retryAt := d.now().Add(retryDelay(registration.Attempts))
				if markErr := d.repository.MarkFailed(
					ctx,
					registration.ID,
					retryAt,
					err.Error(),
				); markErr != nil {
					slog.ErrorContext(
						ctx,
						"record schedule registration failure failed",
						"error",
						markErr,
					)
				}
				slog.ErrorContext(
					ctx,
					"schedule registration failed",
					"kind",
					registration.Kind,
					"resource_id",
					registration.ResourceID,
					"error",
					err,
				)
				continue
			}
			if err := d.repository.MarkRegistered(ctx, registration.ID); err != nil {
				slog.ErrorContext(
					ctx,
					"mark schedule registration complete failed",
					"error",
					err,
				)
			}
		}
		if len(registrations) < registrationBatchSize {
			return
		}
	}
}

func retryDelay(attempt int) time.Duration {
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
