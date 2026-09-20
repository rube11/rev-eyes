package responses

import (
	"encoding/json"
	"testing"
)

func TestParseOutputSeparatesCommentaryFromFinalAnswer(t *testing.T) {
	for _, phase := range []string{"", "final_answer"} {
		commentary, _ := json.Marshal(map[string]any{
			"type": "message", "phase": "commentary",
			"content": []any{map[string]any{"type": "output_text", "text": "Preparing arguments..."}},
		})
		final, _ := json.Marshal(map[string]any{
			"type": "message", "phase": phase,
			"content": []any{map[string]any{"type": "output_text", "text": `{"search_web":{}}`}},
		})
		calls, text, err := ParseOutput([]json.RawMessage{commentary, final})
		if err != nil || len(calls) != 0 || text != `{"search_web":{}}` {
			t.Fatalf("text=%q calls=%v err=%v", text, calls, err)
		}
	}
}
