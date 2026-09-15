package openai

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
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

type evaluationCategory string

const (
	categoryNamedPeople        evaluationCategory = "named_people"
	categoryChangedPreferences evaluationCategory = "changed_preferences"
	categoryVagueRequests      evaluationCategory = "vague_requests"
	categoryStaleFacts         evaluationCategory = "stale_facts"
	categoryWebFailures        evaluationCategory = "web_failures"
	categoryProactive          evaluationCategory = "proactive_transitions"
	evaluationScenarioCount                       = 25
	evaluationSearchFailure                       = "evaluation web provider unavailable"
)

var evaluationCoverage = map[string][]evaluationCategory{
	"just_left_the_gym":                         {categoryVagueRequests, categoryProactive},
	"latest_protein_goal_wins":                  {categoryChangedPreferences, categoryStaleFacts},
	"class_in_thirty_minutes":                   {categoryVagueRequests, categoryProactive},
	"standing_in_the_store":                     {categoryVagueRequests, categoryProactive},
	"do_not_confuse_roommates_preference":       {categoryNamedPeople},
	"hypothetical_diet_is_not_a_memory":         {categoryVagueRequests, categoryStaleFacts},
	"quick_meal_with_actual_equipment":          {categoryVagueRequests},
	"maya_prefers_morning_meetings":             {categoryNamedPeople},
	"jordan_dislikes_mushrooms_not_the_user":    {categoryNamedPeople},
	"send_priya_the_capstone_draft":             {categoryNamedPeople},
	"devon_is_fixing_the_window_motor":          {categoryNamedPeople},
	"nia_is_vegetarian_the_user_is_not":         {categoryNamedPeople},
	"abstract_dinner_with_jordan":               {categoryNamedPeople, categoryVagueRequests},
	"abstract_meeting_with_maya":                {categoryNamedPeople, categoryVagueRequests},
	"abstract_check_in_with_priya":              {categoryNamedPeople, categoryVagueRequests},
	"abstract_visit_from_devon":                 {categoryNamedPeople, categoryVagueRequests},
	"abstract_dinner_with_nia":                  {categoryNamedPeople, categoryVagueRequests},
	"latest_calorie_target_wins":                {categoryChangedPreferences, categoryStaleFacts},
	"updated_dairy_preference_wins":             {categoryChangedPreferences, categoryStaleFacts},
	"vague_move_at_the_grocery_store":           {categoryVagueRequests, categoryProactive},
	"leaving_campus_before_the_gym":             {categoryProactive},
	"finished_exam_next_priority":               {categoryVagueRequests, categoryProactive},
	"web_failure_restaurants_with_jolene":       {categoryNamedPeople, categoryWebFailures},
	"web_failure_red_rock_official_status":      {categoryWebFailures},
	"web_failure_concerts_tonight_under_budget": {categoryWebFailures},
}

func TestPermanentEvaluationSuiteCoverage(t *testing.T) {
	seen := make(map[string]struct{}, evaluationScenarioCount)
	for _, scenario := range syntheticMemoryScenarios() {
		validateEvaluationName(t, seen, scenario.name)
		if strings.TrimSpace(scenario.spoken) == "" ||
			strings.TrimSpace(scenario.routedQuery) == "" ||
			strings.TrimSpace(scenario.sampleResponse) == "" ||
			len(scenario.mustContain) == 0 {
			t.Errorf("memory scenario %q is missing an evaluation signal", scenario.name)
		}
		for _, card := range scenario.memories {
			if err := card.Normalize().Validate(); err != nil {
				t.Errorf("memory scenario %q has invalid card: %v", scenario.name, err)
			}
		}
	}
	for _, scenario := range webFailureEvaluationScenarios() {
		validateEvaluationName(t, seen, scenario.name)
		if !json.Valid(json.RawMessage(scenario.searchArguments)) ||
			len(scenario.searchQueryTerms) == 0 ||
			strings.TrimSpace(scenario.sampleResponse) == "" {
			t.Errorf("web failure scenario %q is incomplete", scenario.name)
		}
		for _, card := range scenario.memories {
			if err := card.Normalize().Validate(); err != nil {
				t.Errorf("web failure scenario %q has invalid card: %v", scenario.name, err)
			}
		}
	}

	if len(seen) != evaluationScenarioCount {
		t.Fatalf("evaluation scenario count = %d, want %d", len(seen), evaluationScenarioCount)
	}
	if len(evaluationCoverage) != evaluationScenarioCount {
		t.Fatalf("coverage manifest count = %d, want %d", len(evaluationCoverage), evaluationScenarioCount)
	}
	counts := make(map[evaluationCategory]int)
	for name, categories := range evaluationCoverage {
		if _, found := seen[name]; !found {
			t.Errorf("coverage manifest contains unknown scenario %q", name)
		}
		if len(categories) == 0 {
			t.Errorf("scenario %q has no coverage category", name)
		}
		for _, category := range categories {
			counts[category]++
		}
	}
	minimums := map[evaluationCategory]int{
		categoryNamedPeople:        10,
		categoryChangedPreferences: 3,
		categoryVagueRequests:      10,
		categoryStaleFacts:         4,
		categoryWebFailures:        3,
		categoryProactive:          6,
	}
	for category, minimum := range minimums {
		if counts[category] < minimum {
			t.Errorf("category %q has %d scenarios, want at least %d", category, counts[category], minimum)
		}
	}
}

