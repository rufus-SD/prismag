package discovery

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/rufus-SD/prismag/internal/availability"
)

func TestPick(t *testing.T) {
	pool := []string{"claude-opus-4-8", "claude-opus-4-8-thinking-high", "claude-opus-4-6", "gpt-5.5", "gpt-5.5-pro"}
	cases := []struct {
		name           string
		family, pinned string
		want           string
		ok             bool
	}{
		{"valid pin wins", "claude-opus-4-8", "claude-opus-4-6", "claude-opus-4-6", true},
		{"exact family", "claude-opus-4-8", "", "claude-opus-4-8", true},
		{"prefix shortest wins", "gpt-5.5", "", "gpt-5.5", true},
		{"prefix only (no exact)", "claude-opus-4-1", "claude-opus-4-1", "", false},
		{"cursor variant via prefix", "claude-opus-4-7", "claude-opus-4-7", "", false},
		{"no match", "gemini", "gemini-ultra", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := Pick(pool, c.family, c.pinned)
			if ok != c.ok || got != c.want {
				t.Fatalf("Pick(%q,%q) = (%q,%v), want (%q,%v)", c.family, c.pinned, got, ok, c.want, c.ok)
			}
		})
	}
}

// When the API id is absent but a Cursor-style superset id exists, the family
// prefix should resolve to it (the core self-healing case).
func TestPickResolvesCursorSuperset(t *testing.T) {
	pool := []string{"claude-opus-4-8-thinking-high", "composer-2.5-fast"}
	got, ok := Pick(pool, "claude-opus-4-8", "claude-opus-4-8")
	if !ok || got != "claude-opus-4-8-thinking-high" {
		t.Fatalf("Pick = (%q,%v), want claude-opus-4-8-thinking-high", got, ok)
	}
}

func TestPickEmptyPool(t *testing.T) {
	if _, ok := Pick(nil, "x", "x"); ok {
		t.Fatal("empty pool should not match")
	}
}

func TestDiscoverAPI(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":[{"id":"gpt-5.5-medium"},{"id":"gpt-5.3-codex"}]}`))
	}))
	defer srv.Close()

	old := OpenAIModelsURL
	OpenAIModelsURL = srv.URL
	defer func() { OpenAIModelsURL = old }()

	t.Setenv("OPENAI_API_KEY", "sk-test")
	res := Discover(availability.ContextCLI, availability.Credentials{OpenAI: true})

	if res.Source != "api" {
		t.Errorf("source = %q", res.Source)
	}
	if !res.Has("gpt-5.5-medium") || !res.Has("gpt-5.3-codex") {
		t.Errorf("missing models: %+v", res.ByProvider)
	}
	if len(res.All()) != 2 {
		t.Errorf("all = %v", res.All())
	}
}

func TestDiscoverAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		w.Write([]byte(`{"error":"bad key"}`))
	}))
	defer srv.Close()
	old := AnthropicModelsURL
	AnthropicModelsURL = srv.URL
	defer func() { AnthropicModelsURL = old }()

	t.Setenv("ANTHROPIC_API_KEY", "sk-bad")
	res := Discover(availability.ContextCLI, availability.Credentials{Anthropic: true})
	if res.Errors["anthropic"] == "" {
		t.Error("expected an error for 401")
	}
	if !res.Empty() {
		t.Error("expected empty model set on error")
	}
}

func TestIDECacheRoundTrip(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	path, err := SetIDEModels([]string{"composer-2.5-fast", "gpt-5.5-medium", "composer-2.5-fast"})
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(filepath.Dir(path)) != filepath.Join(home, ".config") {
		t.Errorf("cache path = %q", path)
	}

	res := Discover(availability.ContextIDE, availability.Credentials{})
	if res.Source != "ide-cache" {
		t.Errorf("source = %q", res.Source)
	}
	all := res.All()
	if len(all) != 2 { // de-duped
		t.Fatalf("all = %v", all)
	}
	if !res.Has("gpt-5.5-medium") {
		t.Errorf("missing model: %v", all)
	}
	if res.UpdatedAt == "" {
		t.Error("expected updated_at timestamp")
	}
}

func TestDiscoverIDENoCache(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	res := Discover(availability.ContextIDE, availability.Credentials{})
	if !res.Empty() {
		t.Error("expected empty result with no cache")
	}
	if _, err := os.Stat(filepath.Join(os.Getenv("HOME"), ".config", "prismag", "ide-models.yaml")); err == nil {
		t.Error("discovery should not create the cache file")
	}
}
