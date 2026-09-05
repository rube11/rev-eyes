package assistant

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/rube11/rev-eyes/backend/internal/memory"
	"github.com/rube11/rev-eyes/backend/internal/session"
	"github.com/rube11/rev-eyes/backend/internal/tool"
)

func TestServiceOriginalRequestPreservesSourceAndNegativeConstraints(t *testing.T) {
	for _, aware := range []bool{false, true} {
		for _, explicitLookup := range []bool{false, true} {
			name := "basic"
			if aware {
				name = "proposal_aware"
			}
			if explicitLookup {
				name += "/explicit_lookup"
			} else {
				name += "/canonical_lookup"
			}
			t.Run(name, func(t *testing.T) {
				const raw = " \tCheck Aurora Observatory: explain the  current viewing rules.\nDo not use blogs, invent prices, or omit the source. \n"
				const canonical = "Explain  the viewing rules."
				decision := Decision{Action: ActionRespond, Query: "  " + canonical + "  ", MemoryLookup: memory.Lookup{Terms: []string{"observatory"}, Topics: []memory.Topic{memory.TopicPlaces}}}
				if explicitLookup {
					decision.MemoryLookup.Query = "  Aurora   preferences  "
				}
				wantLookup := decision.MemoryLookup
				wantLookup.Query = "Explain the viewing rules."
				if explicitLookup {
					wantLookup.Query = "Aurora preferences"
				}
				scope := tool.Scope{UserID: "synthetic-user", SessionID: "synthetic-session", TimeZone: "America/Los_Angeles"}
				wantAgentScope := scope
				wantAgentScope.UtteranceID = "original-utterance"
				cards := []memory.Card{{Title: "Observing preferences", Summary: "Prefers outdoor viewing."}}
				conversation := session.Conversation{Summary: "Earlier synthetic context.", Messages: []session.Message{{ID: "prior-turn", Speaker: session.SpeakerUser, Text: "I enjoy astronomy."}}}
				probe, agent := originalRequestTestAgent(aware, AgentResult{Text: "  Source-checked answer.  "})
				var lookup memory.Lookup
				var prepared, routed, confirmed string
				service, err := NewService(
					routerFunc(func(_ context.Context, utterance string) (Decision, error) {
						routed = utterance
						return decision, nil
					}), agent,
					memoryReaderFunc(func(_ context.Context, gotScope tool.Scope, got memory.Lookup) ([]memory.Card, error) {
						lookup = got
						if gotScope != scope {
							t.Errorf("memory lookup scope changed: %#v", gotScope)
						}
						return cards, nil
					}),
					conversationReaderFunc(func(_ context.Context, gotScope tool.Scope, utteranceID, text string) (session.Conversation, error) {
						prepared = text
						if gotScope != scope || utteranceID != "original-utterance" {
							t.Errorf("conversation identity changed: scope=%#v utterance=%q", gotScope, utteranceID)
						}
						return conversation, nil
					}),
					proposalConfirmerFunc(func(_ context.Context, _ tool.Scope, utterance string) (string, bool, error) {
						confirmed = utterance
						return "", false, nil
					}),
				)
				if err != nil {
					t.Fatal(err)
				}
				outcome, err := service.HandleUtterance(context.Background(), scope, "original-utterance", raw)
				if err != nil {
					t.Fatal(err)
				}
				if probe.query != strings.TrimSpace(raw) || probe.scope != wantAgentScope {
					t.Fatalf("answering agent lost original constraints or trusted identity: query=%q scope=%#v", probe.query, probe.scope)
				}
				if !reflect.DeepEqual(probe.cards, cards) || !reflect.DeepEqual(probe.conversation, conversation) {
					t.Fatal("prepared memories/conversation changed before reaching the agent")
				}
				if !reflect.DeepEqual(lookup, wantLookup) || prepared != canonical {
					t.Fatalf("context preparation must retain canonical queries: lookup=%#v prepared=%q", lookup, prepared)
				}
				if routed != raw || confirmed != raw || !reflect.DeepEqual(outcome.Decision, decision) {
					t.Fatal("forwarding the original request changed routing, confirmation input, or the recorded decision")
				}
				if outcome.Response != "Source-checked answer." || outcome.ProposalCreated {
					t.Fatalf("response/effect handling changed: %#v", outcome)
				}
				assertOriginalRequestAgentPath(t, probe, aware)
			})
		}
	}
}

func TestServiceOriginalRequestUsesOnlyTheAvailableQueryWhenOneIsBlank(t *testing.T) {
	for _, aware := range []bool{false, true} {
		for _, test := range []struct{ name, raw, canonical, want string }{
			{"empty_original", "", "  Canonical request.  ", "Canonical request."},
			{"whitespace_original", " \t\r\n ", "  Canonical request.  ", "Canonical request."},
			{"empty_canonical", "  Check Aurora; do not estimate.  ", " \t ", "Check Aurora; do not estimate."},
		} {
			name := "basic/" + test.name
			if aware {
				name = "proposal_aware/" + test.name
			}
			t.Run(name, func(t *testing.T) {
				probe, agent := originalRequestTestAgent(aware, AgentResult{Text: "Answer."})
				var lookupQuery, prepared string
				service, err := NewService(
					routerFunc(func(context.Context, string) (Decision, error) {
						return Decision{Action: ActionRespond, Query: test.canonical}, nil
					}),
					agent,
					memoryReaderFunc(func(_ context.Context, _ tool.Scope, lookup memory.Lookup) ([]memory.Card, error) {
						lookupQuery = lookup.Query
						return nil, nil
					}),
					conversationReaderFunc(func(_ context.Context, _ tool.Scope, _, text string) (session.Conversation, error) {
						prepared = text
						return session.Conversation{}, nil
					}),
					noProposalConfirmation,
				)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := service.HandleUtterance(context.Background(), tool.Scope{}, "synthetic-turn", test.raw); err != nil {
					t.Fatal(err)
				}
				if probe.query != test.want || lookupQuery != test.want || prepared != test.want {
					t.Fatalf("blank-query fallback diverged: agent=%q lookup=%q context=%q want=%q", probe.query, lookupQuery, prepared, test.want)
				}
				assertOriginalRequestAgentPath(t, probe, aware)
			})
		}
	}
}

