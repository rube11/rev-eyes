package assistant

import (
	"context"
	"errors"
	"log/slog"
	"strings"

	"github.com/rube11/rev-eyes/backend/internal/memory"
	"github.com/rube11/rev-eyes/backend/internal/tool"
)

func (s *Service) loadProfile(ctx context.Context, scope tool.Scope) string {
	profile, err := s.memories.Profile(ctx, scope)
	if err != nil {
		slog.WarnContext(ctx, "user profile unavailable", "error", err)
		return "User profile temporarily unavailable. Do not claim the user has no saved memories."
	}
	return profile
}

func (s *Service) changeProfile(ctx context.Context, scope tool.Scope, decision Decision) (string, bool, error) {
	lookup := decision.MemoryLookup
	if strings.TrimSpace(lookup.Query) == "" {
		lookup.Query = decision.Query
	}
	if lookup.Empty() && len(lookup.Topics) == 0 && len(lookup.Kinds) == 0 {
		return "Which saved memory should I include or exclude from your profile?", false, nil
	}
	layer := memory.ProfileCore
	if decision.Action == ActionProfileExclude {
		layer = memory.ProfileDetail
	}
	count, err := s.memories.SetProfileOverride(ctx, scope, lookup, layer)
	if errors.Is(err, memory.ErrMemoryAmbiguous) {
		return "I found more than one matching memory. Which one do you mean?", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if count == 0 {
		return "I couldn't find that saved memory. Tell me the complete fact first.", false, nil
	}
	if layer == memory.ProfileDetail {
		return "Okay, I removed it from your always-present profile. It's still saved and searchable.", true, nil
	}
	return "Okay, I prioritized it in your always-present profile while it's current.", true, nil
}
