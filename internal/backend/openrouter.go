package backend

import (
	"context"
	"fmt"
	"os"
)

const openRouterAPIURL = "https://openrouter.ai/api/v1"

// OpenRouter calls the OpenRouter chat API (OpenAI-compatible).
type OpenRouter struct {
	client *httpClient
}

// NewOpenRouter creates a backend with the given API key.
func NewOpenRouter(apiKey string) *OpenRouter {
	return &OpenRouter{
		client: newHTTPClient(openRouterAPIURL, map[string]string{
			"Authorization": "Bearer " + apiKey,
			"HTTP-Referer":  "https://github.com/rufus-SD/prismag",
			"X-Title":       "PRISMAG",
		}),
	}
}

// NewOpenRouterFromEnv reads OPENROUTER_API_KEY.
func NewOpenRouterFromEnv() (*OpenRouter, error) {
	key := os.Getenv("OPENROUTER_API_KEY")
	if key == "" {
		return nil, fmt.Errorf("OPENROUTER_API_KEY is not set")
	}
	return NewOpenRouter(key), nil
}

// Complete implements Backend.
func (r *OpenRouter) Complete(ctx context.Context, req Request) (Response, error) {
	body := chatReq{Model: req.Model, Messages: chatMessages(req.System, req.Prompt)}

	var out chatResp
	if err := r.client.postJSON(ctx, "/chat/completions", body, &out); err != nil {
		return Response{}, err
	}
	if len(out.Choices) == 0 || out.Choices[0].Message.Content == "" {
		return Response{}, fmt.Errorf("openrouter: empty response")
	}
	return Response{Text: out.Choices[0].Message.Content, InTokens: out.Usage.PromptTokens, OutTokens: out.Usage.CompletionTokens}, nil
}

// Stream implements Streamer.
func (r *OpenRouter) Stream(ctx context.Context, req Request, onDelta func(string)) (Response, error) {
	return streamChat(r.client, ctx, req, onDelta)
}
