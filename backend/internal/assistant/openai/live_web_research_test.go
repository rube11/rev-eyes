package openai

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/rube11/rev-eyes/backend/internal/assistant"
	"github.com/rube11/rev-eyes/backend/internal/memory"
	"github.com/rube11/rev-eyes/backend/internal/session"
	"github.com/rube11/rev-eyes/backend/internal/tool"
	"github.com/rube11/rev-eyes/backend/internal/tool/websearch"
)

// TestLiveEyesWebResearchScenarios exercises the production Eyes orchestration
// boundary. Each spoken request goes through the live router, assistant.Service,
// injected fake memory retrieval, the live agent, and the real Tavily tool.
// It is intentionally opt-in because it spends OpenAI and Tavily credits.
func TestLiveEyesWebResearchScenarios(t *testing.T) {
	if os.Getenv("RUN_LIVE_WEB_RESEARCH_EVAL") != "1" {
		t.Skip("set RUN_LIVE_WEB_RESEARCH_EVAL=1 to call OpenAI and Tavily")
	}

	openAIKey := requiredLiveEnv(t, "OPENAI_API_KEY")
	classify, err := NewClassifier(
		openAIKey,
		requiredLiveEnv(t, "OPENAI_ROUTER_MODEL"),
	)
	if err != nil {
		t.Fatalf("NewClassifier() error = %v", err)
	}

	for _, scenario := range liveEyesWebScenarios() {
		scenario := scenario
		t.Run(scenario.name, func(t *testing.T) {
			searcher, err := websearch.New(requiredLiveEnv(t, "TAVILY_API_KEY"))
			if err != nil {
				t.Fatalf("websearch.New() error = %v", err)
			}
			recordedSearch := &recordingSearchTool{delegate: searcher}
			registry := tool.NewRegistry()
			if err := registry.Register(recordedSearch); err != nil {
				t.Fatalf("Register(search_web) error = %v", err)
			}
			executor, err := tool.NewExecutor(registry)
			if err != nil {
				t.Fatalf("tool.NewExecutor() error = %v", err)
			}
			agent, err := NewAgent(
				openAIKey,
				requiredLiveEnv(t, "OPENAI_AGENT_MODEL"),
				registry,
				executor,
			)
			if err != nil {
				t.Fatalf("NewAgent() error = %v", err)
			}

			memoryReader := &scenarioMemoryReader{cards: scenario.memories}
			service, err := assistant.NewService(
				assistant.NewRouter(classify),
				agent,
				memoryReader,
				emptyConversationReader{},
				noProposalConfirmer{},
			)
			if err != nil {
				t.Fatalf("assistant.NewService() error = %v", err)
			}

			for _, card := range scenario.memories {
				if err := card.Normalize().Validate(); err != nil {
					t.Fatalf("invalid fake memory %q: %v", card.Title, err)
				}
			}

			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
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
				t.Fatalf("HandleUtterance() error = %v", err)
			}
			if outcome.Decision.Action != assistant.ActionRespond {
				t.Fatalf("router action = %q, want respond", outcome.Decision.Action)
			}
			if strings.TrimSpace(outcome.Response) == "" {
				t.Fatal("Eyes response is empty")
			}

			calls := recordedSearch.Calls()
			if len(calls) == 0 {
				t.Fatal("Eyes did not call search_web")
			}

			lookup := memoryReader.Lookup()
			encodedLookup, _ := json.MarshalIndent(lookup, "", "  ")
			encodedMemories, _ := json.MarshalIndent(scenario.memories, "", "  ")
			encodedCalls, _ := json.MarshalIndent(calls, "", "  ")
			encodedErrors, _ := json.MarshalIndent(recordedSearch.Errors(), "", "  ")
			t.Logf("spoken:\n%s", scenario.spoken)
			t.Logf("Eyes router decision:\n%+v", outcome.Decision)
			t.Logf("Eyes memory lookup:\n%s", encodedLookup)
			t.Logf("fake memories returned to Eyes:\n%s", encodedMemories)
			t.Logf("search_web calls chosen by Eyes:\n%s", encodedCalls)
			t.Logf("search_web errors returned to Eyes:\n%s", encodedErrors)
			t.Logf("Eyes response:\n%s", outcome.Response)
			t.Logf("glasses preview (%d runes):\n%s", utf8.RuneCountInString(outcome.Response), glassesPreview(outcome.Response))
			assertLiveSearchPlan(t, calls, scenario)
		})
	}
}

