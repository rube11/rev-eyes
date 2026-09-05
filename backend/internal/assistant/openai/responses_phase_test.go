package openai

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func phaseTestMessage(phase any, texts ...string) json.RawMessage {
	content := make([]map[string]string, 0, len(texts))
	for _, text := range texts {
		content = append(content, map[string]string{"type": "output_text", "text": text})
	}
	message := map[string]any{"type": "message", "role": "assistant", "content": content}
	if phase != nil {
		message["phase"] = phase
	}
	encoded, err := json.Marshal(message)
	if err != nil {
		panic(err)
	}
	return encoded
}

func TestParseOutputPhaseSelectsExplicitFinalInsteadOfConcatenatingDraftJSON(t *testing.T) {
	t.Parallel()
	const draft = `{"claims":[{"text":"Unverified draft"}],"limitations":[]}`
	const final = `{"claims":[],"limitations":["partial"]}`
	for _, output := range [][]json.RawMessage{
		{phaseTestMessage("commentary", draft), phaseTestMessage("final_answer", final)},
		{phaseTestMessage("final_answer", final), phaseTestMessage("commentary", draft)},
		{phaseTestMessage(nil, draft), phaseTestMessage("final_answer", final)},
		{phaseTestMessage("commentary", "Searching public sources."), phaseTestMessage(nil, draft), phaseTestMessage("final_answer", final)},
	} {
		calls, text, err := parseOutput(output)
		if err != nil || len(calls) != 0 || text != final || !json.Valid([]byte(text)) {
			t.Errorf("phase selection must return only explicit final JSON: calls=%v text=%q err=%v", calls, text, err)
		}
	}
}

func TestParseOutputPhasePreservesLegacyUnphasedTextBehavior(t *testing.T) {
	t.Parallel()
	output := []json.RawMessage{
		phaseTestMessage(nil, "  First part.  ", "", " \n\t ", "Second part."),
		phaseTestMessage(nil, "Third part."),
	}
	_, text, err := parseOutput(output)
	if err != nil || text != "First part.\nSecond part.\nThird part." {
		t.Errorf("legacy text normalization changed: text=%q err=%v", text, err)
	}
	// A null optional phase has the same meaning as an absent one.
	_, text, err = parseOutput([]json.RawMessage{json.RawMessage(`{"type":"message","phase":null,"content":[{"type":"output_text","text":"Legacy null phase."}]}`)})
	if err != nil || text != "Legacy null phase." {
		t.Errorf("nullable phase broke legacy output: text=%q err=%v", text, err)
	}
}

func TestParseOutputPhaseNeverPromotesIntermediateOrEmptyFinalToAnswer(t *testing.T) {
	t.Parallel()
	for _, scenario := range []struct {
		name   string
		output []json.RawMessage
		want   string
	}{
		{"commentary_only", []json.RawMessage{phaseTestMessage("commentary", "I will check that.")}, ""},
		{"unknown_phase_only", []json.RawMessage{phaseTestMessage("future_phase", "Not documented as a final answer.")}, ""},
		{"empty_final_with_commentary", []json.RawMessage{phaseTestMessage("commentary", "Draft."), phaseTestMessage("final_answer", " ")}, ""},
		{"empty_final_with_legacy", []json.RawMessage{phaseTestMessage(nil, "Legacy draft."), phaseTestMessage("final_answer")}, ""},
		{"legacy_after_commentary", []json.RawMessage{phaseTestMessage("commentary", "Progress only."), phaseTestMessage(nil, "Legacy answer.")}, "Legacy answer."},
		{"nonmessage_final_phase", []json.RawMessage{json.RawMessage(`{"type":"reasoning","phase":"final_answer"}`), phaseTestMessage(nil, "Legacy answer.")}, "Legacy answer."},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			_, text, err := parseOutput(scenario.output)
			if err != nil || text != scenario.want {
				t.Errorf("got text=%q err=%v; want %q", text, err, scenario.want)
			}
		})
	}
}