func permanentMemoryEvaluationScenarios() []syntheticMemoryScenario {
	return []syntheticMemoryScenario{
		{
			name:            "latest_calorie_target_wins",
			spoken:          "im planning dinner how many calories am i working with now",
			routedQuery:     "What is my current daily calorie target for planning dinner?",
			lookup:          memory.Lookup{Query: "current calorie target", Terms: []string{"calorie target", "daily calories"}, Topics: []memory.Topic{memory.TopicHealth, memory.TopicGoals}, Kinds: []memory.Kind{memory.KindGoal}},
			memories:        []memory.Card{goalCard("Current daily calorie target", "The user's current daily calorie target is 2,400 calories.")},
			sampleResponse:  "Your current target is 2,400 calories per day. Tell me what you have eaten so far and I can size dinner around what remains.",
			mustContain:     []string{"2,400 calories", "current"},
			liveMustContain: []string{"2,400 calories"},
			mustNotContain:  []string{"2,000 to 3,000", "2000-3000"},
		},
		{
			name:           "updated_dairy_preference_wins",
			spoken:         "would greek yogurt make sense after class",
			routedQuery:    "Would Greek yogurt make sense for me after class?",
			lookup:         memory.Lookup{Query: "current dairy food preference", Terms: []string{"greek yogurt", "dairy preference", "protein"}, Topics: []memory.Topic{memory.TopicPreferences, memory.TopicHealth}, Kinds: []memory.Kind{memory.KindPreference}},
			memories:       []memory.Card{preferenceCard("Current dairy preference", "The user currently eats dairy and enjoys Greek yogurt as a protein snack.")},
			sampleResponse: "Yes. Greek yogurt fits your current preferences and gives you a quick protein-heavy snack after class.",
			mustContain:    []string{"Greek yogurt", "protein"},
			mustNotContain: []string{"dairy-free", "avoid dairy"},
		},
		{
			name:        "vague_move_at_the_grocery_store",
			spoken:      "im here now whats the move",
			routedQuery: "What should I buy now that I am at the grocery store?",
			lookup:      memory.Lookup{Query: "grocery store current plan", Terms: []string{"grocery store", "protein target", "affordable", "chicken"}, Topics: []memory.Topic{memory.TopicPersonal, memory.TopicGoals, memory.TopicPreferences}, Kinds: []memory.Kind{memory.KindFact, memory.KindGoal, memory.KindPreference}},
			memories: []memory.Card{
				factCard("Current location context", "The user is currently at the grocery store."),
				goalCard("Daily protein target", "The user targets 130 grams of protein per day."),
				preferenceCard("Affordable flavorful meals", "The user prefers affordable flavorful meals with chicken and rice."),
			},
			sampleResponse:  "Grab chicken thighs, rice, frozen vegetables, Greek yogurt, eggs, and salsa. That gives you an affordable dinner plus easy protein for tomorrow.",
			mustContain:     []string{"chicken", "affordable", "tomorrow"},
			liveMustContain: []string{"chicken", "rice", "130"},
		},
		{
			name:        "leaving_campus_before_the_gym",
			spoken:      "just left campus heading to the gym what should i grab first",
			routedQuery: "What should I grab before going to the gym after leaving campus?",
			lookup:      memory.Lookup{Query: "quick pre-workout food", Terms: []string{"gym", "quick snack", "protein target", "student"}, Topics: []memory.Topic{memory.TopicHealth, memory.TopicGoals, memory.TopicPersonal}, Kinds: []memory.Kind{memory.KindGoal, memory.KindFact}},
			memories: []memory.Card{
				factCard("Student schedule", "The user is a student and often needs food that is quick between campus and the gym."),
				goalCard("Daily protein target", "The user targets 130 grams of protein per day."),
			},
			sampleResponse:  "Grab a banana and Greek yogurt or a ready-to-drink protein shake, plus water. It is quick enough for the trip from campus without feeling like a full meal.",
			mustContain:     []string{"quick", "protein", "water"},
			liveMustContain: []string{"quick", "protein"},
			liveLookupConcepts: [][]string{
				{"protein", "nutrition", "food", "snack"},
				{"student", "schedule", "quick", "campus"},
			},
		},
		{
			name:        "finished_exam_next_priority",
			spoken:      "finally done with that exam what should i focus on now",
			routedQuery: "What should I focus on now that my exam is finished?",
			lookup:      memory.Lookup{Query: "next priority after exam", Terms: []string{"capstone draft", "priya", "friday", "next priority"}, Topics: []memory.Topic{memory.TopicWork, memory.TopicGoals}, Kinds: []memory.Kind{memory.KindInstruction, memory.KindGoal}},
			memories: []memory.Card{
				personCard("Priya", []memory.Topic{memory.TopicWork}, memory.KindInstruction, "Send Priya the capstone draft", "The user plans to send Priya the capstone draft by Friday."),
			},
			sampleResponse: "Your next concrete priority is the capstone draft you planned to send Priya by Friday. Start with one focused revision block, then decide what remains.",
			mustContain:    []string{"capstone draft", "Priya", "Friday"},
			liveLookupConcepts: [][]string{
				{"pending", "commitment", "deadline", "priority", "instruction", "work"},
			},
		},
	}
}

