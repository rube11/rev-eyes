package openai

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/rube11/rev-eyes/backend/internal/memory"
	"github.com/rube11/rev-eyes/backend/internal/session"
	"github.com/rube11/rev-eyes/backend/internal/tool"
)

const relevantMemoriesPrefix = "Relevant user memories:\n"

// TestSyntheticMemoryScenariosReachAgent is a deterministic, offline check of
// the memory-to-answer boundary. It does not pretend to evaluate PostgreSQL
// ranking or model quality: each scenario states the lookup we want, supplies
// the fake cards that retrieval would return, and verifies that the production
// Agent sends those cards and the routed query to the model in the right order.
//
// The sample response is logged to make the intended glasses experience easy
// to review without making a network call.
func TestSyntheticMemoryScenariosReachAgent(t *testing.T) {
	for _, scenario := range syntheticMemoryScenarios() {
		scenario := scenario
		t.Run(scenario.name, func(t *testing.T) {
			for _, card := range scenario.memories {
				if err := card.Normalize().Validate(); err != nil {
					t.Fatalf("invalid fake memory %q: %v", card.Title, err)
				}
			}

			var request createRequest
			agent := testAgent(t, nil, func(w http.ResponseWriter, r *http.Request) {
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Errorf("decode agent request: %v", err)
				}
				writeJSON(t, w, map[string]any{
					"output": []any{map[string]any{
						"type": "message",
						"content": []any{map[string]any{
							"type": "output_text",
							"text": scenario.sampleResponse,
						}},
					}},
				})
			})

			response, err := agent.Respond(
				context.Background(),
				tool.Scope{TimeZone: "America/Los_Angeles"},
				scenario.routedQuery,
				session.Conversation{},
				scenario.memories,
			)
			if err != nil {
				t.Fatalf("Respond() error = %v", err)
			}
			if response != scenario.sampleResponse {
				t.Fatalf("Respond() = %q, want %q", response, scenario.sampleResponse)
			}
			if utf8.RuneCountInString(response) > 340 {
				t.Fatalf("sample response is too long for glasses: %d characters", utf8.RuneCountInString(response))
			}

			assertSyntheticScenarioRequest(t, request, scenario)
			assertResponseSignals(t, response, scenario)

			lookupJSON, err := json.MarshalIndent(scenario.lookup.Normalize(), "", "  ")
			if err != nil {
				t.Fatalf("encode lookup: %v", err)
			}
			memoryJSON, err := json.MarshalIndent(scenario.memories, "", "  ")
			if err != nil {
				t.Fatalf("encode fake memories: %v", err)
			}
			t.Logf("spoken:\n%s", scenario.spoken)
			t.Logf("routed request:\n%s", scenario.routedQuery)
			t.Logf("memory search query:\n%s", scenario.lookup.Normalize().Query)
			t.Logf("memory lookup filters and search terms:\n%s", lookupJSON)
			t.Logf("fake retrieved memories:\n%s", memoryJSON)
			t.Logf("sample glasses response:\n%s", response)
		})
	}
}

type syntheticMemoryScenario struct {
	name               string
	spoken             string
	routedQuery        string
	lookup             memory.Lookup
	memories           []memory.Card
	sampleResponse     string
	mustContain        []string
	liveMustContain    []string
	mustNotContain     []string
	liveLookupConcepts [][]string
}

