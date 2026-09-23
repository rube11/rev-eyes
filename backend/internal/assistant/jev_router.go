package assistant

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"github.com/rube11/rev-eyes/backend/internal/assistant/jev"
	"github.com/rube11/rev-eyes/backend/internal/memory"
	"github.com/rube11/rev-eyes/backend/internal/session"
	"github.com/rube11/rev-eyes/backend/internal/speech"
)

const (
	jevRouteQuestion           = "route"
	jevMemoryReviewAllQuestion = "memory_review_all"
	jevMemorySignalThreshold   = 0.5
)

var ErrJevEvaluatorRequired = errors.New("Jev evaluator is required")

// JevEvaluator is the part of the Jev client needed by the routing layer.
type JevEvaluator interface {
	Evaluate(context.Context, jev.Request) (jev.Response, error)
}

// JevRouter asks one set of independent, typed questions over the turn. Jev
// selects the route and supplies reusable retrieval judgments; code owns how
// those answers become a bounded decision.
type JevRouter struct {
	evaluator JevEvaluator
}

func NewJevRouter(evaluator JevEvaluator) (*JevRouter, error) {
	if evaluator == nil {
		return nil, ErrJevEvaluatorRequired
	}
	return &JevRouter{evaluator: evaluator}, nil
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

	questions := jevMemoryQuestions()
	criteria := jevRouteCriteria()
	if conversation.Speech.ContextOnly() {
		for name := range criteria {
			if name != string(ActionSuggestTip) && name != string(ActionIgnore) && name != string(ActionStateUpdate) {
				delete(criteria, name)
			}
		}
	}
	questions[jevRouteQuestion] = jev.Question{
		Type: jev.QuestionChoice,
		Instructions: "Which single action should the wearable assistant take for `latest_utterance`? " +
			"Classify only that utterance. Use `recent_dialogue` only to resolve references " +
			"and conversational repairs, never as a new request. Reminder, watch, search, and " +
			"location requests use respond; a later Jev workflow exclusively selects tools. " +
			"When speech_attribution is supplied, self means the wearer, other means an unidentified other person, and unknown means unattributed. These are fallible audio labels, not identities. " +
			"Other or mixed speech is conversation context, not a request from the wearer: choose suggest_tip only for a timely, useful private tip to the wearer; otherwise ignore or state_update. Never infer another person's facts belong to the wearer.",
		Criteria: criteria,
	}
	response, err := r.evaluator.Evaluate(ctx, jev.Request{
		State:     jevRouteState(utterance, conversation),
		Questions: questions,
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

	return decisionFromJev(action, utterance, response), nil
}

func jevMemoryQuestions() map[string]jev.Question {
	questions := make(map[string]jev.Question, len(memory.TopicValues())+len(memory.KindValues())+1)
	for _, value := range memory.TopicValues() {
		questions[memoryTopicQuestion(value)] = jev.Question{
			Type: jev.QuestionNoul,
			Instructions: fmt.Sprintf(
				"Assume the assistant will give a helpful response to `latest_utterance`. Would saved memories tagged with topic %q be among the most directly useful personal context? Judge retrieval usefulness, not whether the word appears. Use `recent_dialogue` only to resolve what the latest utterance refers to.",
				value,
			),
			Criteria: map[string]string{
				"true":  "This topic is directly likely to contain personal context that materially improves the current result.",
				"false": "This topic is unrelated, only remotely possible, or would add noise.",
			},
		}
	}
	for _, value := range memory.KindValues() {
		questions[memoryKindQuestion(value)] = jev.Question{
			Type: jev.QuestionNoul,
			Instructions: fmt.Sprintf(
				"Assume the assistant will give a helpful response to `latest_utterance`. Would saved memories of kind %q be among the most directly useful personal context? Judge retrieval usefulness, not the grammatical form of the utterance. Use `recent_dialogue` only to resolve references.",
				value,
			),
			Criteria: map[string]string{
				"true":  "This kind is directly likely to contain personal context that materially improves the current result.",
				"false": "This kind is unrelated, only remotely possible, or would add noise.",
			},
		}
	}
	questions[jevMemoryReviewAllQuestion] = jev.Question{
		Type:         jev.QuestionNoul,
		Instructions: "Does `latest_utterance` ask to review everything the assistant remembers about the user, rather than memories about one subject?",
		Criteria: map[string]string{
			"true":  "The user asks broadly what is known or remembered about them overall.",
			"false": "The request is about one person, subject, preference, event, goal, or other specific area.",
		},
	}
	return questions
}

func decisionFromJev(action Action, utterance string, response jev.Response) Decision {
	if action == ActionIgnore || action == ActionStateUpdate {
		return Decision{Action: action}
	}
	query := strings.TrimSpace(utterance)
	deictic := isIncompleteMemoryReference(action, query)
	if deictic {
		query = ""
	}
	decision := Decision{Action: action, Query: query}
	if action == ActionMemoryReview {
		decision.MemoryReviewAll = response.Answers[jevMemoryReviewAllQuestion].Noul >= jevMemorySignalThreshold
	}
	if usesMemoryLookup(action) && !deictic {
		decision.MemoryLookup = memory.Lookup{
			Query:  decision.Query,
			Topics: selectedMemoryTopics(response),
			Kinds:  selectedMemoryKinds(response),
		}
	}
	return normalizeDecision(decision)
}

func isIncompleteMemoryReference(action Action, utterance string) bool {
	normalized := strings.ToLower(strings.TrimSpace(utterance))
	normalized = strings.Trim(normalized, " .,!?:;")
	switch action {
	case ActionMemoryCorrect:
		switch normalized {
		case "that's wrong", "that is wrong", "that's incorrect", "that is incorrect",
			"that's not right", "that is not right", "wrong", "incorrect":
			return true
		}
	case ActionMemoryForget:
		switch normalized {
		case "forget that", "forget this", "forget it", "remove that", "remove this",
			"delete that", "delete this":
			return true
		}
	case ActionProfileInclude, ActionProfileExclude:
		words := strings.Fields(normalized)
		for _, pronoun := range []string{"that", "this", "it"} {
			for _, word := range words {
				if word == pronoun && len(words) <= 8 {
					return true
				}
			}
		}
	}
	return false
}

func usesMemoryLookup(action Action) bool {
	switch action {
	case ActionRespond, ActionSuggestTip, ActionStateTransition, ActionMemoryReview,
		ActionMemoryForget, ActionProfileInclude, ActionProfileExclude:
		return true
	default:
		return false
	}
}

type memorySignal[T ~string] struct {
	value T
	score float64
}

func selectedMemoryTopics(response jev.Response) []memory.Topic {
	signals := make([]memorySignal[memory.Topic], 0, len(memory.TopicValues()))
	for _, value := range memory.TopicValues() {
		signals = append(signals, memorySignal[memory.Topic]{
			value: memory.Topic(value), score: response.Answers[memoryTopicQuestion(value)].Noul,
		})
	}
	selected := selectMemorySignals(signals, 3)
	if len(selected) == 0 {
		return nil
	}
	return selected
}

func selectedMemoryKinds(response jev.Response) []memory.Kind {
	signals := make([]memorySignal[memory.Kind], 0, len(memory.KindValues()))
	for _, value := range memory.KindValues() {
		signals = append(signals, memorySignal[memory.Kind]{
			value: memory.Kind(value), score: response.Answers[memoryKindQuestion(value)].Noul,
		})
	}
	return selectMemorySignals(signals, 3)
}

func selectMemorySignals[T ~string](signals []memorySignal[T], limit int) []T {
	sort.SliceStable(signals, func(left, right int) bool { return signals[left].score > signals[right].score })
	selected := make([]T, 0, min(limit, len(signals)))
	for _, signal := range signals {
		if signal.score < jevMemorySignalThreshold || len(selected) == limit {
			break
		}
		selected = append(selected, signal.value)
	}
	return selected
}

func memoryTopicQuestion(value string) string { return "memory_topic_" + value }
func memoryKindQuestion(value string) string  { return "memory_kind_" + value }

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
			"when":     "A direct factual question or command that needs an answer or tool, including search, location, reminders, and ongoing public monitoring, and is not advice, coaching, or a memory or profile operation below.",
			"includes": []string{"What time does the meeting start?", "Remind me tomorrow at nine.", "Tell me when the strike ends."},
			"excludes": "Requests for practical advice use suggest_tip. Also excludes bare completed-activity statements, memory management, and profile controls.",
		},
		string(ActionSuggestTip): map[string]any{
			"when":     "The user asks for advice, coaching, an idea, or a practical way to make their current situation easier or better; or describes a concrete current difficulty where one small practical suggestion is the natural helpful response. The desired answer is one tip grounded in conversation and personal context.",
			"includes": []string{"Any tip for staying focused?", "How can I make this easier?", "I keep checking my phone every few minutes while I study."},
			"excludes": "Factual questions and external actions use respond. Mere venting, emotional disclosure, social conversation, and ordinary narration without an actionable difficulty do not invite a tip. Completed workout or school milestones use state_transition.",
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
	Speech          *speech.Utterance `json:"speech_attribution,omitempty"`
	RecentDialogue  []ResponseTurn    `json:"recent_dialogue,omitempty"`
	LatestUtterance string            `json:"latest_utterance"`
}

func jevRouteState(utterance string, conversation session.Conversation) jevState {
	return jevState{Speech: conversation.Speech, RecentDialogue: BoundedRoutingDialogue(conversation), LatestUtterance: utterance}
}