type webFailureEvaluationScenario struct {
	name             string
	spoken           string
	routedQuery      string
	memories         []memory.Card
	searchArguments  string
	searchQueryTerms []string
	sampleResponse   string
	mustNotContain   []string
}

func webFailureEvaluationScenarios() []webFailureEvaluationScenario {
	return []webFailureEvaluationScenario{
		{
			name:        "web_failure_restaurants_with_jolene",
			spoken:      "find somewhere for me and jolene to eat in las vegas tonight",
			routedQuery: "Find somewhere for Jolene and me to eat in Las Vegas tonight.",
			memories: []memory.Card{
				preferenceCard("Dining budget with Jolene", "The user wants to spend around $60 total when dining with Jolene."),
				personCard("Jolene", []memory.Topic{memory.TopicRelationships, memory.TopicPreferences}, memory.KindRelationship, "Jolene likes Vietnamese food", "Jolene likes Vietnamese food, especially pho; this is Jolene's preference, not the user's."),
			},
			searchArguments:  `{"query":"Las Vegas Vietnamese restaurants tonight for two around $60 total","mode":"research","topic":"general","recency":"none","include_domains":[]}`,
			searchQueryTerms: []string{"las vegas", "60"},
			sampleResponse:   "I couldn't verify current Las Vegas restaurant listings or prices because web search is unavailable. I can retry when search is back.",
			mustNotContain:   []string{"Pho Saigonese", "Hong Kong Garden"},
		},
		{
			name:             "web_failure_red_rock_official_status",
			spoken:           "is red rock canyon open tomorrow morning",
			routedQuery:      "Is Red Rock Canyon open tomorrow morning?",
			searchArguments:  `{"query":"Red Rock Canyon official operating status tomorrow morning","mode":"research","topic":"general","recency":"none","include_domains":["blm.gov","recreation.gov"]}`,
			searchQueryTerms: []string{"red rock"},
			sampleResponse:   "I couldn't verify Red Rock Canyon's official status for tomorrow because web search is unavailable. Check BLM or Recreation.gov before leaving.",
			mustNotContain:   []string{"it is open", "it is closed"},
		},
		{
			name:             "web_failure_concerts_tonight_under_budget",
			spoken:           "what concerts are happening in vegas tonight under 80 bucks",
			routedQuery:      "What concerts are happening in Las Vegas tonight for under $80?",
			searchArguments:  `{"query":"Las Vegas concerts tonight under $80 with current ticket prices","mode":"research","topic":"general","recency":"none","include_domains":[]}`,
			searchQueryTerms: []string{"las vegas", "80"},
			sampleResponse:   "I couldn't verify tonight's Las Vegas concerts or ticket prices because web search is unavailable. I can retry rather than guess at current listings.",
			mustNotContain:   []string{"Adele", "Sphere"},
		},
	}
}