func syntheticMemoryScenarios() []syntheticMemoryScenario {
	scenarios := []syntheticMemoryScenario{
		{
			name:        "just_left_the_gym",
			spoken:      "alright i just got out the gym what should i eat rn",
			routedQuery: "What should I eat right now after leaving the gym?",
			lookup: memory.Lookup{
				Query:  "post-workout meal right now",
				Terms:  []string{"protein target", "calorie range", "flavorful meals", "chicken", "rice"},
				Topics: []memory.Topic{memory.TopicHealth, memory.TopicGoals, memory.TopicPreferences},
				Kinds:  []memory.Kind{memory.KindGoal, memory.KindPreference},
			},
			memories: []memory.Card{
				goalCard("Daily protein target", "The user targets 130 grams of protein per day."),
				goalCard("Daily calorie range", "The user typically needs 2,000 to 3,000 calories per day."),
				preferenceCard("Favorite meal ingredients", "The user likes flavorful meals with steak, chicken, pasta, and rice."),
			},
			sampleResponse:  "Make a spicy chicken-and-rice bowl with vegetables. Use a solid chicken portion for protein; tell me what you have eaten today and I can size it against your 130 g goal.",
			mustContain:     []string{"chicken-and-rice", "130 g"},
			liveMustContain: []string{"chicken", "rice"},
			liveLookupConcepts: [][]string{
				{"protein", "nutrition", "food", "calorie"},
			},
		},
		{
			name:        "latest_protein_goal_wins",
			spoken:      "ive had like 90g of protein so far how much more do i need today",
			routedQuery: "How much protein do I have left today after eating about 90 grams?",
			lookup: memory.Lookup{
				Query:  "daily protein target",
				Terms:  []string{"protein target", "daily protein goal"},
				Topics: []memory.Topic{memory.TopicHealth, memory.TopicGoals},
				Kinds:  []memory.Kind{memory.KindGoal},
			},
			memories: []memory.Card{
				goalCard("Daily protein target", "The user's current protein target is 150 grams per day."),
			},
			sampleResponse: "You have about 60 g left today. A chicken bowl now and Greek yogurt later would get you close.",
			mustContain:    []string{"60 g"},
			mustNotContain: []string{"40 g", "130 g"},
		},
		{
			name:        "class_in_thirty_minutes",
			spoken:      "yo i got class in like 30 mins and havent ate whats the move",
			routedQuery: "What can I eat quickly before class in about 30 minutes?",
			lookup: memory.Lookup{
				Query:  "quick food before class",
				Terms:  []string{"student", "quick meal", "protein", "affordable"},
				Topics: []memory.Topic{memory.TopicPersonal, memory.TopicHealth, memory.TopicPreferences},
				Kinds:  []memory.Kind{memory.KindFact, memory.KindGoal, memory.KindPreference},
			},
			memories: []memory.Card{
				factCard("Student", "The user is a student."),
				goalCard("Daily protein target", "The user targets 130 grams of protein per day."),
				preferenceCard("Affordable food", "The user prefers meals that do not cost a lot."),
			},
			sampleResponse: "Grab a chicken wrap or Greek yogurt with fruit now. Both are quick, protein-heavy, and cheaper than ordering a full meal; eat something larger after class.",
			mustContain:    []string{"quick", "protein"},
		},
		{
			name:        "standing_in_the_store",
			spoken:      "im at the store rn what do i actually need for dinner and tomorrow",
			routedQuery: "What groceries should I buy for dinner tonight and meals tomorrow?",
			lookup: memory.Lookup{
				Query:  "groceries for dinner and tomorrow",
				Terms:  []string{"student", "affordable", "protein target", "chicken", "rice"},
				Topics: []memory.Topic{memory.TopicPersonal, memory.TopicHealth, memory.TopicPreferences},
				Kinds:  []memory.Kind{memory.KindFact, memory.KindGoal, memory.KindPreference},
			},
			memories: []memory.Card{
				factCard("Student", "The user is a student and tries to keep groceries affordable."),
				goalCard("Daily protein target", "The user targets 130 grams of protein per day."),
				preferenceCard("Favorite meal ingredients", "The user likes flavorful meals with chicken and rice."),
			},
			sampleResponse:  "Get chicken thighs, rice, eggs, Greek yogurt, frozen vegetables, tortillas, salsa, and one fruit. Make spicy chicken bowls tonight, then use the leftovers for wraps tomorrow.",
			mustContain:     []string{"chicken", "rice", "tomorrow"},
			liveMustContain: []string{"chicken", "rice", "protein"},
			mustNotContain:  []string{"$"},
		},
		{
			name:        "do_not_confuse_roommates_preference",
			spoken:      "what ingredients do i usually like in pasta",
			routedQuery: "What ingredients do I usually like in pasta?",
			lookup: memory.Lookup{
				Query:  "my pasta ingredient preferences",
				Terms:  []string{"pasta", "mushrooms", "food preferences"},
				Topics: []memory.Topic{memory.TopicPreferences, memory.TopicRelationships},
				Kinds:  []memory.Kind{memory.KindPreference, memory.KindRelationship},
			},
			memories: []memory.Card{
				{
					Topics:  []memory.Topic{memory.TopicPreferences},
					Kind:    memory.KindPreference,
					Title:   "Likes mushrooms",
					Summary: "The user loves mushrooms.",
				},
				{
					Topics:   []memory.Topic{memory.TopicRelationships, memory.TopicPreferences},
					Kind:     memory.KindRelationship,
					Title:    "Jake dislikes mushrooms",
					Summary:  "The user's roommate Jake hates mushrooms; this is Jake's preference, not the user's.",
					Entities: []memory.Entity{{Type: memory.EntityPerson, Name: "Jake"}},
				},
			},
			sampleResponse: "Mushrooms are the pasta ingredient I know you like. Jake is the one who dislikes them.",
			mustContain:    []string{"you like", "Jake"},
			mustNotContain: []string{"you dislike", "you hate"},
		},
		{
			name:        "hypothetical_diet_is_not_a_memory",
			spoken:      "give me a dinner idea that actually fits how i eat",
			routedQuery: "Give me a dinner idea that fits how I normally eat.",
			lookup: memory.Lookup{
				Query:  "usual dinner preferences",
				Terms:  []string{"flavorful meals", "pasta", "rice", "chicken"},
				Topics: []memory.Topic{memory.TopicPreferences},
				Kinds:  []memory.Kind{memory.KindPreference},
			},
			memories: []memory.Card{
				preferenceCard("Favorite meal ingredients", "The user likes flavorful meals with chicken, pasta, and rice."),
			},
			sampleResponse: "Make spicy chicken pasta with peppers and a garlicky tomato sauce. It matches the flavorful chicken-and-pasta meals you actually like.",
			mustContain:    []string{"chicken pasta", "flavorful"},
			mustNotContain: []string{"keto", "low-carb", "low carb"},
		},
		{
			name:        "quick_meal_with_actual_equipment",
			spoken:      "i just got home what can i make quick",
			routedQuery: "What can I cook quickly now that I am home?",
			lookup: memory.Lookup{
				Query:  "quick meal at home",
				Terms:  []string{"20 minutes", "air fryer", "rice cooker", "quick meal"},
				Topics: []memory.Topic{memory.TopicPersonal, memory.TopicPreferences},
				Kinds:  []memory.Kind{memory.KindFact, memory.KindPreference},
			},
			memories: []memory.Card{
				factCard("Weeknight cooking time", "After class, the user usually has about 20 minutes to cook."),
				factCard("Kitchen equipment", "The user has an air fryer and a rice cooker."),
			},
			sampleResponse:  "Air-fry seasoned chicken while the rice cooker runs, then add salsa or a quick sauce. It fits your equipment and should land near 20 minutes.",
			mustContain:     []string{"Air-fry", "rice cooker", "20 minutes"},
			liveMustContain: []string{"air", "rice cooker"},
			liveLookupConcepts: [][]string{
				{"equipment", "kitchen", "air fryer", "rice cooker"},
				{"time", "quick", "cooking"},
			},
		},
		{
			name:        "maya_prefers_morning_meetings",
			spoken:      "yo what time does maya usually like to meet",
			routedQuery: "What time does Maya usually prefer to meet?",
			lookup: memory.Lookup{
				Query:    "Maya meeting time preference",
				Terms:    []string{"maya", "meeting time", "morning meetings"},
				Topics:   []memory.Topic{memory.TopicWork, memory.TopicRelationships, memory.TopicPreferences},
				Kinds:    []memory.Kind{memory.KindRelationship, memory.KindPreference},
				Entities: []string{"Maya"},
			},
			memories: []memory.Card{
				{
					Topics:  []memory.Topic{memory.TopicWork, memory.TopicRelationships},
					Kind:    memory.KindRelationship,
					Title:   "Maya is my capstone partner",
					Summary: "Maya is the user's capstone project partner.",
					Entities: []memory.Entity{
						{Type: memory.EntityPerson, Name: "Maya"},
					},
				},
				{
					Topics:  []memory.Topic{memory.TopicWork, memory.TopicPreferences},
					Kind:    memory.KindPreference,
					Title:   "Maya prefers morning meetings",
					Summary: "Maya prefers meetings before 10 a.m.; this is Maya's preference, not the user's.",
					Entities: []memory.Entity{
						{Type: memory.EntityPerson, Name: "Maya"},
					},
				},
			},
			sampleResponse: "Maya usually prefers meeting before 10 a.m. She is your capstone partner.",
			mustContain:    []string{"Maya", "before 10 a.m.", "capstone partner"},
			mustNotContain: []string{"you prefer morning", "your preference"},
		},
		{
			name:        "jordan_dislikes_mushrooms_not_the_user",
			spoken:      "im grabbing food with jordan what should i avoid ordering for them",
			routedQuery: "What should I avoid ordering when I get food with Jordan?",
			lookup: memory.Lookup{
				Query:    "Jordan food preference",
				Terms:    []string{"jordan", "food preference", "mushrooms"},
				Topics:   []memory.Topic{memory.TopicRelationships, memory.TopicPreferences},
				Kinds:    []memory.Kind{memory.KindRelationship, memory.KindPreference},
				Entities: []string{"Jordan"},
			},
			memories: []memory.Card{
				{
					Topics:  []memory.Topic{memory.TopicRelationships, memory.TopicPreferences},
					Kind:    memory.KindRelationship,
					Title:   "Jordan dislikes mushrooms",
					Summary: "Jordan dislikes mushrooms; this is Jordan's preference, not the user's.",
					Entities: []memory.Entity{
						{Type: memory.EntityPerson, Name: "Jordan"},
					},
				},
				preferenceCard("Likes mushrooms", "The user loves mushrooms."),
			},
			sampleResponse: "Avoid a mushroom-heavy shared dish because Jordan dislikes mushrooms. You can still order mushrooms separately for yourself.",
			mustContain:    []string{"Jordan dislikes mushrooms", "for yourself"},
			mustNotContain: []string{"you dislike mushrooms", "you hate mushrooms"},
		},
		{
			name:        "send_priya_the_capstone_draft",
			spoken:      "wait what was i supposed to send priya again",
			routedQuery: "What was I supposed to send Priya?",
			lookup: memory.Lookup{
				Query:    "send Priya",
				Terms:    []string{"priya", "capstone draft", "send"},
				Topics:   []memory.Topic{memory.TopicWork},
				Kinds:    []memory.Kind{memory.KindInstruction, memory.KindRelationship},
				Entities: []string{"Priya"},
			},
			memories: []memory.Card{
				{
					Topics:  []memory.Topic{memory.TopicWork},
					Kind:    memory.KindInstruction,
					Title:   "Send Priya the capstone draft",
					Summary: "The user plans to send Priya the capstone draft by Friday.",
					Entities: []memory.Entity{
						{Type: memory.EntityPerson, Name: "Priya"},
						{Type: memory.EntityProject, Name: "Capstone"},
					},
				},
				{
					Topics:  []memory.Topic{memory.TopicWork, memory.TopicRelationships},
					Kind:    memory.KindRelationship,
					Title:   "Priya is my professor",
					Summary: "Priya is the user's capstone professor.",
					Entities: []memory.Entity{
						{Type: memory.EntityPerson, Name: "Priya"},
						{Type: memory.EntityProject, Name: "Capstone"},
					},
				},
			},
			sampleResponse: "You were supposed to send Priya your capstone draft by Friday.",
			mustContain:    []string{"Priya", "capstone draft", "Friday"},
		},
		{
			name:        "devon_is_fixing_the_window_motor",
			spoken:      "what was devon fixing on my car again",
			routedQuery: "What was Devon fixing on my car?",
			lookup: memory.Lookup{
				Query:    "Devon car repair",
				Terms:    []string{"devon", "car repair", "window motor"},
				Topics:   []memory.Topic{memory.TopicPersonal, memory.TopicRelationships},
				Kinds:    []memory.Kind{memory.KindEvent, memory.KindRelationship},
				Entities: []string{"Devon"},
			},
			memories: []memory.Card{
				{
					Topics:  []memory.Topic{memory.TopicPersonal},
					Kind:    memory.KindEvent,
					Title:   "Devon is repairing the passenger window",
					Summary: "Devon is replacing the passenger-side window motor in the user's car.",
					Entities: []memory.Entity{
						{Type: memory.EntityPerson, Name: "Devon"},
					},
				},
				{
					Topics:  []memory.Topic{memory.TopicPersonal, memory.TopicRelationships},
					Kind:    memory.KindRelationship,
					Title:   "Devon is my mechanic",
					Summary: "Devon is the user's mechanic.",
					Entities: []memory.Entity{
						{Type: memory.EntityPerson, Name: "Devon"},
					},
				},
			},
			sampleResponse: "Devon was replacing the passenger-side window motor in your car.",
			mustContain:    []string{"Devon", "passenger-side window motor"},
		},
		{
			name:        "nia_is_vegetarian_the_user_is_not",
			spoken:      "what should i order for nia if were sharing",
			routedQuery: "What should I order if Nia and I are sharing food?",
			lookup: memory.Lookup{
				Query:    "Nia shared food preference",
				Terms:    []string{"nia", "vegetarian", "food preference"},
				Topics:   []memory.Topic{memory.TopicRelationships, memory.TopicPreferences},
				Kinds:    []memory.Kind{memory.KindRelationship, memory.KindPreference},
				Entities: []string{"Nia"},
			},
			memories: []memory.Card{
				{
					Topics:  []memory.Topic{memory.TopicRelationships, memory.TopicPreferences},
					Kind:    memory.KindRelationship,
					Title:   "Nia is vegetarian",
					Summary: "Nia is vegetarian; this is Nia's diet, not the user's.",
					Entities: []memory.Entity{
						{Type: memory.EntityPerson, Name: "Nia"},
					},
				},
				preferenceCard("Likes chicken", "The user likes chicken."),
			},
			sampleResponse: "Choose a vegetarian dish to share with Nia. If you want chicken, order it separately rather than adding it to the shared food.",
			mustContain:    []string{"vegetarian", "with Nia", "chicken"},
			mustNotContain: []string{"Nia likes chicken", "you are vegetarian"},
		},
	}
	scenarios = append(scenarios, abstractNamedMemoryScenarios()...)
	return append(scenarios, permanentMemoryEvaluationScenarios()...)
}