type liveEyesWebScenario struct {
	name               string
	spoken             string
	memories           []memory.Card
	wantMode           string
	wantTopic          string
	wantRecency        string
	wantQueryAll       []string
	wantQueryAnyGroups [][]string
}

func liveEyesWebScenarios() []liveEyesWebScenario {
	return []liveEyesWebScenario{
		{
			name:   "las_vegas_restaurants_for_jolene",
			spoken: "find some restaurants for me and jolene in las vegas",
			memories: []memory.Card{
				{
					Topics:  []memory.Topic{memory.TopicPreferences, memory.TopicRelationships},
					Kind:    memory.KindPreference,
					Title:   "Dining budget with Jolene",
					Summary: "The user wants to spend around $60 total for lunch or dinner with Jolene.",
					Entities: []memory.Entity{
						{Type: memory.EntityPerson, Name: "Jolene"},
					},
				},
				{
					Topics:  []memory.Topic{memory.TopicPreferences, memory.TopicRelationships},
					Kind:    memory.KindRelationship,
					Title:   "Jolene's favorite food",
					Summary: "Jolene likes Chinese and Vietnamese food, especially ramen, dim sum, and pho; these are Jolene's preferences, not the user's.",
					Entities: []memory.Entity{
						{Type: memory.EntityPerson, Name: "Jolene"},
					},
				},
			},
			wantMode:     "research",
			wantTopic:    "general",
			wantRecency:  "none",
			wantQueryAll: []string{"las vegas", "60"},
			wantQueryAnyGroups: [][]string{
				{"chinese", "vietnamese", "ramen", "dim sum", "pho"},
			},
		},
		{
			name:   "official_red_rock_entry_guidance",
			spoken: "im thinking of driving through red rock canyon tomorrow morning do i need a reservation right now",
			memories: []memory.Card{
				{
					Topics:  []memory.Topic{memory.TopicPlaces, memory.TopicPersonal},
					Kind:    memory.KindEvent,
					Title:   "Las Vegas trip",
					Summary: "The user is currently visiting Las Vegas and has a rental car.",
					Entities: []memory.Entity{
						{Type: memory.EntityPlace, Name: "Las Vegas"},
					},
				},
			},
			wantMode:     "",
			wantTopic:    "general",
			wantRecency:  "none",
			wantQueryAll: []string{"red rock", "reservation"},
		},
		{
			name:   "tonight_in_vegas_with_mateo",
			spoken: "whats something me and mateo could actually do in vegas tonight under 100 each",
			memories: []memory.Card{
				{
					Topics:  []memory.Topic{memory.TopicPreferences, memory.TopicRelationships},
					Kind:    memory.KindRelationship,
					Title:   "Mateo likes comedy and live music",
					Summary: "Mateo enjoys stand-up comedy and live music, but neither Mateo nor the user wants a nightclub.",
					Entities: []memory.Entity{
						{Type: memory.EntityPerson, Name: "Mateo"},
					},
				},
			},
			wantMode:     "research",
			wantTopic:    "general",
			wantQueryAll: []string{"las vegas", "100"},
			wantQueryAnyGroups: [][]string{
				{"comedy", "live music"},
				{"tonight", time.Now().Format("2006-01-02"), time.Now().Format("January 2")},
			},
		},
		{
			name:   "confirmed_raiders_news",
			spoken: "did the raiders make any real roster moves this week that i should know about",
			memories: []memory.Card{
				{
					Topics:  []memory.Topic{memory.TopicPreferences, memory.TopicPersonal},
					Kind:    memory.KindPreference,
					Title:   "Follows the Las Vegas Raiders",
					Summary: "The user follows the Las Vegas Raiders and prefers confirmed roster news over rumors.",
					Entities: []memory.Entity{
						{Type: memory.EntityOrganization, Name: "Las Vegas Raiders"},
					},
				},
			},
			wantMode:     "",
			wantTopic:    "news",
			wantRecency:  "week",
			wantQueryAll: []string{"raiders", "roster"},
		},
	}
}

