package openai

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// Evaluation-only identity of the exact successful SearXNG payload consumed
// by the collector. Neither source content nor the public tool contract changes.
func webEvidenceCaptureDigest(content string) (string, bool) {
	var payload struct {
		Provider string            `json:"provider"`
		Results  []json.RawMessage `json:"results"`
	}
	if json.Unmarshal([]byte(content), &payload) != nil || payload.Provider != "searxng" {
		return "", false
	}
	digest := sha256.Sum256([]byte(content))
	return hex.EncodeToString(digest[:]), true
}

func webEvidenceCaptureOrder(calls []toolCall, outputs []json.RawMessage) []string {
	var order []string
	for index, call := range calls {
		if call.Name != "search_web" || index >= len(outputs) {
			continue
		}
		var output toolOutput
		if json.Unmarshal(outputs[index], &output) != nil {
			continue
		}
		if digest, ok := webEvidenceCaptureDigest(output.Output); ok {
			order = append(order, digest)
		}
	}
	return order
}
