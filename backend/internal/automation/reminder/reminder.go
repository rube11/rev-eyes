package reminder

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

const maxFieldLength = 120

var ErrProposalInvalid = errors.New("task proposal is invalid")

// Proposal is an action awaiting explicit user confirmation.
type Proposal struct {
	Title    string
	Schedule string
	DueAt    time.Time
}

func (p Proposal) normalize() Proposal {
	p.Title = strings.TrimSpace(p.Title)
	p.Schedule = strings.TrimSpace(p.Schedule)
	if !p.DueAt.IsZero() {
		p.DueAt = p.DueAt.UTC()
	}
	return p
}

func (p Proposal) validate() error {
	switch {
	case p.Title == "":
		return fmt.Errorf("%w: title is required", ErrProposalInvalid)
	case utf8.RuneCountInString(p.Title) > maxFieldLength:
		return fmt.Errorf("%w: title is too long", ErrProposalInvalid)
	case p.Schedule == "":
		return fmt.Errorf("%w: schedule is required", ErrProposalInvalid)
	case utf8.RuneCountInString(p.Schedule) > maxFieldLength:
		return fmt.Errorf("%w: schedule is too long", ErrProposalInvalid)
	case p.DueAt.IsZero():
		return fmt.Errorf("%w: due time is required", ErrProposalInvalid)
	default:
		return nil
	}
}
