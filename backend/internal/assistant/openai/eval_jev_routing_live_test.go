package openai

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/rube11/rev-eyes/backend/internal/assistant"
	"github.com/rube11/rev-eyes/backend/internal/assistant/jev"
	"github.com/rube11/rev-eyes/backend/internal/assistant/openai/routing"
	"github.com/rube11/rev-eyes/backend/internal/memory"
	"github.com/rube11/rev-eyes/backend/internal/session"
	"github.com/rube11/rev-eyes/backend/internal/tool"
)

// TestLiveJevRoutingScenarios exercises Jev-owned intent routing, OpenAI memory
// enrichment, and response generation without touching a user's database.
func TestLiveJevRoutingScenarios(t *testing.T) {
	if os.Getenv("RUN_LIVE_JEV_ROUTING_TEST") != "1" {
		t.Skip("set RUN_LIVE_JEV_ROUTING_TEST=1 to call Jev and OpenAI")
	}

	openAIKey := requiredLiveEnv(t, "OPENAI_API_KEY")
	enricher, err := routing.New(openAIKey, requiredLiveEnv(t, "OPENAI_ROUTER_MODEL"))
	if err != nil {
		t.Fatalf("NewRouterEnricher: %v", err)
	}
	agent, err := NewAgent(openAIKey, requiredLiveEnv(t, "OPENAI_AGENT_MODEL"), nil)
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}
	jevClient, err := jev.New(requiredLiveEnv(t, "JEV_API_KEY"))
	if err != nil {
		t.Fatalf("jev.New: %v", err)
	}

	for _, scenario := range []struct {
		name         string
		spoken       string
		wantAction   assistant.Action
		wantResponse bool
		memories     []memory.Card
	}{
		{
			name:       "ordinary_statement_is_ignored",
			spoken:     "The meeting starts at three.",
			wantAction: assistant.ActionIgnore,
		},
		{
			name:       "arrival_updates_state_silently",
			spoken:     "I just arrived at the gym.",
			wantAction: assistant.ActionStateUpdate,
		},
		{
			name:         "completed_workout_gets_next_step",
			spoken:       "I just left the gym.",
			wantAction:   assistant.ActionStateTransition,
			wantResponse: true,
			memories:     workoutMemories(),
		},
		{
			name:         "workout_question_gets_direct_response",
			spoken:       "I just left the gym; what should I eat?",
			wantAction:   assistant.ActionRespond,
			wantResponse: true,
			memories:     workoutMemories(),
		},
		{
			name:       "memory_command_is_classified_by_jev",
			spoken:     "Remember that Maya is my manager.",
			wantAction: assistant.ActionRemember,
		},
	} {
		scenario := scenario
		t.Run(scenario.name, func(t *testing.T) {
			recorder := &recordingJevEvaluator{client: jevClient}
			router, err := assistant.NewJevRouter(recorder, enricher)
			if err != nil {
				t.Fatalf("NewJevRouter: %v", err)
			}
			memoryReader := &scenarioMemoryReader{cards: scenario.memories}
			service := assistant.NewService(
				router,
				agent,
				memoryReader,
				emptyConversationReader{},
				noProposalConfirmer{},
			)

			ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			defer cancel()
			outcome, err := service.HandleUtterance(
				ctx,
				tool.Scope{
					UserID:    "00000000-0000-0000-0000-000000000001",
					SessionID: "00000000-0000-0000-0000-000000000002",
					TimeZone:  "America/Los_Angeles",
				},
				"00000000-0000-0000-0000-000000000003",
				scenario.spoken,
			)
			if err != nil {
				t.Fatalf("HandleUtterance: %v", err)
			}
			if outcome.Decision.Action != scenario.wantAction {
				t.Errorf("action = %q, want %q", outcome.Decision.Action, scenario.wantAction)
			}
			hasResponse := strings.TrimSpace(outcome.Response) != ""
			if hasResponse != scenario.wantResponse {
				t.Errorf("response present = %v, want %v; response = %q", hasResponse, scenario.wantResponse, outcome.Response)
			}
			if hasResponse {
				assertLiveGlassesResponse(t, outcome.Response)
				lookup := memoryReader.Lookup()
				if len(lookup.Terms) == 0 && len(lookup.Topics) == 0 &&
					len(lookup.Kinds) == 0 && len(lookup.Entities) == 0 {
					t.Errorf("response route did not create structured memory lookup: %#v", lookup)
				}
			}

			encodedJev, _ := json.MarshalIndent(recorder.response.Answers["route"], "", "  ")
			encodedLookup, _ := json.MarshalIndent(memoryReader.Lookup(), "", "  ")
			t.Logf("spoken: %s", scenario.spoken)
			t.Logf("Jev route answer:\n%s", encodedJev)
			t.Logf("final decision: %+v", outcome.Decision)
			t.Logf("memory lookup:\n%s", encodedLookup)
			if hasResponse {
				t.Logf("assistant response:\n%s", outcome.Response)
			}
		})
	}
}

