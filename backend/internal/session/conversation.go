package session

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/rube11/rev-eyes/backend/internal/tool"
	tiktoken "github.com/tiktoken-go/tokenizer"
)

const (
	maxConversationTokens    = 8_000
	recentConversationTokens = 2_000
	messageTokenOverhead     = 4
)

var (
	ErrConversationStoreRequired = errors.New("conversation store is required")
	ErrCompactorRequired         = errors.New("conversation compactor is required")
)

// Message is one finalized user or assistant transcript entry.
type Message struct {
	ID      string
	Speaker Speaker
	Text    string
}

// Conversation is the compacted summary followed by the recent transcript.
type Conversation struct {
	Summary  string
	Messages []Message
}

type ConversationStore interface {
	LoadConversation(context.Context, tool.Scope, string) (Conversation, error)
	SaveConversationSummary(context.Context, tool.Scope, string, string) error
}

type Compactor interface {
	Compact(context.Context, Conversation) (string, error)
}

// ConversationManager keeps recent transcript context within a fixed budget.
type ConversationManager struct {
	store        ConversationStore
	compactor    Compactor
	countTokens  func(string) (int, error)
	maxTokens    int
	recentTokens int
}

func NewConversationManager(
	store ConversationStore,
	compactor Compactor,
) (*ConversationManager, error) {
	if store == nil {
		return nil, ErrConversationStoreRequired
	}
	if compactor == nil {
		return nil, ErrCompactorRequired
	}
	codec, err := tiktoken.Get(tiktoken.O200kBase)
	if err != nil {
		return nil, fmt.Errorf("initialize conversation tokenizer: %w", err)
	}

	return &ConversationManager{
		store:     store,
		compactor: compactor,
		countTokens: func(text string) (int, error) {
			tokens, _, err := codec.Encode(text)
			return len(tokens), err
		},
		maxTokens:    maxConversationTokens,
		recentTokens: recentConversationTokens,
	}, nil
}

// Prepare loads prior turns and compacts the oldest ones when the budget is
// crossed. currentUtteranceID is excluded because the caller supplies its
// routed text separately to the agent. Its result remains usable on error.
func (m *ConversationManager) Prepare(
	ctx context.Context,
	scope tool.Scope,
	currentUtteranceID string,
	currentText string,
) (Conversation, error) {
	conversation, err := m.store.LoadConversation(ctx, scope, currentUtteranceID)
	if err != nil {
		return Conversation{}, err
	}

	tokens, err := m.count(conversation, currentText)
	if err != nil {
		return conversation, err
	}
	if tokens < m.maxTokens {
		return conversation, nil
	}

	cut, err := m.compactionCut(conversation.Messages, currentText)
	if err != nil || cut == 0 {
		return conversation, err
	}

	older := Conversation{
		Summary:  conversation.Summary,
		Messages: conversation.Messages[:cut],
	}
	summary, err := m.compactor.Compact(ctx, older)
	if err != nil {
		return conversation, fmt.Errorf("compact conversation: %w", err)
	}
	summary = strings.TrimSpace(summary)
	if summary == "" {
		return conversation, errors.New("compact conversation: empty summary")
	}

	throughID := older.Messages[len(older.Messages)-1].ID
	compacted := Conversation{Summary: summary, Messages: conversation.Messages[cut:]}
	return compacted, m.store.SaveConversationSummary(ctx, scope, throughID, summary)
}

func (m *ConversationManager) count(conversation Conversation, currentText string) (int, error) {
	total, err := m.messageTokens("user", currentText)
	if err != nil {
		return 0, err
	}
	if conversation.Summary != "" {
		tokens, err := m.messageTokens("summary", conversation.Summary)
		if err != nil {
			return 0, err
		}
		total += tokens
	}
	for _, message := range conversation.Messages {
		tokens, err := m.messageTokens(string(message.Speaker), message.Text)
		if err != nil {
			return 0, err
		}
		total += tokens
	}
	return total, nil
}

func (m *ConversationManager) compactionCut(messages []Message, currentText string) (int, error) {
	tokens, err := m.messageTokens("user", currentText)
	if err != nil {
		return 0, err
	}

	cut := len(messages)
	for cut > 0 {
		cut--
		messageTokens, err := m.messageTokens(
			string(messages[cut].Speaker),
			messages[cut].Text,
		)
		if err != nil {
			return 0, err
		}
		tokens += messageTokens
		if tokens >= m.recentTokens {
			break
		}
	}
	if cut > 0 && messages[cut].Speaker == SpeakerAssistant {
		cut--
	}
	return cut, nil
}

func (m *ConversationManager) messageTokens(role, text string) (int, error) {
	tokens, err := m.countTokens(role + "\n" + text)
	if err != nil {
		return 0, fmt.Errorf("count conversation tokens: %w", err)
	}
	// Local tokenizers do not include the API's role and message boundaries.
	return tokens + messageTokenOverhead, nil
}
