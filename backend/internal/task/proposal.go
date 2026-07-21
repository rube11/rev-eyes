package task

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/rube11/rev-eyes/backend/internal/tool"
)

const (
	StatusAccepted Status = "accepted"
	StatusRejected Status = "rejected"

	maxFieldLength = 120
)

var (
	ErrProposalInvalid  = errors.New("task proposal is invalid")
	ErrResolverRequired = errors.New("task proposal resolver is required")
)

type Status string

// Proposal is an action awaiting explicit user confirmation.
type Proposal struct {
	Title    string
	Schedule string
}

func (p Proposal) normalize() Proposal {
	p.Title = strings.TrimSpace(p.Title)
	p.Schedule = strings.TrimSpace(p.Schedule)
	return p
}

func (p Proposal) validate() error {
	switch {
	case p.Title == "":
		return fmt.Errorf("%w: title is required", ErrProposalInvalid)
	case utf8.RuneCountInString(p.Title) > maxFieldLength:
		return fmt.Errorf("%w: title is too long", ErrProposalInvalid)
	case utf8.RuneCountInString(p.Schedule) > maxFieldLength:
		return fmt.Errorf("%w: schedule is too long", ErrProposalInvalid)
	default:
		return nil
	}
}

type Resolver interface {
	ResolvePending(context.Context, tool.Scope, Status) (bool, error)
}

// Confirmer resolves a pending proposal only for an unambiguous answer.
type Confirmer struct {
	resolver Resolver
}

func NewConfirmer(resolver Resolver) (*Confirmer, error) {
	if resolver == nil {
		return nil, ErrResolverRequired
	}
	return &Confirmer{resolver: resolver}, nil
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

	resolved, err := c.resolver.ResolvePending(ctx, scope, status)
	if err != nil || !resolved {
		return "", false, err
	}
	if status == StatusAccepted {
		return "Okay, I saved that reminder.", true, nil
	}
	return "Okay, I won't create that reminder.", true, nil
}

func confirmationStatus(utterance string) (Status, bool) {
	normalized := strings.ToLower(strings.TrimSpace(utterance))
	normalized = strings.Trim(normalized, " .,!?;:")

	switch normalized {
	case "yes", "yeah", "yep", "yup", "yes please", "sure", "okay", "ok", "do it", "please do", "go ahead", "sounds good":
		return StatusAccepted, true
	case "no", "nope", "nah", "don't", "do not", "cancel", "never mind", "nevermind":
		return StatusRejected, true
	default:
		return "", false
	}
}