// TestLiveJevAmbiguousScenarios records both Jev's competing probabilities and
// the final user-facing response for utterances near routing boundaries.
func TestLiveJevAmbiguousScenarios(t *testing.T) {
	if os.Getenv("RUN_LIVE_JEV_ROUTING_TEST") != "1" {
		t.Skip("set RUN_LIVE_JEV_ROUTING_TEST=1 to call Jev and OpenAI")
	}

	openAIKey := requiredLiveEnv(t, "OPENAI_API_KEY")
	enricher, err := routing.New(openAIKey, requiredLiveEnv(t, "OPENAI_ROUTER_MODEL"))
	if err != nil {
		t.Fatalf("NewRouterEnricher: %v", err)
	}
	agent, err := NewAgent(openAIKey, requiredLiveEnv(t, "OPENAI_AGENT_MODEL"), nil)
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}
	jevClient, err := jev.New(requiredLiveEnv(t, "JEV_API_KEY"))
	if err != nil {
		t.Fatalf("jev.New: %v", err)
	}

	for _, scenario := range []struct {
		name         string
		spoken       string
		conversation session.Conversation
		wantAction   assistant.Action
		memories     []memory.Card
	}{
		{
			name:       "vague_completion_at_gym",
			spoken:     "I'm done here at the gym.",
			wantAction: assistant.ActionStateTransition,
			memories:   workoutMemories(),
		},
		{
			name:       "completion_without_named_place",
			spoken:     "I'm finished studying for now.",
			wantAction: assistant.ActionStateTransition,
			memories:   studyMemories(),
		},
		{
			name:       "remember_word_used_as_recall",
			spoken:     "Can you remember what I like to eat after workouts?",
			wantAction: assistant.ActionMemoryReview,
			memories:   workoutMemories(),
		},
		{
			name:       "generic_next_step_question",
			spoken:     "Okay, what should I do now?",
			wantAction: assistant.ActionRespond,
			memories:   studyMemories(),
		},
		{
			name:       "soft_monitoring_request",
			spoken:     "Could you keep an eye on the election results for me?",
			wantAction: assistant.ActionRespond,
		},
		{
			name:       "implied_task_not_explicit_reminder",
			spoken:     "I really need to call the dentist tomorrow.",
			wantAction: assistant.ActionRespond,
		},
		{
			name:       "one_time_check_not_monitoring",
			spoken:     "Check whether the election results are out yet.",
			wantAction: assistant.ActionRespond,
		},
		{
			name:       "location_update_with_question",
			spoken:     "I'm heading home; should I grab dinner first?",
			wantAction: assistant.ActionRespond,
			memories:   workoutMemories(),
		},
		{
			name:   "contextual_memory_review_follow_up",
			spoken: "What about food?",
			conversation: session.Conversation{Messages: []session.Message{
				{Speaker: session.SpeakerUser, Text: "What do you remember about me?"},
				{Speaker: session.SpeakerAssistant, Text: "I remember several things about your goals and preferences."},
			}},
			wantAction: assistant.ActionMemoryReview,
			memories:   workoutMemories(),
		},
	} {
		scenario := scenario
		t.Run(scenario.name, func(t *testing.T) {
			recorder := &recordingJevEvaluator{client: jevClient}
			router, err := assistant.NewJevRouter(recorder, enricher)
			if err != nil {
				t.Fatalf("NewJevRouter: %v", err)
			}
			memoryReader := &scenarioMemoryReader{cards: scenario.memories}
			service := assistant.NewService(
				router,
				agent,
				memoryReader,
				fixedConversationReader{conversation: scenario.conversation},
				noProposalConfirmer{},
			)

			ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			defer cancel()
			outcome, err := service.HandleUtterance(
				ctx,
				tool.Scope{
					UserID:    "00000000-0000-0000-0000-000000000001",
					SessionID: "00000000-0000-0000-0000-000000000002",
					TimeZone:  "America/Los_Angeles",
				},
				"00000000-0000-0000-0000-000000000003",
				scenario.spoken,
			)
			if err != nil {
				t.Fatalf("HandleUtterance: %v", err)
			}

			answer := recorder.response.Answers["route"]
			encodedJev, _ := json.MarshalIndent(answer, "", "  ")
			encodedLookup, _ := json.MarshalIndent(memoryReader.Lookup(), "", "  ")
			t.Logf("spoken: %s", scenario.spoken)
			t.Logf("Jev route answer:\n%s", encodedJev)
			t.Logf("final decision: %+v", outcome.Decision)
			t.Logf("memory lookup:\n%s", encodedLookup)
			t.Logf("model response: %q", outcome.Response)

			if outcome.Decision.Action != scenario.wantAction {
				t.Errorf(
					"action = %q, want %q; Jev confidence = %.2f",
					outcome.Decision.Action,
					scenario.wantAction,
					answer.Confidence,
				)
			}
			if strings.TrimSpace(outcome.Response) == "" {
				t.Error("model response is empty")
			} else {
				assertLiveGlassesResponse(t, outcome.Response)
			}
		})
	}
}

