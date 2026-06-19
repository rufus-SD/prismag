package backend

import "os"

// Local, OpenAI-compatible servers. Both Ollama and vLLM expose the OpenAI
// /chat/completions API (including SSE streaming), so they reuse the OpenAI
// backend with a different base URL and no required API key.
const (
	defaultOllamaURL = "http://localhost:11434/v1"
	defaultVLLMURL   = "http://localhost:8000/v1"
)

// NewOllama returns a backend for a local Ollama server. Resolution order for
// the endpoint: explicit baseURL (from registry) → OLLAMA_BASE_URL → default.
// Ollama needs no key; OLLAMA_API_KEY is honored if set (e.g. behind a proxy).
func NewOllama(baseURL string) *OpenAI {
	if baseURL == "" {
		baseURL = envOr("OLLAMA_BASE_URL", defaultOllamaURL)
	}
	return newOpenAICompatible(baseURL, os.Getenv("OLLAMA_API_KEY"))
}

// NewVLLM returns a backend for a local/self-hosted vLLM OpenAI server.
// Endpoint: explicit baseURL → VLLM_BASE_URL → default. VLLM_API_KEY is used
// when the server is started with --api-key.
func NewVLLM(baseURL string) *OpenAI {
	if baseURL == "" {
		baseURL = envOr("VLLM_BASE_URL", defaultVLLMURL)
	}
	return newOpenAICompatible(baseURL, os.Getenv("VLLM_API_KEY"))
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
