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
) (string, error) {
	lookup := decision.MemoryLookup
	if strings.TrimSpace(lookup.Query) == "" && strings.TrimSpace(decision.Query) != "" {
		lookup.Query = decision.Query
	}
	cards, err := s.memories.Review(ctx, scope, lookup)
	if err != nil {
		return "", fmt.Errorf("review memories: %w", err)
	}
	return formatMemoryReview(cards), nil
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

func formatMemoryReview(cards []memory.Card) string {
	if len(cards) == 0 {
		return "I don't have any active memories matching that."
	}
	if len(cards) == 1 {
		return "I remember: " + memoryReviewLine(cards[0], 320)
	}

	shown := min(len(cards), 3)
	var response strings.Builder
	fmt.Fprintf(&response, "I found %d matching memories:", len(cards))
	for index := 0; index < shown; index++ {
		fmt.Fprintf(&response, "\n%d. %s", index+1, memoryReviewLine(cards[index], 90))
	}
	if len(cards) > shown {
		response.WriteString("\nAsk for a narrower topic to see the rest.")
	} else {
		response.WriteString("\nName one to correct or forget it.")
	}
	return response.String()
}

func memoryReviewLine(card memory.Card, maxRunes int) string {
	title := strings.TrimSpace(card.Title)
	summary := strings.TrimSpace(card.Summary)
	line := summary
	if title != "" && !strings.EqualFold(title, summary) {
		line = title + " — " + summary
	}
	runes := []rune(line)
	if len(runes) <= maxRunes {
		return line
	}
	return strings.TrimSpace(string(runes[:maxRunes-1])) + "…"
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
