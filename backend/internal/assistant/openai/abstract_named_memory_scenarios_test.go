package openai

import "github.com/rube11/rev-eyes/backend/internal/memory"

// abstractNamedMemoryScenarios ask for general help without naming the stored
// fact. The person's entity is the retrieval anchor.
func abstractNamedMemoryScenarios() []syntheticMemoryScenario {
	return []syntheticMemoryScenario{
		{
			name:        "abstract_dinner_with_jordan",
			spoken:      "im going out to eat with jordan today any recommendations",
			routedQuery: "What would be a good place or meal when I go out to eat with Jordan today?",
			lookup: memory.Lookup{
				Query:    "restaurant recommendation with Jordan",
				Terms:    []string{"jordan", "food preference", "restaurant"},
				Topics:   []memory.Topic{memory.TopicRelationships, memory.TopicPreferences},
				Kinds:    []memory.Kind{memory.KindRelationship, memory.KindPreference},
				Entities: []string{"Jordan"},
			},
			memories: []memory.Card{
				personCard(
					"Jordan",
					[]memory.Topic{memory.TopicRelationships, memory.TopicPreferences},
					memory.KindRelationship,
					"Jordan dislikes mushrooms",
					"Jordan dislikes mushrooms; this is Jordan's preference, not the user's.",
				),
				preferenceCard("Likes mushrooms", "The user loves mushrooms."),
			},
			sampleResponse: "Pick somewhere with customizable dishes, and avoid mushrooms in anything you share with Jordan. You can still order mushrooms for yourself.",
			mustContain:    []string{"avoid mushrooms", "with Jordan", "for yourself"},
			mustNotContain: []string{"you dislike mushrooms", "Jordan is allergic"},
		},
		{
			name:        "abstract_meeting_with_maya",
			spoken:      "im meeting up with maya later anything i should know",
			routedQuery: "Is there anything I should know before meeting Maya later?",
			lookup: memory.Lookup{
				Query:    "context for meeting Maya",
				Terms:    []string{"maya", "meeting preference", "capstone"},
				Topics:   []memory.Topic{memory.TopicWork, memory.TopicRelationships, memory.TopicPreferences},
				Kinds:    []memory.Kind{memory.KindRelationship, memory.KindPreference},
				Entities: []string{"Maya"},
			},
			memories: []memory.Card{
				personCard(
					"Maya",
					[]memory.Topic{memory.TopicWork, memory.TopicRelationships},
					memory.KindRelationship,
					"Maya is my capstone partner",
					"Maya is the user's capstone project partner.",
				),
				personCard(
					"Maya",
					[]memory.Topic{memory.TopicWork, memory.TopicPreferences},
					memory.KindPreference,
					"Maya prefers morning meetings",
					"Maya prefers meetings before 10 a.m.; this is Maya's preference, not the user's.",
				),
			},
			sampleResponse: "Maya is your capstone partner and usually prefers meetings before 10 a.m. If later is after that, confirm the time with her.",
			mustContain:    []string{"capstone partner", "before 10 a.m.", "confirm the time"},
			mustNotContain: []string{"you prefer morning"},
		},
		{
			name:        "abstract_check_in_with_priya",
			spoken:      "im about to talk to priya am i forgetting anything",
			routedQuery: "Am I forgetting anything before I talk to Priya?",
			lookup: memory.Lookup{
				Query:    "open context involving Priya",
				Terms:    []string{"priya", "capstone", "send"},
				Topics:   []memory.Topic{memory.TopicWork},
				Kinds:    []memory.Kind{memory.KindInstruction, memory.KindRelationship},
				Entities: []string{"Priya"},
			},
			memories: []memory.Card{
				personCard(
					"Priya",
					[]memory.Topic{memory.TopicWork},
					memory.KindInstruction,
					"Send Priya the capstone draft",
					"The user plans to send Priya the capstone draft by Friday.",
				),
				personCard(
					"Priya",
					[]memory.Topic{memory.TopicWork, memory.TopicRelationships},
					memory.KindRelationship,
					"Priya is my professor",
					"Priya is the user's capstone professor.",
				),
			},
			sampleResponse: "Remember that you planned to send Priya your capstone draft by Friday.",
			mustContain:    []string{"Priya", "capstone draft", "Friday"},
		},
		{
			name:        "abstract_visit_from_devon",
			spoken:      "devons coming by later remind me what thats about",
			routedQuery: "Why is Devon coming by later?",
			lookup: memory.Lookup{
				Query:    "reason Devon is visiting",
				Terms:    []string{"devon", "car repair", "window motor"},
				Topics:   []memory.Topic{memory.TopicPersonal, memory.TopicRelationships},
				Kinds:    []memory.Kind{memory.KindEvent, memory.KindRelationship},
				Entities: []string{"Devon"},
			},
			memories: []memory.Card{
				personCard(
					"Devon",
					[]memory.Topic{memory.TopicPersonal},
					memory.KindEvent,
					"Devon is repairing the passenger window",
					"Devon is replacing the passenger-side window motor in the user's car.",
				),
				personCard(
					"Devon",
					[]memory.Topic{memory.TopicPersonal, memory.TopicRelationships},
					memory.KindRelationship,
					"Devon is my mechanic",
					"Devon is the user's mechanic.",
				),
			},
			sampleResponse: "Devon is your mechanic, and he is coming by about replacing the passenger-side window motor in your car.",
			mustContain:    []string{"mechanic", "passenger-side window motor"},
		},
		{
			name:        "abstract_dinner_with_nia",
			spoken:      "nia wants to grab dinner what kinda place makes sense",
			routedQuery: "What kind of restaurant would make sense for dinner with Nia?",
			lookup: memory.Lookup{
				Query:    "restaurant for dinner with Nia",
				Terms:    []string{"nia", "food preference", "restaurant"},
				Topics:   []memory.Topic{memory.TopicRelationships, memory.TopicPreferences},
				Kinds:    []memory.Kind{memory.KindRelationship, memory.KindPreference},
				Entities: []string{"Nia"},
			},
			memories: []memory.Card{
				personCard(
					"Nia",
					[]memory.Topic{memory.TopicRelationships, memory.TopicPreferences},
					memory.KindRelationship,
					"Nia is vegetarian",
					"Nia is vegetarian; this is Nia's diet, not the user's.",
				),
				preferenceCard("Likes chicken", "The user likes chicken."),
			},
			sampleResponse: "Choose a vegetarian-friendly restaurant with optional meat dishes, such as Mediterranean, Indian, Thai, or Mexican, so both you and Nia have good choices.",
			mustContain:    []string{"vegetarian-friendly", "both you and Nia"},
			mustNotContain: []string{"you are vegetarian", "Nia likes chicken"},
		},
	}
}

func personCard(
	name string,
	topics []memory.Topic,
	kind memory.Kind,
	title string,
	summary string,
) memory.Card {
	return memory.Card{
		Topics:   topics,
		Kind:     kind,
		Title:    title,
		Summary:  summary,
		Entities: []memory.Entity{{Type: memory.EntityPerson, Name: name}},
	}
}
