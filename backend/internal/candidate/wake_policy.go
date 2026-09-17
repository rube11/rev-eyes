package candidate

import (
	"regexp"
	"strings"
	"unicode"
)

// WakeReason identifies the explicit phrase family that authorized backend
// processing. It intentionally mirrors the WebView's local candidate gate.
type WakeReason string

const (
	WakeAssistantRequest WakeReason = "assistant_request"
	WakeCommitment       WakeReason = "commitment"
	WakeIntention        WakeReason = "intention"
	WakeManual           WakeReason = "manual"
	WakePreference       WakeReason = "preference"
	WakeReminder         WakeReason = "reminder"
)

type wakeRule struct {
	reason   WakeReason
	patterns []*regexp.Regexp
}

var wakeRules = []wakeRule{
	{
		reason: WakeReminder,
		patterns: []*regexp.Regexp{
			regexp.MustCompile(`\bremind\s+me\b`),
			regexp.MustCompile(`\bdon'?t\s+let\s+me\s+forget\b`),
		},
	},
	{
		reason: WakeAssistantRequest,
		patterns: []*regexp.Regexp{
			regexp.MustCompile(`\bglasses\b`),
			regexp.MustCompile(`^(?:please\s+)?remember\b`),
			regexp.MustCompile(`\b(?:show\s+(?:that|it)\s+again|repeat\s+that)\b`),
		},
	},
	{
		reason: WakeCommitment,
		patterns: []*regexp.Regexp{
			regexp.MustCompile(`\bneed(?:\s+to)?\b`),
			regexp.MustCompile(`\bi\s+should\b`),
		},
	},
	{
		reason: WakeIntention,
		patterns: []*regexp.Regexp{
			regexp.MustCompile(`\bi\s+plan\s+to\b`),
			regexp.MustCompile(`\bi\s+want\s+to\b`),
		},
	},
	{
		reason:   WakePreference,
		patterns: []*regexp.Regexp{regexp.MustCompile(`\bi\s+prefer\b`)},
	},
}

// MatchWakePhrase applies the authoritative wake policy to an accurate
// transcript. A miss must be discarded before persistence or model routing.
func MatchWakePhrase(transcript string) (WakeReason, bool) {
	normalized := normalizeWakeTranscript(transcript)
	if normalized == "" {
		return "", false
	}
	for _, rule := range wakeRules {
		for _, pattern := range rule.patterns {
			if pattern.MatchString(normalized) {
				return rule.reason, true
			}
		}
	}
	return "", false
}

func normalizeWakeTranscript(transcript string) string {
	transcript = strings.ToLower(strings.TrimSpace(transcript))
	transcript = strings.ReplaceAll(transcript, "’", "'")
	var normalized strings.Builder
	normalized.Grow(len(transcript))
	for _, character := range transcript {
		if unicode.IsLetter(character) || unicode.IsNumber(character) || character == '\'' {
			normalized.WriteRune(character)
			continue
		}
		normalized.WriteByte(' ')
	}
	return strings.Join(strings.Fields(normalized.String()), " ")
}
