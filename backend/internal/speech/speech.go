// Package speech carries observed speaker attribution, never authenticated identity.
package speech

import (
	"encoding/json"
	"strings"
)

type Role uint8

const (
	Unknown Role = iota
	Self
	Other
)

func (r Role) String() string {
	switch r {
	case Self:
		return "self"
	case Other:
		return "other"
	default:
		return "unknown"
	}
}

func (r Role) MarshalJSON() ([]byte, error) { return json.Marshal(r.String()) }

// Span uses half-open sample offsets relative to the containing PCM chunk.
type Span struct {
	Start, End int64
	Role       Role
}

type Segment struct {
	Role Role   `json:"speaker_role"`
	Text string `json:"text"`
}

type Utterance struct {
	Text     string    `json:"text"`
	Segments []Segment `json:"segments"`
}

// ContextOnly prevents another person's speech (including a mixed utterance)
// from being interpreted as authorization for an action.
func (u *Utterance) ContextOnly() bool {
	if u == nil {
		return false
	}
	for _, s := range u.Segments {
		if s.Role == Other {
			return true
		}
	}
	return false
}

// PersonalMemory requires an entirely wearer-attributed utterance. Never join
// disjoint wearer fragments across someone else's words into a new assertion.
func (u *Utterance) PersonalMemory() bool {
	if u == nil {
		return true
	} // Intentional text input.
	if len(u.Segments) == 0 {
		return false
	}
	for _, s := range u.Segments {
		if s.Role != Self {
			return false
		}
	}
	return true
}

// Record retains provenance in existing transcript storage and compaction.
func (u *Utterance) Record() string {
	segments := u.Segments
	if len(segments) == 0 {
		segments = []Segment{{Role: Unknown, Text: u.Text}}
	}
	lines := make([]string, 0, len(segments))
	for _, segment := range segments {
		label := "Unattributed speech (unknown)"
		if segment.Role == Self {
			label = "Wearer (self)"
		}
		if segment.Role == Other {
			label = "Other speaker (unidentified)"
		}
		text := segment.Text
		if len(segments) == 1 {
			text = u.Text
		} // Preserve transcription formatting.
		quoted, _ := json.Marshal(text)
		lines = append(lines, label+": "+string(quoted))
	}
	return strings.Join(lines, "\n")
}
