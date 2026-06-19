// Package backend calls LLM provider APIs directly (no SDKs).
package backend

import (
	"context"
	"fmt"

	"github.com/rufus-SD/prismag/internal/registry"
)

// Request is one model completion call.
type Request struct {
	Model  string
	System string // shared preamble / instructions
	Prompt string // the task for this block
}

// Response is the model's text output, with optional token usage.
type Response struct {
	Text      string
	InTokens  int
	OutTokens int
}

// Backend completes a prompt against one provider.
type Backend interface {
	Complete(ctx context.Context, req Request) (Response, error)
}

// Streamer is an optional capability: a backend that can stream the completion
// token-by-token, invoking onDelta for each text chunk and returning the full
// Response (with usage) at the end.
type Streamer interface {
	Stream(ctx context.Context, req Request, onDelta func(string)) (Response, error)
}

// New returns a backend for the given provider using env credentials.
func New(p registry.Provider) (Backend, error) {
	switch p {
	case registry.ProviderAnthropic:
		return NewAnthropicFromEnv()
	case registry.ProviderOpenAI:
		return NewOpenAIFromEnv()
	case registry.ProviderOpenRouter:
		return NewOpenRouterFromEnv()
	case registry.ProviderCursor:
		return nil, fmt.Errorf("cursor provider requires the Cursor SDK (not available in CLI yet)")
	default:
		return nil, fmt.Errorf("unknown provider %q", p)
	}
}
