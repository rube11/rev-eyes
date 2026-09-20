package openai

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	"github.com/rube11/rev-eyes/backend/internal/memory"
	"github.com/rube11/rev-eyes/backend/internal/session"
	"github.com/rube11/rev-eyes/backend/internal/tool"
)

func requiredLiveEnv(t *testing.T, name string) string {
	t.Helper()
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		t.Fatalf("%s is required for the live integration test", name)
	}
	return value
}

func assertLiveGlassesResponse(t *testing.T, response string) {
	t.Helper()
	if count := utf8.RuneCountInString(response); count > 420 {
		t.Errorf("live response is too long for glasses: %d characters", count)
	}
	numberedLines := 0
	for _, line := range strings.Split(response, "\n") {
		line = strings.TrimSpace(line)
		if len(line) >= 3 && line[0] >= '1' && line[0] <= '9' && line[1] == '.' && line[2] == ' ' {
			numberedLines++
		}
	}
	if numberedLines > 3 {
		t.Errorf("live response has %d numbered lines, want at most 3", numberedLines)
	}
}

type liveSearchArguments struct {
	Query          string   `json:"query"`
	Mode           string   `json:"mode"`
	Topic          string   `json:"topic"`
	Recency        string   `json:"recency"`
	IncludeDomains []string `json:"include_domains"`
}

type scenarioMemoryReader struct {
	cards  []memory.Card
	mu     sync.Mutex
	lookup memory.Lookup
}

func (r *scenarioMemoryReader) Find(_ context.Context, _ tool.Scope, lookup memory.Lookup) ([]memory.Card, error) {
	r.mu.Lock()
	r.lookup = lookup
	r.mu.Unlock()
	return append([]memory.Card(nil), r.cards...), nil
}

func (r *scenarioMemoryReader) Review(ctx context.Context, scope tool.Scope, lookup memory.Lookup) ([]memory.Card, error) {
	return r.Find(ctx, scope, lookup)
}

func (r *scenarioMemoryReader) Forget(context.Context, tool.Scope, memory.Lookup) (int, error) {
	return 0, nil
}

func (r *scenarioMemoryReader) Lookup() memory.Lookup {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.lookup
}

func (r *scenarioMemoryReader) Profile(context.Context, tool.Scope) (string, error) {
	return "", nil
}

func (r *scenarioMemoryReader) SetProfileOverride(context.Context, tool.Scope, memory.Lookup, memory.ProfileLayer) (int, error) {
	return 0, nil
}

type emptyConversationReader struct{}

func (emptyConversationReader) Prepare(context.Context, tool.Scope, string, string) (session.Conversation, error) {
	return session.Conversation{}, nil
}

type noProposalConfirmer struct{}

func (noProposalConfirmer) Confirm(context.Context, tool.Scope, string) (string, bool, error) {
	return "", false, nil
}
