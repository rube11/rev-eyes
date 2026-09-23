package assistant

import (
	"strings"

	"github.com/rube11/rev-eyes/backend/internal/memory"
)

type Action string

const (
	ActionIgnore          Action = "ignore"
	ActionRespond         Action = "respond"
	ActionSuggestTip      Action = "suggest_tip"
	ActionStateUpdate     Action = "state_update"
	ActionStateTransition Action = "state_transition"
	ActionRemember        Action = "remember"
	ActionMemoryReview    Action = "memory_review"
	ActionMemoryCorrect   Action = "memory_correct"
	ActionMemoryForget    Action = "memory_forget"
	ActionProfileInclude  Action = "profile_include"
	ActionProfileExclude  Action = "profile_exclude"
	ActionResolveProposal Action = "resolve_proposal"
)

type Decision struct {
	Action          Action        `json:"action"`
	Query           string        `json:"query"`
	MemoryLookup    memory.Lookup `json:"memory_lookup"`
	MemoryReviewAll bool          `json:"memory_review_all"`
}

func normalizeDecision(decision Decision) Decision {
	decision.Query = strings.TrimSpace(decision.Query)
	decision = validateDecision(decision)
	if decision.Action == ActionMemoryReview && decision.MemoryReviewAll {
		decision.Query = ""
		decision.MemoryLookup = memory.Lookup{}
	} else {
		decision.MemoryReviewAll = false
	}
	if decision.Action == ActionRespond ||
		decision.Action == ActionSuggestTip ||
		decision.Action == ActionStateTransition ||
		decision.Action == ActionMemoryReview ||
		decision.Action == ActionMemoryForget ||
		decision.Action == ActionProfileInclude ||
		decision.Action == ActionProfileExclude {
		decision.MemoryLookup = decision.MemoryLookup.Normalize()
	} else {
		decision.MemoryLookup = memory.Lookup{}
	}
	return decision
}

func validateDecision(decision Decision) Decision {
	switch decision.Action {
	case ActionIgnore,
		ActionRespond,
		ActionSuggestTip,
		ActionStateUpdate,
		ActionStateTransition,
		ActionRemember,
		ActionMemoryReview,
		ActionMemoryCorrect,
		ActionMemoryForget,
		ActionProfileInclude,
		ActionProfileExclude:
		return decision
	default:
		return Decision{Action: ActionIgnore}
	}
}

func shouldIgnore(utterance string) bool {
	normalized := strings.ToLower(strings.TrimSpace(utterance))
	normalized = strings.Trim(normalized, ".,!?;:")

	if normalized == "" {
		return true
	}

	switch normalized {
	case "um", "uh", "hmm", "mm", "mhm":
		return true
	default:
		return false
	}
}
