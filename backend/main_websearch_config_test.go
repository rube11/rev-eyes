package main

import (
	"errors"
	"reflect"
	"testing"

	"github.com/rube11/rev-eyes/backend/internal/tool/websearch"
)

func TestWebSearchConfigFromEnvironmentDefaultsToTavily(t *testing.T) {
	t.Setenv("WEB_SEARCH_PROVIDER", "")
	t.Setenv("TAVILY_API_KEY", " tavily-key ")
	t.Setenv("SEARXNG_BASE_URL", "")
	t.Setenv("WEB_SEARCH_TAVILY_FALLBACK", "")
	t.Setenv("WEB_SEARCH_RESEARCH_EXTRACTION", "")

	want := websearch.Config{
		Provider:     "tavily",
		TavilyAPIKey: "tavily-key",
	}
	if got := webSearchConfigFromEnvironment(); !reflect.DeepEqual(got, want) {
		t.Fatalf("webSearchConfigFromEnvironment() = %#v, want %#v", got, want)
	}
}

func TestWebSearchConfigFromEnvironmentReadsSearXNGOptions(t *testing.T) {
	t.Setenv("WEB_SEARCH_PROVIDER", " SearXNG ")
	t.Setenv("TAVILY_API_KEY", " fallback-key ")
	t.Setenv("SEARXNG_BASE_URL", " https://search.example.com/ ")
	t.Setenv("WEB_SEARCH_TAVILY_FALLBACK", " TrUe ")
	t.Setenv("WEB_SEARCH_RESEARCH_EXTRACTION", " true ")

	want := websearch.Config{
		Provider:           "searxng",
		TavilyAPIKey:       "fallback-key",
		SearXNGBaseURL:     "https://search.example.com/",
		TavilyFallback:     true,
		ResearchExtraction: true,
	}
	if got := webSearchConfigFromEnvironment(); !reflect.DeepEqual(got, want) {
		t.Fatalf("webSearchConfigFromEnvironment() = %#v, want %#v", got, want)
	}
}

func TestEnvironmentEnabledRequiresTrue(t *testing.T) {
	for _, test := range []struct {
		value string
		want  bool
	}{
		{"true", true},
		{" TRUE ", true},
		{"false", false},
		{"1", false},
		{"yes", false},
		{"", false},
	} {
		if got := environmentEnabled(test.value); got != test.want {
			t.Fatalf("environmentEnabled(%q) = %t, want %t", test.value, got, test.want)
		}
	}
}

func TestNewWebSearchFromEnvironmentRejectsInvalidProvider(t *testing.T) {
	t.Setenv("WEB_SEARCH_PROVIDER", "unknown")
	t.Setenv("TAVILY_API_KEY", "")
	t.Setenv("SEARXNG_BASE_URL", "")
	t.Setenv("WEB_SEARCH_TAVILY_FALLBACK", "")
	t.Setenv("WEB_SEARCH_RESEARCH_EXTRACTION", "")

	if _, err := newWebSearchFromEnvironment(); !errors.Is(err, websearch.ErrUnsupportedProvider) {
		t.Fatalf("newWebSearchFromEnvironment() error = %v, want unsupported provider", err)
	}
}

func TestNewWebSearchFromEnvironmentRequiresDefaultProviderConfig(t *testing.T) {
	t.Setenv("WEB_SEARCH_PROVIDER", "")
	t.Setenv("TAVILY_API_KEY", "")
	t.Setenv("SEARXNG_BASE_URL", "")
	t.Setenv("WEB_SEARCH_TAVILY_FALLBACK", "")
	t.Setenv("WEB_SEARCH_RESEARCH_EXTRACTION", "")

	if _, err := newWebSearchFromEnvironment(); !errors.Is(err, websearch.ErrAPIKeyRequired) {
		t.Fatalf("newWebSearchFromEnvironment() error = %v, want missing Tavily API key", err)
	}
}
