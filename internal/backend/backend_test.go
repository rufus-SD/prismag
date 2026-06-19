package backend

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rufus-SD/prismag/internal/registry"
)

func TestAnthropicComplete(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		if r.Header.Get("x-api-key") != "test-key" {
			t.Fatalf("missing api key header")
		}
		var body anthropicReq
		json.NewDecoder(r.Body).Decode(&body)
		if body.Model != "claude-test" || body.System != "preamble" {
			t.Fatalf("body = %+v", body)
		}
		json.NewEncoder(w).Encode(anthropicResp{
			Content: []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			}{{Type: "text", Text: "hello from opus"}},
		})
	}))
	defer srv.Close()

	// Point client at test server.
	a := NewAnthropic("test-key")
	a.client.baseURL = srv.URL + "/v1"

	resp, err := a.Complete(context.Background(), Request{
		Model:  "claude-test",
		System: "preamble",
		Prompt: "do the thing",
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Text != "hello from opus" {
		t.Fatalf("text = %q", resp.Text)
	}
}

func TestOpenAIComplete(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		var body chatReq
		json.NewDecoder(r.Body).Decode(&body)
		if len(body.Messages) != 2 || body.Messages[0].Role != "system" {
			t.Fatalf("messages = %+v", body.Messages)
		}
		json.NewEncoder(w).Encode(chatResp{
			Choices: []struct {
				Message chatMsg `json:"message"`
			}{{Message: chatMsg{Role: "assistant", Content: "done"}}},
		})
	}))
	defer srv.Close()

	o := NewOpenAI("sk-test")
	o.client.baseURL = srv.URL + "/v1"

	resp, err := o.Complete(context.Background(), Request{
		Model:  "gpt-test",
		System: "ctx",
		Prompt: "task",
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Text != "done" {
		t.Fatalf("text = %q", resp.Text)
	}
}

func TestNewProviderFactory(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "k")
	b, err := New(registry.Alias{Provider: registry.ProviderAnthropic})
	if err != nil || b == nil {
		t.Fatalf("anthropic: err=%v b=%v", err, b)
	}

	t.Setenv("OPENAI_API_KEY", "k")
	b, err = New(registry.Alias{Provider: registry.ProviderOpenAI})
	if err != nil || b == nil {
		t.Fatalf("openai: err=%v", err)
	}

	// Local providers need no key and never error at construction.
	for _, p := range []registry.Provider{registry.ProviderOllama, registry.ProviderVLLM} {
		b, err = New(registry.Alias{Provider: p})
		if err != nil || b == nil {
			t.Fatalf("%s: err=%v b=%v", p, err, b)
		}
	}

	_, err = New(registry.Alias{Provider: registry.ProviderCursor})
	if err == nil {
		t.Fatal("expected error for cursor")
	}
}

func TestLocalBackendBaseURL(t *testing.T) {
	// Per-alias base_url wins and the OpenAI-compatible client hits it.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "" {
			t.Fatalf("local backend should send no auth header by default")
		}
		json.NewEncoder(w).Encode(chatResp{
			Choices: []struct {
				Message chatMsg `json:"message"`
			}{{Message: chatMsg{Role: "assistant", Content: "local ok"}}},
		})
	}))
	defer srv.Close()

	b, err := New(registry.Alias{Provider: registry.ProviderOllama, Model: "qwen2.5-coder:7b", BaseURL: srv.URL + "/v1"})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := b.Complete(context.Background(), Request{Model: "qwen2.5-coder:7b", Prompt: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Text != "local ok" {
		t.Fatalf("text = %q", resp.Text)
	}
}

func TestLocalBackendEnvDefault(t *testing.T) {
	t.Setenv("OLLAMA_BASE_URL", "http://example.test:1234/v1")
	o := NewOllama("")
	if o.client.baseURL != "http://example.test:1234/v1" {
		t.Fatalf("base url = %q, want env override", o.client.baseURL)
	}
	o2 := NewOllama("")
	t.Setenv("OLLAMA_BASE_URL", "")
	o3 := NewOllama("")
	if o3.client.baseURL != defaultOllamaURL {
		t.Fatalf("base url = %q, want default %q", o3.client.baseURL, defaultOllamaURL)
	}
	_ = o2
}
