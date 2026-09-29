package openai

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/rube11/rev-eyes/backend/internal/assistant"
	"github.com/rube11/rev-eyes/backend/internal/assistant/jev"
	"github.com/rube11/rev-eyes/backend/internal/session"
	"github.com/rube11/rev-eyes/backend/internal/speech"
	"github.com/rube11/rev-eyes/backend/internal/tool"
)

// Synthetic read-only eval: actual Jev routing and final composition, no account
// data or mutation tools. Opt in explicitly because this uses paid providers.
func TestLiveConversationTips(t *testing.T) {
	if os.Getenv("RUN_LIVE_CONVERSATION_TIPS_TEST") != "1" {
		t.Skip("set RUN_LIVE_CONVERSATION_TIPS_TEST=1 to call Jev and OpenAI")
	}
	client, err := jev.New(os.Getenv("JEV_API_KEY"))
	if err != nil {
		t.Fatal(err)
	}
	router, err := assistant.NewJevRouter(client)
	if err != nil {
		t.Fatal(err)
	}
	agent, err := NewAgent(os.Getenv("OPENAI_API_KEY"), os.Getenv("OPENAI_AGENT_MODEL"), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, text, profile, earlier string
		want                         assistant.Action
	}{
		{"unfamiliar_idiom", "That argument is a non sequitur.", "", "", assistant.ActionSuggestTip},
		{"technical_concept", "This API operation needs to be idempotent.", "", "", assistant.ActionSuggestTip},
		{"contextual_acronym", "The designer says our landing page needs a stronger CTA.", "", "", assistant.ActionSuggestTip},
		{"already_explained", "This operation is idempotent, meaning repeating it has the same effect as doing it once.", "", "", assistant.ActionIgnore},
		{"recent_explanation", "The operation should be idempotent.", "", "Idempotent means repeating an operation has the same effect as doing it once.", assistant.ActionIgnore},
		{"known_familiarity", "The operation should be idempotent.", "The wearer is an API engineer and understands idempotency well. They do not want basic programming definitions.", "", assistant.ActionIgnore},
		{"ordinary_statement", "Lunch is at noon in the kitchen.", "", "", assistant.ActionIgnore},
		{"question_to_colleague", "Jamie, can you send me the slides after lunch?", "", "", assistant.ActionIgnore},
		{"ambiguous_acronym", "We should check the XYZ before tomorrow.", "", "", assistant.ActionIgnore},
		{"explicit_definition", "Hey glasses, what does idempotent mean?", "", "", assistant.ActionRespond},
		{"practical_tip", "Hey glasses, any tip for staying focused while studying?", "", "", assistant.ActionSuggestTip},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			input := &speech.Utterance{Text: tc.text, Segments: []speech.Segment{{Role: speech.Other, Text: tc.text}}}
			conversation := session.Conversation{Speech: input, Profile: tc.profile}
			if tc.earlier != "" {
				conversation.Messages = []session.Message{{Speaker: session.SpeakerAssistant, Text: tc.earlier}}
			}
			decision, err := router.RouteWithContext(ctx, tc.text, conversation)
			if err != nil {
				t.Fatal(err)
			}
			if decision.Action != tc.want {
				t.Fatalf("route=%s want=%s", decision.Action, tc.want)
			}
			if decision.Action == assistant.ActionIgnore {
				return
			}
			result, err := agent.RespondWithResult(ctx, tool.Scope{Speech: input}, decision.Action, tc.text, conversation, nil)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("route=%s text=%q", decision.Action, result.Text)
			if strings.TrimSpace(result.Text) == "" || utf8.RuneCountInString(result.Text) > 420 {
				t.Fatalf("invalid glasses response %q", result.Text)
			}
		})
	}
}
