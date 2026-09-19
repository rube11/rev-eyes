package openai

import (
	"strings"
	"testing"
)

func TestTonalityInstructions(t *testing.T) {
	t.Parallel()

	guide := tonalityInstructions()
	for _, section := range []string{
		"## Voice constants",
		"## Calibration",
		"## Wit and humor",
		"## Tone flexes by context",
		"## Original examples",
		"## Final voice check",
	} {
		if !strings.Contains(guide, section) {
			t.Errorf("tonality guide missing %q", section)
		}
	}
	if strings.Contains(guide, "You are Jarvis") {
		t.Fatal("tonality guide must inspire a distinct Eyes voice, not impersonate Jarvis")
	}
}
