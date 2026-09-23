package assistant

import (
	"fmt"
	"strings"
	"time"

	"github.com/rube11/rev-eyes/backend/internal/memory"
	"github.com/rube11/rev-eyes/backend/internal/session"
	"github.com/rube11/rev-eyes/backend/internal/speech"
	"github.com/rube11/rev-eyes/backend/internal/tool"
)

// ResponseContext is built once, after retrieval, and shared by tool selection,
// argument preparation, and final response generation. Authentication stays in Scope.
type ResponseContext struct {
	Speech           *speech.Utterance `json:"speech_attribution,omitempty"`
	Action           Action            `json:"route"`
	Query            string            `json:"query"`
	Profile          string            `json:"profile,omitempty"`
	Memories         []memory.Card     `json:"memories,omitempty"`
	Summary          string            `json:"conversation_summary,omitempty"`
	Messages         []ResponseTurn    `json:"recent_dialogue,omitempty"`
	CurrentLocalTime string            `json:"current_local_time"`
	TimeZone         string            `json:"time_zone"`
	AlwaysRespond    bool              `json:"always_respond"`
	MemoryReview     bool              `json:"memory_review"`
}

type ResponseTurn struct {
	Speaker session.Speaker `json:"speaker"`
	Text    string          `json:"text"`
}

// BoundedRoutingDialogue returns the shared history window used by both routing
// judgments. Full response context remains unmodified.
func BoundedRoutingDialogue(conversation session.Conversation) []ResponseTurn {
	messages := conversation.Messages
	if len(messages) > 6 {
		messages = messages[len(messages)-6:]
	}
	turns := make([]ResponseTurn, 0, len(messages))
	for _, message := range messages {
		text := []rune(message.Text)
		if len(text) > 800 {
			text = text[:800]
		}
		turns = append(turns, ResponseTurn{Speaker: message.Speaker, Text: string(text)})
	}
	return turns
}

func NewResponseContext(scope tool.Scope, query string, conversation session.Conversation, memories []memory.Card, now time.Time) (ResponseContext, error) {
	location := time.UTC
	if scope.TimeZone != "" {
		var err error
		location, err = time.LoadLocation(scope.TimeZone)
		if err != nil {
			return ResponseContext{}, fmt.Errorf("load assistant time zone: %w", err)
		}
	}
	turn := ResponseContext{
		Speech: scope.Speech,
		Action: ActionRespond, Query: strings.TrimSpace(query), Profile: conversation.Profile,
		Memories: memories, Summary: conversation.Summary,
		CurrentLocalTime: now.In(location).Format(time.RFC3339), TimeZone: location.String(),
		AlwaysRespond: scope.AlwaysRespond, MemoryReview: scope.MemoryReview,
	}
	for _, message := range conversation.Messages {
		turn.Messages = append(turn.Messages, ResponseTurn{Speaker: message.Speaker, Text: message.Text})
	}
	return turn, nil
}
