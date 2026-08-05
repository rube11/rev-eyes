package proposal

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/rube11/rev-eyes/backend/internal/automation/watch"
	"github.com/rube11/rev-eyes/backend/internal/tool"
)

const (
	KindReminder Kind = "reminder"
	KindWatch    Kind = "watch"

	StatusAccepted Status = "accepted"
	StatusRejected Status = "rejected"
)

var ErrResolverRequired = errors.New("proposal resolver is required")
var ErrScheduleTriggerRequired = errors.New("schedule registration trigger is required")

type Kind string
type Status string

type Resolution struct {
	Kind                    Kind
	Status                  Status
	ActiveWatchLimitReached bool
}

type Resolver interface {
	ResolvePending(context.Context, tool.Scope, Status) (Resolution, bool, error)
}

// Confirmer resolves only short, unambiguous answers to the latest proposal.
type Confirmer struct {
	resolver        Resolver
	triggerSchedule func()
}

func NewConfirmer(resolver Resolver, triggerSchedule func()) (*Confirmer, error) {
	if resolver == nil {
		return nil, ErrResolverRequired
	}
	if triggerSchedule == nil {
		return nil, ErrScheduleTriggerRequired
	}
	return &Confirmer{
		resolver:        resolver,
		triggerSchedule: triggerSchedule,
	}, nil
}

func (c *Confirmer) Confirm(
	ctx context.Context,
	scope tool.Scope,
	utterance string,
) (string, bool, error) {
	status, understood := confirmationStatus(utterance)
	if !understood {
		return "", false, nil
	}

	resolution, resolved, err := c.resolver.ResolvePending(ctx, scope, status)
	if err != nil || !resolved {
		return "", false, err
	}
	if resolution.ActiveWatchLimitReached {
		return fmt.Sprintf(
			"You already have %d active watches. Let one expire before starting another.",
			watch.MaxActiveWatches,
		), true, nil
	}
	if resolution.Status == StatusAccepted {
		c.triggerSchedule()
	}
	return confirmationResponse(resolution.Kind, resolution.Status), true, nil
}

func confirmationStatus(utterance string) (Status, bool) {
	normalized := strings.ToLower(strings.TrimSpace(utterance))
	normalized = strings.Trim(normalized, " .,!?;:")

	switch normalized {
	case "yes", "yeah", "yep", "yup", "yes please", "sure", "okay", "ok", "do it", "please do", "go ahead", "sounds good", "save that":
		return StatusAccepted, true
	case "no", "nope", "nah", "don't", "do not", "cancel", "never mind", "nevermind":
		return StatusRejected, true
	default:
		return "", false
	}
}

func confirmationResponse(kind Kind, status Status) string {
	if status == StatusRejected {
		if kind == KindWatch {
			return "Okay, I won't create that watch."
		}
		return "Okay, I won't create that reminder."
	}
	if kind == KindWatch {
		return "Okay, I'll watch for that."
	}
	return "Okay, I saved that reminder."
}