type liveSearchArguments struct {
	Query          string   `json:"query"`
	Mode           string   `json:"mode"`
	Topic          string   `json:"topic"`
	Recency        string   `json:"recency"`
	IncludeDomains []string `json:"include_domains"`
}

func assertLiveSearchPlan(
	t *testing.T,
	calls []json.RawMessage,
	scenario liveEyesWebScenario,
) {
	t.Helper()
	var decoded []liveSearchArguments
	for _, call := range calls {
		var arguments liveSearchArguments
		if err := json.Unmarshal(call, &arguments); err != nil {
			t.Fatalf("decode search_web call %s: %v", call, err)
		}
		decoded = append(decoded, arguments)
	}

	for _, call := range decoded {
		if (scenario.wantMode != "" && call.Mode != scenario.wantMode) ||
			call.Topic != scenario.wantTopic ||
			(scenario.wantRecency != "" && call.Recency != scenario.wantRecency) {
			continue
		}
		query := strings.ToLower(call.Query)
		if strings.Contains(query, "memory") {
			t.Fatalf("search query exposed memory implementation: %q", call.Query)
		}
		if !containsAll(query, scenario.wantQueryAll) {
			continue
		}
		matchedGroups := true
		for _, group := range scenario.wantQueryAnyGroups {
			if !containsAny(query, group) {
				matchedGroups = false
				break
			}
		}
		if matchedGroups {
			return
		}
	}
	t.Fatalf("Eyes search plan did not preserve the scenario constraints: %#v", decoded)
}

func containsAll(value string, terms []string) bool {
	for _, term := range terms {
		if !strings.Contains(value, strings.ToLower(term)) {
			return false
		}
	}
	return true
}

func containsAny(value string, terms []string) bool {
	for _, term := range terms {
		if strings.Contains(value, strings.ToLower(term)) {
			return true
		}
	}
	return false
}

type recordingSearchTool struct {
	delegate tool.Tool
	mu       sync.Mutex
	calls    []json.RawMessage
	errors   []string
}

func (r *recordingSearchTool) Spec() tool.Spec {
	return r.delegate.Spec()
}

func (r *recordingSearchTool) Execute(
	ctx context.Context,
	scope tool.Scope,
	arguments json.RawMessage,
) (tool.Result, error) {
	owned := append(json.RawMessage(nil), arguments...)
	r.mu.Lock()
	r.calls = append(r.calls, owned)
	r.mu.Unlock()
	result, err := r.delegate.Execute(ctx, scope, arguments)
	r.mu.Lock()
	if err == nil {
		r.errors = append(r.errors, "")
	} else {
		r.errors = append(r.errors, err.Error())
	}
	r.mu.Unlock()
	return result, err
}

func (r *recordingSearchTool) Calls() []json.RawMessage {
	r.mu.Lock()
	defer r.mu.Unlock()
	calls := make([]json.RawMessage, len(r.calls))
	for index, call := range r.calls {
		calls[index] = append(json.RawMessage(nil), call...)
	}
	return calls
}

func (r *recordingSearchTool) Errors() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.errors...)
}

type scenarioMemoryReader struct {
	cards  []memory.Card
	mu     sync.Mutex
	lookup memory.Lookup
}

func (r *scenarioMemoryReader) Find(
	_ context.Context,
	_ tool.Scope,
	lookup memory.Lookup,
) ([]memory.Card, error) {
	r.mu.Lock()
	r.lookup = lookup
	r.mu.Unlock()
	return append([]memory.Card(nil), r.cards...), nil
}

func (r *scenarioMemoryReader) Review(
	ctx context.Context,
	scope tool.Scope,
	lookup memory.Lookup,
) ([]memory.Card, error) {
	return r.Find(ctx, scope, lookup)
}

func (r *scenarioMemoryReader) Forget(
	context.Context,
	tool.Scope,
	memory.Lookup,
) (int, error) {
	return 0, nil
}

func (r *scenarioMemoryReader) Lookup() memory.Lookup {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.lookup
}

type emptyConversationReader struct{}

func (emptyConversationReader) Prepare(
	context.Context,
	tool.Scope,
	string,
	string,
) (session.Conversation, error) {
	return session.Conversation{}, nil
}

type noProposalConfirmer struct{}

func (noProposalConfirmer) Confirm(
	context.Context,
	tool.Scope,
	string,
) (string, bool, error) {
	return "", false, nil
}
