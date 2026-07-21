package session

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/rube11/rev-eyes/backend/internal/tool"
)

type fakeConversationStore struct {
	conversation Conversation
	loadErr      error
	saveErr      error
	savedScope   tool.Scope
	savedThrough string
	savedSummary string
}

func (s *fakeConversationStore) LoadConversation(
	context.Context,
	tool.Scope,
	string,
) (Conversation, error) {
	return s.conversation, s.loadErr
}

func (s *fakeConversationStore) SaveConversationSummary(
	_ context.Context,
	scope tool.Scope,
	through string,
	summary string,
) error {
	s.savedScope = scope
	s.savedThrough = through
	s.savedSummary = summary
	return s.saveErr
}

type compactorFunc func(context.Context, Conversation) (string, error)

func (f compactorFunc) Compact(ctx context.Context, conversation Conversation) (string, error) {
	return f(ctx, conversation)
}

var oneToken = func(string) (int, error) {
	return 1, nil
}

func testConversationManager(
	store ConversationStore,
	compactor Compactor,
) *ConversationManager {
	return &ConversationManager{
		store:        store,
		compactor:    compactor,
		countTokens:  oneToken,
		maxTokens:    maxConversationTokens,
		recentTokens: recentConversationTokens,
	}
}

func TestConversationManagerReturnsContextBelowThreshold(t *testing.T) {
	t.Parallel()

	want := Conversation{Messages: []Message{{
		ID:      "message-1",
		Speaker: SpeakerAssistant,
		Text:    "How can I help?",
	}}}
	store := &fakeConversationStore{conversation: want}
	manager := testConversationManager(
		store,
		compactorFunc(func(context.Context, Conversation) (string, error) {
			t.Fatal("Compact() called below threshold")
			return "", nil
		}),
	)
	manager.maxTokens = 20

	got, err := manager.Prepare(
		context.Background(),
		tool.Scope{},
		"current",
		"hello",
	)
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Prepare() = %#v, want %#v", got, want)
	}
	if store.savedThrough != "" {
		t.Fatalf("saved through = %q", store.savedThrough)
	}
}

func TestConversationManagerCompactsOldestMessages(t *testing.T) {
	t.Parallel()

	scope := tool.Scope{UserID: "user-1", SessionID: "session-1"}
	conversation := Conversation{
		Summary: "Existing handoff.",
		Messages: []Message{
			{ID: "message-1", Speaker: SpeakerUser, Text: "old question"},
			{ID: "message-2", Speaker: SpeakerAssistant, Text: "old answer"},
			{ID: "message-3", Speaker: SpeakerUser, Text: "recent question"},
			{ID: "message-4", Speaker: SpeakerAssistant, Text: "recent answer"},
		},
	}
	store := &fakeConversationStore{conversation: conversation}
	manager := testConversationManager(
		store,
		compactorFunc(func(_ context.Context, got Conversation) (string, error) {
			want := Conversation{
				Summary:  conversation.Summary,
				Messages: conversation.Messages[:2],
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("Compact() conversation = %#v, want %#v", got, want)
			}
			return " Updated handoff. ", nil
		}),
	)
	manager.maxTokens = 30
	manager.recentTokens = 10

	got, err := manager.Prepare(context.Background(), scope, "current", "follow up")
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	want := Conversation{
		Summary:  "Updated handoff.",
		Messages: conversation.Messages[2:],
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Prepare() = %#v, want %#v", got, want)
	}
	if store.savedScope != scope ||
		store.savedThrough != "message-2" ||
		store.savedSummary != want.Summary {
		t.Fatalf("saved scope = %#v, through = %q, summary = %q", store.savedScope, store.savedThrough, store.savedSummary)
	}
}

func TestConversationManagerKeepsOriginalContextWhenCompactionFails(t *testing.T) {
	t.Parallel()

	want := Conversation{Messages: []Message{
		{ID: "message-1", Speaker: SpeakerUser, Text: "old question"},
		{ID: "message-2", Speaker: SpeakerAssistant, Text: "old answer"},
		{ID: "message-3", Speaker: SpeakerUser, Text: "recent question"},
	}}
	compactErr := errors.New("model unavailable")
	manager := testConversationManager(
		&fakeConversationStore{conversation: want},
		compactorFunc(func(context.Context, Conversation) (string, error) {
			return "", compactErr
		}),
	)
	manager.maxTokens = 5
	manager.recentTokens = 5

	got, err := manager.Prepare(context.Background(), tool.Scope{}, "current", "hello")
	if !errors.Is(err, compactErr) {
		t.Fatalf("Prepare() error = %v, want wrapped compaction error", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Prepare() = %#v, want original %#v", got, want)
	}
}

func TestConversationManagerRequiresDependencies(t *testing.T) {
	t.Parallel()

	store := &fakeConversationStore{}
	compactor := compactorFunc(func(context.Context, Conversation) (string, error) {
		return "summary", nil
	})

	if _, err := NewConversationManager(nil, compactor); !errors.Is(err, ErrConversationStoreRequired) {
		t.Fatalf("NewConversationManager(nil store) error = %v", err)
	}
	if _, err := NewConversationManager(store, nil); !errors.Is(err, ErrCompactorRequired) {
		t.Fatalf("NewConversationManager(nil compactor) error = %v", err)
	}
}

func TestConversationManagerCountsText(t *testing.T) {
	t.Parallel()

	manager, err := NewConversationManager(
		&fakeConversationStore{},
		compactorFunc(func(context.Context, Conversation) (string, error) {
			return "summary", nil
		}),
	)
	if err != nil {
		t.Fatalf("NewConversationManager() error = %v", err)
	}
	tokens, err := manager.countTokens("hello world")
	if err != nil {
		t.Fatalf("Count() error = %v", err)
	}
	if tokens <= 0 {
		t.Fatalf("Count() = %d", tokens)
	}
}
