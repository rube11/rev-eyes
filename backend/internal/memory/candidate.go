package memory

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	TemporaryMemoryLifetime = 3 * time.Hour
	maxMemoryKeyLength      = 160
)

type Retention string

const (
	RetentionDurable   Retention = "durable"
	RetentionTemporary Retention = "temporary"
)

var (
	ErrCandidateInvalid = errors.New("memory candidate is invalid")
	ErrUnsafeMemory     = errors.New("memory contains secret material")
	memoryKeyPattern    = regexp.MustCompile(`^[a-z0-9]+(?:[._-][a-z0-9]+)*$`)
)

// Candidate is one atomic memory proposed from a trusted source utterance.
// Retention is extraction metadata; persistence represents it with ExpiresAt.
type Candidate struct {
	Card      Card       `json:"card"`
	MemoryKey string     `json:"memory_key"`
	Retention Retention  `json:"retention"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

// Normalize canonicalizes candidate metadata and applies lifecycle defaults.
func (c Candidate) Normalize() Candidate {
	c.Card = c.Card.Normalize()
	sort.Slice(c.Card.Topics, func(left, right int) bool {
		return c.Card.Topics[left] < c.Card.Topics[right]
	})
	sort.Slice(c.Card.Details, func(left, right int) bool {
		if c.Card.Details[left].Key == c.Card.Details[right].Key {
			return c.Card.Details[left].Value < c.Card.Details[right].Value
		}
		return c.Card.Details[left].Key < c.Card.Details[right].Key
	})
	sort.Slice(c.Card.Entities, func(left, right int) bool {
		if c.Card.Entities[left].Type == c.Card.Entities[right].Type {
			return strings.ToLower(c.Card.Entities[left].Name) <
				strings.ToLower(c.Card.Entities[right].Name)
		}
		return c.Card.Entities[left].Type < c.Card.Entities[right].Type
	})
	c.MemoryKey = strings.ToLower(strings.Join(strings.Fields(c.MemoryKey), "_"))
	if c.Retention == "" {
		c.Retention = RetentionDurable
	}
	return c
}

func (c Candidate) Validate() error {
	if err := c.Card.Validate(); err != nil {
		return fmt.Errorf("%w: %v", ErrCandidateInvalid, err)
	}
	if c.MemoryKey == "" ||
		utf8.RuneCountInString(c.MemoryKey) > maxMemoryKeyLength ||
		!memoryKeyPattern.MatchString(c.MemoryKey) {
		return fmt.Errorf("%w: invalid memory key", ErrCandidateInvalid)
	}
	if c.Retention != RetentionDurable && c.Retention != RetentionTemporary {
		return fmt.Errorf("%w: invalid retention", ErrCandidateInvalid)
	}
	if c.Retention == RetentionDurable && c.ExpiresAt != nil {
		return fmt.Errorf("%w: durable memory cannot expire", ErrCandidateInvalid)
	}
	return nil
}
