package orchestrator

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rufus-SD/prismag/internal/availability"
	"github.com/rufus-SD/prismag/internal/backend"
	"github.com/rufus-SD/prismag/internal/contextstore"
	"github.com/rufus-SD/prismag/internal/registry"
)

type stubBackend struct {
	fn func(req backend.Request) (backend.Response, error)
}

func (s stubBackend) Complete(_ context.Context, req backend.Request) (backend.Response, error) {
	return s.fn(req)
}

// streamStub implements both Backend and Streamer; Stream emits the text in
// word-sized deltas and reports usage.
type streamStub struct {
	text string
}

func (s streamStub) Complete(_ context.Context, _ backend.Request) (backend.Response, error) {
	return backend.Response{Text: s.text, InTokens: 10, OutTokens: 5}, nil
}

func (s streamStub) Stream(_ context.Context, _ backend.Request, onDelta func(string)) (backend.Response, error) {
	for _, w := range strings.Fields(s.text) {
		onDelta(w + " ")
	}
	return backend.Response{Text: s.text, InTokens: 10, OutTokens: 5}, nil
}

func TestRunStreaming(t *testing.T) {
	reg := testRegistry(t)
	factory := func(a registry.Alias) (backend.Backend, error) {
		return streamStub{text: "hello streamed world"}, nil
	}
	var got strings.Builder
	res, err := Run(context.Background(), "@@fast: go", Options{
		Registry: reg,
		Factory:  factory,
		Creds:    availability.Credentials{OpenAI: true},
		Context:  availability.ContextCLI,
		Stream:   func(_, delta string) { got.WriteString(delta) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(got.String()) != "hello streamed world" {
		t.Fatalf("streamed deltas = %q", got.String())
	}
	if len(res.Tasks) != 1 || res.Tasks[0].OutTokens != 5 || res.Tasks[0].InTokens != 10 {
		t.Fatalf("usage not propagated: %+v", res.Tasks)
	}
}

func testRegistry(t *testing.T) *registry.Registry {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "registry.yaml")
	content := `aliases:
  opus:
    model: claude-test
    provider: anthropic
    agent: opus-planner
  composer:
    model: composer-test
    provider: cursor
    agent: composer-implementer
  fast:
    model: gpt-test
    provider: openai
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	r, err := registry.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func testFactory() BackendFactory {
	return func(a registry.Alias) (backend.Backend, error) {
		switch a.Provider {
		case registry.ProviderAnthropic:
			return stubBackend{fn: func(req backend.Request) (backend.Response, error) {
				if !strings.Contains(req.System, "shared preamble") {
					return backend.Response{}, fmt.Errorf("missing preamble in system")
				}
				return backend.Response{Text: "opus says: " + req.Prompt}, nil
			}}, nil
		case registry.ProviderOpenAI:
			return stubBackend{fn: func(req backend.Request) (backend.Response, error) {
				return backend.Response{Text: "fast says: " + req.Prompt}, nil
			}}, nil
		default:
			return nil, fmt.Errorf("unexpected provider %q", a.Provider)
		}
	}
}

func TestRunSerialChained(t *testing.T) {
	reg := testRegistry(t)
	store := contextstore.NewMemoryStore()
	creds := availability.Credentials{Anthropic: true, OpenAI: true}

	input := "shared preamble\n@@opus: plan auth\n@@fast: summarize plan"
	result, err := Run(context.Background(), input, Options{
		Registry:      reg,
		Store:         store,
		Factory:       testFactory(),
		Creds:         creds,
		Context:       availability.ContextCLI,
		SessionID:     "test",
		ContextBudget: 500,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Tasks) != 2 {
		t.Fatalf("tasks = %d", len(result.Tasks))
	}
	if !strings.Contains(result.Tasks[1].Output, "fast says") {
		t.Fatalf("task1 = %q", result.Tasks[1].Output)
	}

	// Second block should have received prior context in its system prompt via store.
	recalled, _ := store.Recall("test", "", 500)
	if !strings.Contains(recalled, "opus says") {
		t.Fatalf("store recall = %q, want prior opus output", recalled)
	}
}

func TestRunParallelNoChaining(t *testing.T) {
	reg := testRegistry(t)
	store := contextstore.NewMemoryStore()
	creds := availability.Credentials{Anthropic: true, OpenAI: true}

	input := "shared preamble\n@@opus: plan auth\n@@fast: summarize plan"
	_, err := Run(context.Background(), input, Options{
		Parallel:  true,
		Registry:  reg,
		Store:     store,
		Factory:   testFactory(),
		Creds:     creds,
		Context:   availability.ContextCLI,
		SessionID: "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	recalled, _ := store.Recall("test", "plan", 500)
	if recalled != "" {
		t.Fatalf("parallel should not chain, recall = %q", recalled)
	}
}

func TestRunSkipsUnavailableButRunsReady(t *testing.T) {
	reg := testRegistry(t)
	// opus has a key; fast (openai) does not.
	creds := availability.Credentials{Anthropic: true, OpenAI: false}

	input := "shared preamble\n@@opus: ok\n@@fast: needs key"
	res, err := Run(context.Background(), input, Options{
		Registry: reg,
		Factory:  testFactory(),
		Creds:    creds,
		Context:  availability.ContextCLI,
	})
	if err != nil {
		t.Fatalf("expected partial run, got error: %v", err)
	}
	if len(res.Tasks) != 2 {
		t.Fatalf("tasks = %d", len(res.Tasks))
	}
	if res.Tasks[0].Skipped {
		t.Error("opus should have run")
	}
	if !res.Tasks[1].Skipped {
		t.Error("fast should be skipped (no key)")
	}
	if res.Tasks[1].SkipNote == "" {
		t.Error("skip note should explain why")
	}
}

func TestRunAllUnavailableErrors(t *testing.T) {
	reg := testRegistry(t)
	_, err := Run(context.Background(), "@@opus: x\n@@fast: y", Options{
		Registry: reg,
		Factory:  testFactory(),
		Creds:    availability.Credentials{}, // no keys at all
		Context:  availability.ContextCLI,
	})
	if err == nil {
		t.Fatal("expected error when nothing is ready")
	}
}

func TestRunUnknownAlias(t *testing.T) {
	reg := testRegistry(t)
	_, err := Run(context.Background(), "@@missing: x", Options{
		Registry: reg,
		Factory:  testFactory(),
		Creds:    availability.Credentials{Anthropic: true, OpenAI: true},
		Context:  availability.ContextCLI,
	})
	if err == nil {
		t.Fatal("expected unknown alias error")
	}
}

func TestFormatMarkdown(t *testing.T) {
	md := FormatMarkdown(Result{
		Preamble: "ctx",
		Tasks: []TaskResult{{
			RawAlias: "opus",
			Model:    "claude-test",
			Output:   "hello",
		}},
	})
	if !strings.Contains(md, "## @@opus") || !strings.Contains(md, "hello") {
		t.Fatalf("md = %q", md)
	}
}