func TestParseOutputPhasePreservesEveryToolCallRegardlessOfMessageSelection(t *testing.T) {
	t.Parallel()
	output := []json.RawMessage{
		phaseTestMessage("commentary", "Checking the sources."),
		json.RawMessage(`{"type":"function_call","call_id":"first","name":"search_web","arguments":"{\"query\":\"Aurora\"}"}`),
		phaseTestMessage("final_answer", "A final answer."),
		json.RawMessage(`{"type":"function_call","call_id":"second","name":"search_web","arguments":"{\"query\":\"Cedar\"}"}`),
	}
	calls, text, err := parseOutput(output)
	wantCalls := []toolCall{
		{CallID: "first", Name: "search_web", Arguments: json.RawMessage(`{"query":"Aurora"}`)},
		{CallID: "second", Name: "search_web", Arguments: json.RawMessage(`{"query":"Cedar"}`)},
	}
	if err != nil || text != "A final answer." || !reflect.DeepEqual(calls, wantCalls) {
		t.Errorf("phase selection lost/reordered calls: calls=%#v text=%q err=%v", calls, text, err)
	}
	// Tool-bearing commentary does not become a text answer when no final exists.
	calls, text, err = parseOutput(output[:2])
	if err != nil || len(calls) != 1 || text != "" {
		t.Errorf("commentary-only tool step changed: calls=%#v text=%q err=%v", calls, text, err)
	}
}

func TestParseOutputPhaseDoesNotSuppressRefusalsOrMalformedItems(t *testing.T) {
	t.Parallel()
	for _, phase := range []string{"commentary", "final_answer", "future_phase", ""} {
		t.Run("refusal_"+phase, func(t *testing.T) {
			refusal, _ := json.Marshal(map[string]any{"type": "message", "phase": phase, "content": []any{map[string]any{"type": "refusal", "refusal": "Cannot assist with that."}}})
			for _, output := range [][]json.RawMessage{
				{refusal, phaseTestMessage("final_answer", "Do not hide a refusal.")},
				{phaseTestMessage("final_answer", "Do not hide a refusal."), refusal},
			} {
				calls, text, err := parseOutput(output)
				if err == nil || !strings.Contains(err.Error(), "OpenAI refused response") || calls != nil || text != "" {
					t.Errorf("discarded phase hid refusal: calls=%v text=%q err=%v", calls, text, err)
				}
			}
		})
	}
	for _, raw := range []json.RawMessage{
		json.RawMessage(`{"type":`),
		json.RawMessage(`{"type":"function_call","call_id":"","name":"search_web","arguments":"{}"}`),
		json.RawMessage(`{"type":"function_call","call_id":"first","name":" ","arguments":"{}"}`),
	} {
		if _, _, err := parseOutput([]json.RawMessage{phaseTestMessage("final_answer", "Final."), raw}); err == nil {
			t.Errorf("explicit final hid malformed later output: %s", raw)
		}
	}
}

func TestParseOutputPhaseDoesNotGuessLastJSONOrMergeConflictingFinals(t *testing.T) {
	t.Parallel()
	const first = `{"claims":[{"text":"First claim"}],"limitations":[]}`
	const second = `{"claims":[{"text":"Conflicting second claim"}],"limitations":[]}`
	for _, output := range [][]json.RawMessage{
		{phaseTestMessage("final_answer", first), phaseTestMessage("final_answer", second)},
		{phaseTestMessage("final_answer", first, second)},
		{phaseTestMessage(nil, first), phaseTestMessage(nil, second)},
	} {
		_, text, err := parseOutput(output)
		if err != nil || text != first+"\n"+second || json.Valid([]byte(text)) {
			t.Errorf("parser chose/merged conflicting structured answers: text=%q err=%v", text, err)
		}
	}
}

func TestParseOutputPhaseKeepsRawOutputUntouchedForReplay(t *testing.T) {
	t.Parallel()
	output := []json.RawMessage{
		json.RawMessage(`{"type":"reasoning","encrypted_content":"opaque-fixture"}`),
		phaseTestMessage("commentary", "Checking evidence."),
		phaseTestMessage("final_answer", "Final result."),
	}
	before, _ := json.Marshal(output)
	if _, _, err := parseOutput(output); err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(output)
	if string(after) != string(before) {
		t.Errorf("parser mutated raw phase/reasoning items replayed by the agent: before=%s after=%s", before, after)
	}
}
