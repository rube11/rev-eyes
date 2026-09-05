package openai

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/rube11/rev-eyes/backend/internal/session"
	"github.com/rube11/rev-eyes/backend/internal/tool"
)

// Checks the instructions actually sent at planning, after a failed lookup,
// and during final review. The scripted model does not measure whether a live
// model follows these rules; that requires a separately graded search run.
func TestAgentWebPlanningRecoveryReachesRetryAndReview(t *testing.T) {
	t.Parallel()
	search := &recordingTool{name: "search_web", err: errors.New("no usable results")}
	var requests []map[string]json.RawMessage
	agent := webReasoningTestAgent(t, "gpt-5.4-mini", search, []string{"search_web", "", ""}, &requests)
	if _, err := agent.Respond(context.Background(), tool.Scope{}, "Verify the cafe's breakfast prices and Saturday opening hours.", session.Conversation{}, nil); err != nil {
		t.Fatal(err)
	}
	if len(requests) != 3 || len(search.arguments) != 1 {
		t.Fatalf("requests=%d searches=%d, want planning, draft, review with one search", len(requests), len(search.arguments))
	}
	for index, request := range requests {
		var instructions string
		if err := json.Unmarshal(request["instructions"], &instructions); err != nil {
			t.Fatal(err)
		}
		for _, required := range []string{
			"quick mode only to discover possible sources; it does not fetch source pages",
			"Every follow-up seeking those facts also needs research mode",
			"hostnames supplied by the user or identified by retrieved evidence",
			"never site: syntax in the query",
			"try the named candidate without the domain filter",
			"do not repeat the same failed restriction",
			"try another promising candidate instead of repeating that lookup",
			"Give supported partial results and identify missing coverage",
		} {
			if !strings.Contains(instructions, required) {
				t.Errorf("request %d lost planning rule %q", index, required)
			}
		}
	}
	var input []json.RawMessage
	if err := json.Unmarshal(requests[2]["input"], &input); err != nil {
		t.Fatal(err)
	}
	var foundReview bool
	for _, raw := range input {
		var message inputMessage
		if json.Unmarshal(raw, &message) == nil && message.Role == "developer" && message.Content == webDraftReviewInstructions {
			foundReview = true
			for _, required := range []string{"research-mode search", "removing the filter or choosing another candidate", "Keep supported partial results"} {
				if !strings.Contains(message.Content, required) {
					t.Errorf("review lost recovery rule %q", required)
				}
			}
		}
	}
	if !foundReview {
		t.Fatal("failed lookup did not reach the final review instructions")
	}
}
