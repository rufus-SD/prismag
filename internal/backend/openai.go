package backend

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

const openAIAPIURL = "https://api.openai.com/v1"

// OpenAI calls the OpenAI Chat Completions API.
type OpenAI struct {
	client *httpClient
}

// NewOpenAI creates a backend with the given API key.
func NewOpenAI(apiKey string) *OpenAI {
	return &OpenAI{
		client: newHTTPClient(openAIAPIURL, map[string]string{
			"Authorization": "Bearer " + apiKey,
		}),
	}
}

// NewOpenAIFromEnv reads OPENAI_API_KEY.
func NewOpenAIFromEnv() (*OpenAI, error) {
	key := os.Getenv("OPENAI_API_KEY")
	if key == "" {
		return nil, fmt.Errorf("OPENAI_API_KEY is not set")
	}
	return NewOpenAI(key), nil
}

type chatReq struct {
	Model         string      `json:"model"`
	Messages      []chatMsg   `json:"messages"`
	Stream        bool        `json:"stream,omitempty"`
	StreamOptions *streamOpts `json:"stream_options,omitempty"`
}

type streamOpts struct {
	IncludeUsage bool `json:"include_usage"`
}

func chatMessages(system, prompt string) []chatMsg {
	msgs := []chatMsg{}
	if system != "" {
		msgs = append(msgs, chatMsg{Role: "system", Content: system})
	}
	return append(msgs, chatMsg{Role: "user", Content: prompt})
}

// streamChat runs an OpenAI-compatible chat completion as an SSE stream. Shared
// by the OpenAI and OpenRouter backends.
func streamChat(client *httpClient, ctx context.Context, req Request, onDelta func(string)) (Response, error) {
	body := chatReq{
		Model:         req.Model,
		Messages:      chatMessages(req.System, req.Prompt),
		Stream:        true,
		StreamOptions: &streamOpts{IncludeUsage: true},
	}
	var text strings.Builder
	var inTok, outTok int
	err := client.postStream(ctx, "/chat/completions", body, func(data []byte) error {
		var ev struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
			Usage struct {
				PromptTokens     int `json:"prompt_tokens"`
				CompletionTokens int `json:"completion_tokens"`
			} `json:"usage"`
		}
		if err := json.Unmarshal(data, &ev); err != nil {
			return nil
		}
		if len(ev.Choices) > 0 && ev.Choices[0].Delta.Content != "" {
			text.WriteString(ev.Choices[0].Delta.Content)
			onDelta(ev.Choices[0].Delta.Content)
		}
		if ev.Usage.CompletionTokens > 0 {
			inTok = ev.Usage.PromptTokens
			outTok = ev.Usage.CompletionTokens
		}
		return nil
	})
	if err != nil {
		return Response{}, err
	}
	if text.Len() == 0 {
		return Response{}, fmt.Errorf("empty stream")
	}
	return Response{Text: text.String(), InTokens: inTok, OutTokens: outTok}, nil
}

type chatMsg struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatResp struct {
	Choices []struct {
		Message chatMsg `json:"message"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
}

// Complete implements Backend.
func (o *OpenAI) Complete(ctx context.Context, req Request) (Response, error) {
	body := chatReq{Model: req.Model, Messages: chatMessages(req.System, req.Prompt)}

	var out chatResp
	if err := o.client.postJSON(ctx, "/chat/completions", body, &out); err != nil {
		return Response{}, err
	}
	if len(out.Choices) == 0 || out.Choices[0].Message.Content == "" {
		return Response{}, fmt.Errorf("openai: empty response")
	}
	return Response{Text: out.Choices[0].Message.Content, InTokens: out.Usage.PromptTokens, OutTokens: out.Usage.CompletionTokens}, nil
}

// Stream implements Streamer.
func (o *OpenAI) Stream(ctx context.Context, req Request, onDelta func(string)) (Response, error) {
	return streamChat(o.client, ctx, req, onDelta)
}
