package realtime

import (
	"strings"
	"unicode"
)

// isRepeatRequest recognizes a small presentation-control vocabulary. It runs
// after accurate transcription and before persistence or assistant routing.
func isRepeatRequest(utterance string) bool {
	normalized := strings.ToLower(strings.TrimSpace(utterance))
	var words strings.Builder
	words.Grow(len(normalized))
	for _, character := range normalized {
		if unicode.IsLetter(character) || unicode.IsNumber(character) {
			words.WriteRune(character)
			continue
		}
		words.WriteByte(' ')
	}
	normalized = strings.Join(strings.Fields(words.String()), " ")

	switch normalized {
	case "show that again", "show it again", "repeat that", "repeat that please":
		return true
	default:
		return false
	}
}