func assertSyntheticScenarioRequest(
	t *testing.T,
	request createRequest,
	scenario syntheticMemoryScenario,
) {
	t.Helper()
	if len(request.Input) != 2 {
		t.Fatalf("agent input count = %d, want memory context plus query", len(request.Input))
	}

	var memoryInput inputMessage
	if err := json.Unmarshal(request.Input[0], &memoryInput); err != nil {
		t.Fatalf("decode memory input: %v", err)
	}
	if memoryInput.Role != "user" || !strings.HasPrefix(memoryInput.Content, relevantMemoriesPrefix) {
		t.Fatalf("memory input = %#v", memoryInput)
	}
	var delivered []memory.Card
	if err := json.Unmarshal([]byte(strings.TrimPrefix(memoryInput.Content, relevantMemoriesPrefix)), &delivered); err != nil {
		t.Fatalf("decode delivered memories: %v", err)
	}
	if !reflect.DeepEqual(delivered, scenario.memories) {
		t.Fatalf("delivered memories = %#v, want %#v", delivered, scenario.memories)
	}

	var queryInput inputMessage
	if err := json.Unmarshal(request.Input[1], &queryInput); err != nil {
		t.Fatalf("decode query input: %v", err)
	}
	if queryInput.Role != "user" || queryInput.Content != scenario.routedQuery {
		t.Fatalf("query input = %#v", queryInput)
	}
}

