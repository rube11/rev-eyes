package watch

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	minIntervalMinutes = 60
	maxIntervalMinutes = 1440
	maxWatchDuration   = 30 * 24 * time.Hour
	maxConditionLength = 200
	maxQueryLength     = 400
	MaxActiveWatches   = 5
)

var ErrProposalInvalid = errors.New("watch proposal is invalid")

// Proposal describes a web condition that remains inactive until confirmed.
type Proposal struct {
	Query           string
	Condition       string
	IntervalMinutes int
	ExpiresAt       time.Time
}

func (p Proposal) normalize() Proposal {
	p.Query = strings.TrimSpace(p.Query)
	p.Condition = strings.TrimSpace(p.Condition)
	if !p.ExpiresAt.IsZero() {
		p.ExpiresAt = p.ExpiresAt.UTC()
	}
	return p
}

func (p Proposal) validate() error {
	switch {
	case p.Query == "":
		return fmt.Errorf("%w: query is required", ErrProposalInvalid)
	case utf8.RuneCountInString(p.Query) > maxQueryLength:
		return fmt.Errorf("%w: query is too long", ErrProposalInvalid)
	case p.Condition == "":
		return fmt.Errorf("%w: condition is required", ErrProposalInvalid)
	case utf8.RuneCountInString(p.Condition) > maxConditionLength:
		return fmt.Errorf("%w: condition is too long", ErrProposalInvalid)
	case p.IntervalMinutes < minIntervalMinutes || p.IntervalMinutes > maxIntervalMinutes:
		return fmt.Errorf("%w: interval must be between %d and %d minutes", ErrProposalInvalid, minIntervalMinutes, maxIntervalMinutes)
	case p.ExpiresAt.IsZero():
		return fmt.Errorf("%w: expiration is required", ErrProposalInvalid)
	default:
		return nil
	}
}

// Watch is a claimed background check.
type Watch struct {
	ID          string
	UserID      string
	Query       string
	Condition   string
	NextCheckAt time.Time
}

// Item is one public web result observed by a watch.
type Item struct {
	Title string
	URL   string
}
