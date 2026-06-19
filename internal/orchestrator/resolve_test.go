package orchestrator

import (
	"strings"
	"testing"

	"github.com/rufus-SD/prismag/internal/availability"
	"github.com/rufus-SD/prismag/internal/discovery"
	"github.com/rufus-SD/prismag/internal/registry"
)

func cliModels(byProvider map[string][]string) discovery.Result {
	return discovery.Result{Context: "cli", Source: "api", ByProvider: byProvider}
}

func TestResolveModelExactPasses(t *testing.T) {
	a := registry.Alias{Model: "claude-opus-4-8", Provider: registry.ProviderAnthropic}
	models := cliModels(map[string][]string{"anthropic": {"claude-opus-4-8", "claude-opus-4-6"}})
	got, note := resolveModel(a, models, availability.ContextCLI)
	if got != "claude-opus-4-8" || note != "" {
		t.Fatalf("got (%q,%q), want (claude-opus-4-8, \"\")", got, note)
	}
}

func TestResolveModelSelfHeals(t *testing.T) {
	// Pinned id is gone from the live list, but a family-prefixed id exists.
	a := registry.Alias{Model: "claude-opus-4-8", Provider: registry.ProviderAnthropic}
	models := cliModels(map[string][]string{"anthropic": {"claude-opus-4-8-20260601"}})
	got, note := resolveModel(a, models, availability.ContextCLI)
	if got != "claude-opus-4-8-20260601" {
		t.Fatalf("got %q, want self-healed claude-opus-4-8-20260601", got)
	}
	if !strings.Contains(note, "resolved") {
		t.Fatalf("expected a resolution note, got %q", note)
	}
}

func TestResolveModelMatchFamily(t *testing.T) {
	a := registry.Alias{Match: "gpt-5.5", Model: "gpt-5.5-legacy", Provider: registry.ProviderOpenAI}
	models := cliModels(map[string][]string{"openai": {"gpt-5.5", "gpt-5.5-pro"}})
	got, _ := resolveModel(a, models, availability.ContextCLI)
	if got != "gpt-5.5" {
		t.Fatalf("got %q, want gpt-5.5 (matched via family)", got)
	}
}

func TestResolveModelUnverifiedWhenNoList(t *testing.T) {
	a := registry.Alias{Model: "claude-opus-4-8", Provider: registry.ProviderAnthropic}
	got, note := resolveModel(a, discovery.Result{}, availability.ContextCLI)
	if got != "claude-opus-4-8" || note != "" {
		t.Fatalf("offline path should keep pinned silently, got (%q,%q)", got, note)
	}
}

func TestResolveModelNotFoundNote(t *testing.T) {
	a := registry.Alias{Model: "claude-opus-4-8-thinking-high", Provider: registry.ProviderAnthropic}
	models := cliModels(map[string][]string{"anthropic": {"claude-opus-4-6"}})
	got, note := resolveModel(a, models, availability.ContextCLI)
	if got != "claude-opus-4-8-thinking-high" {
		t.Fatalf("unmatched pin should be preserved, got %q", got)
	}
	if !strings.Contains(note, "not in the live") {
		t.Fatalf("expected not-found note, got %q", note)
	}
}

func TestResolveModelLocalUntouched(t *testing.T) {
	a := registry.Alias{Model: "qwen2.5-coder:7b", Provider: registry.ProviderOllama}
	// Even with a (bogus) list present, local providers aren't enumerated.
	models := cliModels(map[string][]string{"ollama": {"something-else"}})
	got, note := resolveModel(a, models, availability.ContextCLI)
	if got != "qwen2.5-coder:7b" || note != "" {
		t.Fatalf("local model must stay pinned, got (%q,%q)", got, note)
	}
}

func TestResolveModelIDEContextUsesCache(t *testing.T) {
	a := registry.Alias{Model: "claude-opus-4-8", Provider: registry.ProviderAnthropic}
	ide := discovery.Result{Context: "ide", Source: "ide-cache", ByProvider: map[string][]string{
		"ide": {"claude-opus-4-8-thinking-high", "composer-2.5-fast"},
	}}
	got, _ := resolveModel(a, ide, availability.ContextIDE)
	if got != "claude-opus-4-8-thinking-high" {
		t.Fatalf("IDE context should resolve to Cursor id, got %q", got)
	}
}
