package openai

import (
	_ "embed"
	"strings"
)

//go:embed TONALITY.md
var tonalityDocument string

func tonalityInstructions() string {
	return strings.TrimSpace(tonalityDocument)
}