type recordingJevEvaluator struct {
	client   *jev.Client
	response jev.Response
}

func (r *recordingJevEvaluator) Evaluate(ctx context.Context, request jev.Request) (jev.Response, error) {
	response, err := r.client.Evaluate(ctx, request)
	r.response = response
	return response, err
}

func workoutMemories() []memory.Card {
	return []memory.Card{
		{
			Topics:  []memory.Topic{memory.TopicHealth, memory.TopicGoals},
			Kind:    memory.KindGoal,
			Title:   "Daily protein target",
			Summary: "The user aims for 150 grams of protein per day.",
		},
		{
			Topics:  []memory.Topic{memory.TopicPreferences, memory.TopicHealth},
			Kind:    memory.KindPreference,
			Title:   "Post-workout food preference",
			Summary: "The user prefers a quick chicken rice bowl after training.",
		},
	}
}

func studyMemories() []memory.Card {
	return []memory.Card{
		{
			Topics:  []memory.Topic{memory.TopicPersonal, memory.TopicGoals},
			Kind:    memory.KindGoal,
			Title:   "Submit statistics assignment",
			Summary: "The user's statistics assignment is due tomorrow afternoon.",
		},
		{
			Topics:  []memory.Topic{memory.TopicPreferences, memory.TopicPersonal},
			Kind:    memory.KindPreference,
			Title:   "Study break preference",
			Summary: "After studying, the user prefers a short walk before starting another task.",
		},
	}
}

type fixedConversationReader struct {
	conversation session.Conversation
}

func (r fixedConversationReader) Prepare(
	context.Context,
	tool.Scope,
	string,
	string,
) (session.Conversation, error) {
	return r.conversation, nil
}