func assertResponseSignals(t *testing.T, response string, scenario syntheticMemoryScenario) {
	t.Helper()
	lowerResponse := strings.ToLower(response)
	for _, wanted := range scenario.mustContain {
		if !strings.Contains(lowerResponse, strings.ToLower(wanted)) {
			t.Errorf("response %q does not contain %q", response, wanted)
		}
	}
	for _, forbidden := range scenario.mustNotContain {
		if strings.Contains(lowerResponse, strings.ToLower(forbidden)) {
			t.Errorf("response %q unexpectedly contains %q", response, forbidden)
		}
	}
}

func goalCard(title, summary string) memory.Card {
	return memory.Card{
		Topics:  []memory.Topic{memory.TopicHealth, memory.TopicGoals},
		Kind:    memory.KindGoal,
		Title:   title,
		Summary: summary,
	}
}

func preferenceCard(title, summary string) memory.Card {
	return memory.Card{
		Topics:  []memory.Topic{memory.TopicPreferences},
		Kind:    memory.KindPreference,
		Title:   title,
		Summary: summary,
	}
}

func factCard(title, summary string) memory.Card {
	return memory.Card{
		Topics:  []memory.Topic{memory.TopicPersonal},
		Kind:    memory.KindFact,
		Title:   title,
		Summary: summary,
	}
}