func TestServiceOriginalRequestDoesNotChangeTransitionOrProposalQueries(t *testing.T) {
	for _, aware := range []bool{false, true} {
		for _, action := range []Action{ActionStateTransition, ActionProposeTask, ActionProposeWatch} {
			name := "basic/" + string(action)
			if aware {
				name = "proposal_aware/" + string(action)
			}
			t.Run(name, func(t *testing.T) {
				const canonical = "Perform the canonical routed action."
				decision := Decision{Action: action, Query: "  " + canonical + "  "}
				created := aware && action != ActionStateTransition
				probe, agent := originalRequestTestAgent(aware, AgentResult{Text: "  Existing action response.  ", ProposalCreated: created})
				var lookupQuery, prepared string
				service, err := NewService(
					routerFunc(func(context.Context, string) (Decision, error) { return decision, nil }), agent,
					memoryReaderFunc(func(_ context.Context, _ tool.Scope, lookup memory.Lookup) ([]memory.Card, error) {
						lookupQuery = lookup.Query
						return nil, nil
					}),
					conversationReaderFunc(func(_ context.Context, _ tool.Scope, _, text string) (session.Conversation, error) {
						prepared = text
						return session.Conversation{}, nil
					}),
					noProposalConfirmation,
				)
				if err != nil {
					t.Fatal(err)
				}
				outcome, err := service.HandleUtterance(context.Background(), tool.Scope{}, "synthetic-turn", "The raw utterance differs from its routed action.")
				if err != nil {
					t.Fatal(err)
				}
				if probe.query != canonical || lookupQuery != canonical || prepared != canonical {
					t.Fatalf("non-respond action stopped using its canonical query: agent=%q lookup=%q context=%q", probe.query, lookupQuery, prepared)
				}
				if !reflect.DeepEqual(outcome.Decision, decision) || outcome.Response != "Existing action response." || outcome.ProposalCreated != created {
					t.Fatalf("proposal/transition outcome changed: %#v", outcome)
				}
				assertOriginalRequestAgentPath(t, probe, aware)
			})
		}
	}
}

func TestServiceOriginalRequestLeavesProposalConfirmationAheadOfRouting(t *testing.T) {
	probe, agent := originalRequestTestAgent(true, AgentResult{})
	routerCalled := false
	confirmed := ""
	service, err := NewService(
		routerFunc(func(context.Context, string) (Decision, error) { routerCalled = true; return Decision{}, nil }), agent,
		noMemories, noConversation,
		proposalConfirmerFunc(func(_ context.Context, _ tool.Scope, utterance string) (string, bool, error) {
			confirmed = utterance
			return "  Saved existing proposal.  ", true, nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	const raw = "  Yes, save it.  "
	outcome, err := service.HandleUtterance(context.Background(), tool.Scope{}, "confirmation-turn", raw)
	if err != nil {
		t.Fatal(err)
	}
	if confirmed != raw || routerCalled || probe.basicCalls != 0 || probe.awareCalls != 0 || outcome.Decision.Action != ActionResolveProposal || outcome.Response != "Saved existing proposal." {
		t.Fatalf("proposal confirmation no longer preempts routing/agent execution: outcome=%#v router=%t basic=%d aware=%d", outcome, routerCalled, probe.basicCalls, probe.awareCalls)
	}
}

type originalRequestProbe struct {
	query        string
	scope        tool.Scope
	conversation session.Conversation
	cards        []memory.Card
	basicCalls   int
	awareCalls   int
	result       AgentResult
}

func (p *originalRequestProbe) capture(scope tool.Scope, query string, conversation session.Conversation, cards []memory.Card) {
	p.scope, p.query, p.conversation, p.cards = scope, query, conversation, cards
}

func (p *originalRequestProbe) Respond(_ context.Context, scope tool.Scope, query string, conversation session.Conversation, cards []memory.Card) (string, error) {
	p.basicCalls++
	p.capture(scope, query, conversation, cards)
	return p.result.Text, nil
}

type originalRequestAwareProbe struct{ *originalRequestProbe }

func (p *originalRequestAwareProbe) RespondWithResult(_ context.Context, scope tool.Scope, query string, conversation session.Conversation, cards []memory.Card) (AgentResult, error) {
	p.awareCalls++
	p.capture(scope, query, conversation, cards)
	return p.result, nil
}

func originalRequestTestAgent(aware bool, result AgentResult) (*originalRequestProbe, Agent) {
	probe := &originalRequestProbe{result: result}
	if aware {
		return probe, &originalRequestAwareProbe{probe}
	}
	return probe, probe
}

func assertOriginalRequestAgentPath(t *testing.T, probe *originalRequestProbe, aware bool) {
	t.Helper()
	wantBasic, wantAware := 1, 0
	if aware {
		wantBasic, wantAware = 0, 1
	}
	if probe.basicCalls != wantBasic || probe.awareCalls != wantAware {
		t.Fatalf("wrong agent interface path: basic=%d aware=%d want basic=%d aware=%d", probe.basicCalls, probe.awareCalls, wantBasic, wantAware)
	}
}
