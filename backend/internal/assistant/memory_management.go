package assistant

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/rube11/rev-eyes/backend/internal/memory"
	"github.com/rube11/rev-eyes/backend/internal/session"
	"github.com/rube11/rev-eyes/backend/internal/tool"
)

const (
	memoryCorrectionQuestion = "Please say the complete corrected fact, like: Change my protein target to 150 grams."
	memoryForgetQuestion     = "Which memory should I forget?"
)

func (s *Service) reviewMemories(
	ctx context.Context,
	scope tool.Scope,
	decision Decision,
	utterance string,
	conversation session.Conversation,
) (string, error) {
	lookup := decision.MemoryLookup
	if decision.MemoryReviewAll {
		lookup = memory.Lookup{}
	} else if strings.TrimSpace(lookup.Query) == "" && strings.TrimSpace(decision.Query) != "" {
		lookup.Query = decision.Query
	}
	cards, err := s.memories.Review(ctx, scope, lookup)
	if err != nil {
		return "", fmt.Errorf("review memories: %w", err)
	}
	// One bounded profile fallback prevents wording differences from being
	// mistaken for an empty account. The agent must still answer only from facts.
	if len(cards) == 0 && (!lookup.Empty() || len(lookup.Topics) > 0 || len(lookup.Kinds) > 0) {
		cards, err = s.memories.Review(ctx, scope, memory.Lookup{})
		if err != nil {
			return "", fmt.Errorf("review memory fallback: %w", err)
		}
	}
	scope.MemoryReview = true
	conversation.Profile = s.loadProfile(ctx, scope)
	return s.agent.Respond(ctx, scope, utterance, conversation, cards)
}

func (s *Service) forgetMemory(
	ctx context.Context,
	scope tool.Scope,
	utteranceID string,
	utterance string,
	decision Decision,
) (string, bool, error) {
	lookup := decision.MemoryLookup
	if strings.TrimSpace(lookup.Query) == "" && strings.TrimSpace(decision.Query) != "" {
		lookup.Query = decision.Query
	}
	if lookup.Empty() && len(lookup.Topics) == 0 && len(lookup.Kinds) == 0 {
		conversation, err := s.conversation.Prepare(
			ctx,
			scope,
			utteranceID,
			utterance,
		)
		if err != nil {
			return "", false, fmt.Errorf("prepare contextual memory removal: %w", err)
		}
		var found bool
		lookup, found = reviewedMemoryLookup(conversation)
		if !found {
			return memoryForgetQuestion, false, nil
		}
	}

	forgotten, err := s.memories.Forget(ctx, scope, lookup)
	if errors.Is(err, memory.ErrMemoryAmbiguous) {
		return "I found more than one matching memory. Which one should I forget?", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("forget memory: %w", err)
	}
	if forgotten == 0 {
		return "I couldn't find that memory.", false, nil
	}
	return "Okay, I forgot that.", true, nil
}

func reviewedMemoryLookup(conversation session.Conversation) (memory.Lookup, bool) {
	const prefix = "I remember: "
	if len(conversation.Messages) == 0 {
		return memory.Lookup{}, false
	}
	message := conversation.Messages[len(conversation.Messages)-1]
	if message.Speaker != session.SpeakerAssistant ||
		!strings.HasPrefix(message.Text, prefix) {
		return memory.Lookup{}, false
	}
	line := strings.TrimSpace(strings.TrimPrefix(message.Text, prefix))
	if title, _, found := strings.Cut(line, " — "); found {
		line = strings.TrimSpace(title)
	}
	if line != "" {
		return memory.Lookup{Terms: []string{line}}, true
	}
	return memory.Lookup{}, false
}
