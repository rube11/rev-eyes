package websearch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"

	"github.com/rube11/rev-eyes/backend/internal/tool"
)

const (
	ProviderTavily  = "tavily"
	ProviderSearXNG = "searxng"

	DefaultSearXNGBaseURL = "http://127.0.0.1:8888"
)

var ErrUnsupportedProvider = errors.New("unsupported web search provider")

// Searcher is the web-search capability used by both interactive tool calls
// and background news watches.
type Searcher interface {
	tool.Tool
	SearchNews(context.Context, string) ([]Result, error)
}

// Config selects the web-search provider. An empty Provider retains the
// historical Tavily behavior. TavilyFallback is only applied to SearXNG.
type Config struct {
	Provider           string
	TavilyAPIKey       string
	SearXNGBaseURL     string
	TavilyFallback     bool
	ResearchExtraction bool
	// Optional additional outbound policy for fetched evidence pages. It runs
	// before DNS on initial URLs and every redirect, and never replaces SSRF
	// validation. Nil preserves normal production behavior.
	ExtractionURLPolicy func(*url.URL) error
}

// NewConfigured constructs a provider-neutral web search tool.
func NewConfigured(config Config) (Searcher, error) {
	provider := strings.ToLower(strings.TrimSpace(config.Provider))
	if provider == "" {
		provider = ProviderTavily
	}

	switch provider {
	case ProviderTavily:
		return New(config.TavilyAPIKey)
	case ProviderSearXNG:
		baseURL := strings.TrimSpace(config.SearXNGBaseURL)
		if baseURL == "" {
			baseURL = DefaultSearXNGBaseURL
		}
		primary, err := newSearXNG(baseURL, config.ResearchExtraction)
		if err != nil {
			return nil, err
		}
		if config.ExtractionURLPolicy != nil {
			validatePublic := primary.extractor.validateURL
			primary.extractor.validateURL = func(ctx context.Context, target *url.URL) error {
				if err := config.ExtractionURLPolicy(target); err != nil {
					return err
				}
				return validatePublic(ctx, target)
			}
		}
		if !config.TavilyFallback {
			return primary, nil
		}
		fallback, err := New(config.TavilyAPIKey)
		if err != nil {
			return nil, fmt.Errorf("configure Tavily fallback: %w", err)
		}
		return &fallbackSearcher{primary: primary, fallback: fallback}, nil
	default:
		return nil, fmt.Errorf("%w %q", ErrUnsupportedProvider, provider)
	}
}

type fallbackSearcher struct {
	primary  Searcher
	fallback Searcher
}

func (s *fallbackSearcher) Spec() tool.Spec {
	return s.primary.Spec()
}

func (s *fallbackSearcher) Execute(
	ctx context.Context,
	scope tool.Scope,
	arguments json.RawMessage,
) (tool.Result, error) {
	// Argument errors are provider-independent and must not spend a fallback
	// request or obscure useful validation feedback.
	if _, err := decodeSearchInput(arguments); err != nil {
		return tool.Result{}, err
	}
	requestCtx, cancel := context.WithTimeout(ctx, searxTotalTimeout)
	defer cancel()

	result, err := s.primary.Execute(requestCtx, scope, arguments)
	if err == nil {
		return result, nil
	}
	if ctx.Err() != nil {
		return tool.Result{}, ctx.Err()
	}
	if requestCtx.Err() != nil {
		return tool.Result{}, requestCtx.Err()
	}
	slog.WarnContext(ctx, "web search provider fallback",
		"from_provider", ProviderSearXNG,
		"to_provider", ProviderTavily,
		"reason", searchErrorClass(err),
	)
	return s.fallback.Execute(requestCtx, scope, arguments)
}

func (s *fallbackSearcher) SearchNews(ctx context.Context, query string) ([]Result, error) {
	query = strings.TrimSpace(query)
	if err := validateQuery(query); err != nil {
		return nil, err
	}
	requestCtx, cancel := context.WithTimeout(ctx, searxTotalTimeout)
	defer cancel()

	results, err := s.primary.SearchNews(requestCtx, query)
	if err == nil {
		return results, nil
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if requestCtx.Err() != nil {
		return nil, requestCtx.Err()
	}
	slog.WarnContext(ctx, "web search provider fallback",
		"from_provider", ProviderSearXNG,
		"to_provider", ProviderTavily,
		"reason", searchErrorClass(err),
	)
	return s.fallback.SearchNews(requestCtx, query)
}

var (
	_ Searcher = (*Tool)(nil)
	_ Searcher = (*searxNGTool)(nil)
	_ Searcher = (*fallbackSearcher)(nil)
)

func searchErrorClass(err error) string {
	switch {
	case err == nil:
		return "none"
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, ErrNoResults):
		return "no_results"
	default:
		return "provider_error"
	}
}