func TestWebFailureEvaluationScenariosReachAgent(t *testing.T) {
	for _, scenario := range webFailureEvaluationScenarios() {
		scenario := scenario
		t.Run(scenario.name, func(t *testing.T) {
			search := newUnavailableEvaluationSearch(t)
			requestNumber := 0
			agent := testAgent(t, search, func(w http.ResponseWriter, r *http.Request) {
				requestNumber++
				var request createRequest
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Errorf("decode agent request: %v", err)
				}
				if requestNumber == 1 {
					writeJSON(t, w, map[string]any{"output": []any{map[string]any{
						"type": "function_call", "call_id": "eval-search", "name": "search_web", "arguments": scenario.searchArguments,
					}}})
					return
				}
				encodedInput, _ := json.Marshal(request.Input)
				if !strings.Contains(string(encodedInput), evaluationSearchFailure) {
					t.Errorf("tool failure was not replayed to the agent: %s", encodedInput)
				}
				writeJSON(t, w, map[string]any{"output": []any{map[string]any{
					"type": "message", "content": []any{map[string]any{"type": "output_text", "text": scenario.sampleResponse}},
				}}})
			})

			response, err := agent.Respond(context.Background(), tool.Scope{}, scenario.routedQuery, session.Conversation{}, scenario.memories)
			if err != nil {
				t.Fatalf("Respond() error = %v", err)
			}
			assertWebFailureResponse(t, response, scenario)
			if calls := search.Calls(); len(calls) != 1 {
				t.Fatalf("search calls = %d, want 1", len(calls))
			}
		})
	}
}

func TestLiveWebFailureEvaluationScenarios(t *testing.T) {
	if os.Getenv("RUN_LIVE_ASSISTANT_TEST") != "1" {
		t.Skip("set RUN_LIVE_ASSISTANT_TEST=1 to call the live OpenAI API")
	}
	apiKey := requiredLiveEnv(t, "OPENAI_API_KEY")
	classify, err := NewClassifier(apiKey, requiredLiveEnv(t, "OPENAI_ROUTER_MODEL"))
	if err != nil {
		t.Fatalf("NewClassifier() error = %v", err)
	}
	router := assistant.NewRouter(classify)

	for _, scenario := range webFailureEvaluationScenarios() {
		scenario := scenario
		t.Run(scenario.name, func(t *testing.T) {
			search := newUnavailableEvaluationSearch(t)
			registry := tool.NewRegistry()
			if err := registry.Register(search); err != nil {
				t.Fatalf("Register(search_web) error = %v", err)
			}
			executor, err := tool.NewExecutor(registry)
			if err != nil {
				t.Fatalf("NewExecutor() error = %v", err)
			}
			agent, err := NewAgent(apiKey, requiredLiveEnv(t, "OPENAI_AGENT_MODEL"), registry, executor)
			if err != nil {
				t.Fatalf("NewAgent() error = %v", err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			defer cancel()
			decision, err := router.Route(ctx, scenario.spoken)
			if err != nil {
				t.Fatalf("Route() error = %v", err)
			}
			if decision.Action != assistant.ActionRespond {
				t.Fatalf("router action = %q, want respond", decision.Action)
			}
			query := strings.TrimSpace(decision.Query)
			if query == "" {
				query = scenario.spoken
			}
			result, err := agent.RespondWithResult(ctx, tool.Scope{TimeZone: "America/Los_Angeles"}, query, session.Conversation{}, scenario.memories)
			if err != nil {
				t.Fatalf("RespondWithResult() error = %v", err)
			}
			calls := search.Calls()
			if len(calls) == 0 || len(calls) > 2 {
				t.Fatalf("search calls = %d, want 1 or 2", len(calls))
			}
			assertEvaluationSearchTerms(t, calls, scenario.searchQueryTerms)
			assertWebFailureResponse(t, result.Text, scenario)
			assertLiveGlassesResponse(t, result.Text)
			t.Logf("spoken:\n%s", scenario.spoken)
			t.Logf("search attempts:\n%s", calls)
			t.Logf("live failure response:\n%s", result.Text)
		})
	}
}

type unavailableEvaluationSearch struct {
	spec  tool.Spec
	mu    sync.Mutex
	calls []json.RawMessage
}

func newUnavailableEvaluationSearch(t *testing.T) *unavailableEvaluationSearch {
	t.Helper()
	search, err := websearch.New("evaluation-key")
	if err != nil {
		t.Fatalf("websearch.New() error = %v", err)
	}
	return &unavailableEvaluationSearch{spec: search.Spec()}
}

func (s *unavailableEvaluationSearch) Spec() tool.Spec { return s.spec }

func (s *unavailableEvaluationSearch) Execute(_ context.Context, _ tool.Scope, arguments json.RawMessage) (tool.Result, error) {
	s.mu.Lock()
	s.calls = append(s.calls, append(json.RawMessage(nil), arguments...))
	s.mu.Unlock()
	return tool.Result{}, errors.New(evaluationSearchFailure)
}

func (s *unavailableEvaluationSearch) Calls() []json.RawMessage {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]json.RawMessage(nil), s.calls...)
}

