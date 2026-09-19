package openai

import (
	"github.com/rube11/rev-eyes/backend/internal/assistant/openai/extraction"
	"github.com/rube11/rev-eyes/backend/internal/assistant/openai/routing"
	"github.com/rube11/rev-eyes/backend/internal/assistant/openai/tooling"
)

// These aliases keep existing live evaluation code source-compatible while
// production wiring imports each focused package directly.
type RouterEnricher = routing.Enricher
type ToolArgumentBuilder = tooling.ArgumentBuilder
type MemoryExtractor = extraction.MemoryExtractor

func NewRouterEnricher(apiKey, model string) (*RouterEnricher, error) {
	return routing.New(apiKey, model)
}

func NewToolArgumentBuilder(apiKey, model string) (*ToolArgumentBuilder, error) {
	return tooling.NewArgumentBuilder(apiKey, model)
}

func NewMemoryExtractor(apiKey, model string) (*MemoryExtractor, error) {
	return extraction.NewMemoryExtractor(apiKey, model)
}
