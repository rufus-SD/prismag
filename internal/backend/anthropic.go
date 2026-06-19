package backend

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

const anthropicAPIURL = "https://api.anthropic.com/v1"

// Anthropic calls the Anthropic Messages API.
type Anthropic struct {
	client *httpClient
}

// NewAnthropic creates a backend with the given API key.
func NewAnthropic(apiKey string) *Anthropic {
	return &Anthropic{
		client: newHTTPClient(anthropicAPIURL, map[string]string{
			"x-api-key":         apiKey,
			"anthropic-version": "2023-06-01",
		}),
	}
}

// NewAnthropicFromEnv reads ANTHROPIC_API_KEY.
func NewAnthropicFromEnv() (*Anthropic, error) {
	key := os.Getenv("ANTHROPIC_API_KEY")
	if key == "" {
		return nil, fmt.Errorf("ANTHROPIC_API_KEY is not set")
	}
	return NewAnthropic(key), nil
}

type anthropicReq struct {
	Model     string         `json:"model"`
	MaxTokens int            `json:"max_tokens"`
	System    string         `json:"system,omitempty"`
	Messages  []anthropicMsg `json:"messages"`
	Stream    bool           `json:"stream,omitempty"`
}

type anthropicMsg struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type anthropicResp struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	Usage struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

// Complete implements Backend.
func (a *Anthropic) Complete(ctx context.Context, req Request) (Response, error) {
	body := anthropicReq{
		Model:     req.Model,
		MaxTokens: 4096,
		System:    req.System,
		Messages: []anthropicMsg{
			{Role: "user", Content: req.Prompt},
		},
	}

	var out anthropicResp
	if err := a.client.postJSON(ctx, "/messages", body, &out); err != nil {
		return Response{}, err
	}

	var text string
	for _, block := range out.Content {
		if block.Type == "text" {
			text += block.Text
		}
	}
	if text == "" {
		return Response{}, fmt.Errorf("anthropic: empty response")
	}
	return Response{Text: text, InTokens: out.Usage.InputTokens, OutTokens: out.Usage.OutputTokens}, nil
}

// Stream implements Streamer using the Anthropic Messages streaming API.
func (a *Anthropic) Stream(ctx context.Context, req Request, onDelta func(string)) (Response, error) {
	body := anthropicReq{
		Model:     req.Model,
		MaxTokens: 4096,
		System:    req.System,
		Messages:  []anthropicMsg{{Role: "user", Content: req.Prompt}},
		Stream:    true,
	}
	var text strings.Builder
	var inTok, outTok int
	err := a.client.postStream(ctx, "/messages", body, func(data []byte) error {
		var ev struct {
			Type  string `json:"type"`
			Delta struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"delta"`
			Message struct {
				Usage struct {
					InputTokens int `json:"input_tokens"`
				} `json:"usage"`
			} `json:"message"`
			Usage struct {
				OutputTokens int `json:"output_tokens"`
			} `json:"usage"`
		}
		if err := json.Unmarshal(data, &ev); err != nil {
			return nil // ignore malformed keepalive/event lines
		}
		switch ev.Type {
		case "message_start":
			inTok = ev.Message.Usage.InputTokens
		case "content_block_delta":
			if ev.Delta.Text != "" {
				text.WriteString(ev.Delta.Text)
				onDelta(ev.Delta.Text)
			}
		case "message_delta":
			if ev.Usage.OutputTokens > 0 {
				outTok = ev.Usage.OutputTokens
			}
		}
		return nil
	})
	if err != nil {
		return Response{}, err
	}
	if text.Len() == 0 {
		return Response{}, fmt.Errorf("anthropic: empty stream")
	}
	return Response{Text: text.String(), InTokens: inTok, OutTokens: outTok}, nil
}