func validateEvaluationName(t *testing.T, seen map[string]struct{}, name string) {
	t.Helper()
	if strings.TrimSpace(name) == "" {
		t.Error("evaluation scenario has an empty name")
		return
	}
	if _, found := seen[name]; found {
		t.Errorf("duplicate evaluation scenario %q", name)
	}
	seen[name] = struct{}{}
	if _, found := evaluationCoverage[name]; !found {
		t.Errorf("evaluation scenario %q is missing from coverage manifest", name)
	}
}

func assertWebFailureResponse(t *testing.T, response string, scenario webFailureEvaluationScenario) {
	t.Helper()
	lower := strings.ReplaceAll(strings.ToLower(response), "’", "'")
	failureSignals := []string{"couldn't verify", "could not verify", "can't verify", "unable to verify", "search is unavailable", "web search is unavailable", "web lookup is unavailable"}
	foundSignal := false
	for _, signal := range failureSignals {
		if strings.Contains(lower, signal) {
			foundSignal = true
			break
		}
	}
	if !foundSignal {
		t.Errorf("response does not disclose the search failure: %q", response)
	}
	for _, forbidden := range scenario.mustNotContain {
		if strings.Contains(lower, strings.ToLower(forbidden)) {
			t.Errorf("response fabricated unsupported detail %q: %q", forbidden, response)
		}
	}
}

func assertEvaluationSearchTerms(t *testing.T, calls []json.RawMessage, terms []string) {
	t.Helper()
	queries := make([]string, 0, len(calls))
	for _, call := range calls {
		var arguments struct {
			Query string `json:"query"`
		}
		if err := json.Unmarshal(call, &arguments); err != nil {
			t.Fatalf("decode search call %s: %v", call, err)
		}
		queries = append(queries, strings.ToLower(arguments.Query))
	}
	joined := strings.Join(queries, " ")
	if strings.Contains(joined, "memory") {
		t.Errorf("search query exposed memory implementation: %q", joined)
	}
	for _, term := range terms {
		if !strings.Contains(joined, strings.ToLower(term)) {
			t.Errorf("search queries %q do not preserve %q", joined, term)
		}
	}
}

func assertLiveEvaluationLookup(t *testing.T, got memory.Lookup, scenario syntheticMemoryScenario) {
	t.Helper()
	got = got.Normalize()
	if got.Empty() && len(got.Topics) == 0 && len(got.Kinds) == 0 {
		t.Error("live router produced an empty proactive memory lookup")
	}
	want := scenario.lookup.Normalize()
	lookupText := strings.ToLower(strings.Join(append(
		append(
			append([]string{got.Query}, got.Terms...),
			memoryTopicsAsStrings(got.Topics)...,
		),
		memoryKindsAsStrings(got.Kinds)...,
	), " "))
	for _, alternatives := range scenario.liveLookupConcepts {
		matched := false
		for _, alternative := range alternatives {
			if strings.Contains(lookupText, strings.ToLower(alternative)) {
				matched = true
				break
			}
		}
		if !matched {
			t.Errorf("live lookup %q misses context concept alternatives %q", lookupText, alternatives)
		}
	}
	for _, entity := range want.Entities {
		found := false
		for _, actual := range got.Entities {
			if strings.EqualFold(actual, entity) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("live lookup entities %q do not include %q", got.Entities, entity)
		}
	}
}

func assertLiveResponseSignals(t *testing.T, response string, scenario syntheticMemoryScenario) {
	t.Helper()
	signals := scenario.liveMustContain
	if len(signals) == 0 {
		signals = scenario.mustContain
	}
	assertResponseSignals(t, response, syntheticMemoryScenario{
		mustContain:    signals,
		mustNotContain: scenario.mustNotContain,
	})
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
