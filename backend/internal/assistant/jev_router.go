package assistant

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/rube11/rev-eyes/backend/internal/assistant/jev"
	"github.com/rube11/rev-eyes/backend/internal/session"
)

const jevRouteQuestion = "route"

var (
	ErrJevEvaluatorRequired     = errors.New("Jev evaluator is required")
	ErrDecisionEnricherRequired = errors.New("decision enricher is required")
)

// JevEvaluator is the part of the Jev client needed by the routing layer.
type JevEvaluator interface {
	Evaluate(context.Context, jev.Request) (jev.Response, error)
}

// DecisionEnricher plans retrieval inputs after Jev has selected the action.
// It cannot change which action the application executes.
type DecisionEnricher interface {
	Enrich(context.Context, Action, string, session.Conversation) (Decision, error)
}

// JevRouter uses Jev as the sole intent classifier, then enriches the fixed
// action with query and memory-retrieval inputs where needed.
type JevRouter struct {
	evaluator JevEvaluator
	enricher  DecisionEnricher
}

func NewJevRouter(evaluator JevEvaluator, enricher DecisionEnricher) (*JevRouter, error) {
	if evaluator == nil {
		return nil, ErrJevEvaluatorRequired
	}
	if enricher == nil {
		return nil, ErrDecisionEnricherRequired
	}
	return &JevRouter{evaluator: evaluator, enricher: enricher}, nil
}

func (r *JevRouter) Route(ctx context.Context, utterance string) (Decision, error) {
	return r.RouteWithContext(ctx, utterance, session.Conversation{})
}

func (r *JevRouter) RouteWithContext(
	ctx context.Context,
	utterance string,
	conversation session.Conversation,
) (Decision, error) {
	utterance = strings.TrimSpace(utterance)
	if shouldIgnore(utterance) {
		return Decision{Action: ActionIgnore}, nil
	}

	response, err := r.evaluator.Evaluate(ctx, jev.Request{
		State: jevRouteState(utterance, conversation),
		Questions: map[string]jev.Question{
			jevRouteQuestion: {
				Type: jev.QuestionChoice,
				Instructions: "Which single action should the wearable assistant take for `latest_utterance`? " +
					"Classify only that utterance. Use `recent_dialogue` only to resolve references " +
					"and conversational repairs, never as a new request. Reminder, watch, search, and " +
					"location requests use respond; a later Jev workflow exclusively selects tools.",
				Criteria: jevRouteCriteria(),
			},
		},
	})
	if err != nil {
		return Decision{}, fmt.Errorf("evaluate Jev route: %w", err)
	}

	answer, ok := response.Answers[jevRouteQuestion]
	if !ok {
		return Decision{}, errors.New("Jev response omitted route answer")
	}
	action := Action(answer.Choice)
	if !isRoutableAction(action) {
		return Decision{}, fmt.Errorf("unsupported Jev route %q", answer.Choice)
	}
	slog.InfoContext(ctx, "Jev route", "action", action, "confidence", answer.Confidence)

	if action == ActionIgnore || action == ActionStateUpdate {
		return Decision{Action: action}, nil
	}

	decision, err := r.enricher.Enrich(ctx, action, utterance, conversation)
	if err != nil {
		return Decision{}, fmt.Errorf("enrich %s decision: %w", action, err)
	}
	decision.Action = action
	return normalizeDecision(decision), nil
}

func isRoutableAction(action Action) bool {
	if action == "propose_task" || action == "propose_watch" {
		return false
	}
	return validateDecision(Decision{Action: action}).Action == action
}

func jevRouteCriteria() map[string]any {
	return map[string]any{
		string(ActionIgnore): map[string]any{
			"when":     "Background speech, filler, incidental narration, overheard conversation, or an ordinary factual statement that asks the assistant for nothing.",
			"includes": []string{"The meeting starts at three.", "Maya works in accounting."},
			"excludes": "Questions, commands, explicit memory operations, and first-person activity changes.",
		},
		string(ActionRespond): map[string]any{
			"when":     "A direct question or command that needs an answer or tool, including search, location, reminders, and ongoing public monitoring, and is not a memory or profile operation below.",
			"includes": []string{"What time does the meeting start?", "Remind me tomorrow at nine.", "Tell me when the strike ends."},
			"excludes": "Bare completed-activity statements, memory management, and profile controls.",
		},
		string(ActionStateUpdate): map[string]any{
			"when":     "A first-person immediate update that the user is starting, entering, or changing a current activity or place, with no request for an answer.",
			"includes": []string{"I just arrived at the gym.", "I'm walking into the client meeting now."},
			"excludes": "Departures and completed workouts, exams, or study sessions.",
		},
		string(ActionStateTransition): map[string]any{
			"when":     "A bare statement, not a question or command, that the user just completed a workout or school milestone and one short next step may help.",
			"includes": []string{"I just left the gym.", "I finished my exam."},
			"excludes": "A question about the completed activity uses respond.",
		},
		string(ActionRemember): map[string]any{
			"when":     "An explicit request to remember a durable fact or preference.",
			"includes": []string{"Remember that Maya is my manager."},
			"excludes": "Never infer a memory-save request from an ordinary statement.",
		},
		string(ActionMemoryReview): map[string]any{
			"when":     "A request to inspect what the assistant remembers about the user, a person, or a topic.",
			"includes": []string{"What do you remember about Jolene?", "What do you know about me?"},
		},
		string(ActionMemoryCorrect): map[string]any{
			"when":     "The user says a remembered detail is wrong or supplies its replacement.",
			"includes": []string{"That's wrong.", "Change my protein target to 150 grams."},
		},
		string(ActionMemoryForget): map[string]any{
			"when":     "An explicit request to remove a remembered detail.",
			"includes": []string{"Forget that.", "Forget where I work."},
		},
		string(ActionProfileInclude): map[string]any{
			"when":     "An explicit request to prioritize an existing saved memory in the always-present profile.",
			"includes": []string{"Always keep my protein target in mind."},
			"excludes": "A new fact to save uses remember.",
		},
		string(ActionProfileExclude): map[string]any{
			"when":     "An explicit request to exclude an existing fact from the always-present profile while keeping it saved and searchable.",
			"includes": []string{"Don't include my weight in my profile."},
			"excludes": "A request to delete the fact uses memory_forget.",
		},
	}
}

type jevState struct {
	RecentDialogue  []ResponseTurn `json:"recent_dialogue,omitempty"`
	LatestUtterance string         `json:"latest_utterance"`
}

func jevRouteState(utterance string, conversation session.Conversation) jevState {
	return jevState{RecentDialogue: BoundedRoutingDialogue(conversation), LatestUtterance: utterance}
}
