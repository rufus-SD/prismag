// Package discovery enumerates the models actually available right now — from
// provider /models endpoints (CLI) or an agent-maintained cache (IDE).
package discovery

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/rufus-SD/prismag/internal/availability"
	"gopkg.in/yaml.v3"
)

// Base URLs are package vars so tests can point them at httptest servers.
var (
	AnthropicModelsURL  = "https://api.anthropic.com/v1/models"
	OpenAIModelsURL     = "https://api.openai.com/v1/models"
	OpenRouterModelsURL = "https://openrouter.ai/api/v1/models"
	httpTimeout         = 15 * time.Second
)

// Result is the set of available models for a context.
type Result struct {
	Context    string              `json:"context"`
	Source     string              `json:"source"` // "api" or "ide-cache"
	ByProvider map[string][]string `json:"byProvider"`
	Errors     map[string]string   `json:"errors,omitempty"`
	UpdatedAt  string              `json:"updatedAt,omitempty"` // IDE cache only
}

// All returns every available model id, sorted and de-duplicated.
func (r Result) All() []string {
	seen := map[string]bool{}
	var out []string
	for _, ms := range r.ByProvider {
		for _, m := range ms {
			if !seen[m] {
				seen[m] = true
				out = append(out, m)
			}
		}
	}
	sort.Strings(out)
	return out
}

// Has reports whether a model id is in the discovered set.
func (r Result) Has(id string) bool {
	for _, ms := range r.ByProvider {
		for _, m := range ms {
			if m == id {
				return true
			}
		}
	}
	return false
}

// Empty reports whether nothing was discovered.
func (r Result) Empty() bool { return len(r.All()) == 0 }

// Discover returns available models for the given context.
func Discover(ctx availability.Context, creds availability.Credentials) Result {
	if ctx == availability.ContextIDE {
		return discoverIDE()
	}
	return discoverAPI(creds)
}

func discoverAPI(creds availability.Credentials) Result {
	res := Result{
		Context:    availability.ContextCLI.String(),
		Source:     "api",
		ByProvider: map[string][]string{},
		Errors:     map[string]string{},
	}
	if creds.Anthropic {
		collect(&res, "anthropic", AnthropicModelsURL, map[string]string{
			"x-api-key":         os.Getenv("ANTHROPIC_API_KEY"),
			"anthropic-version": "2023-06-01",
		})
	}
	if creds.OpenAI {
		collect(&res, "openai", OpenAIModelsURL, map[string]string{
			"Authorization": "Bearer " + os.Getenv("OPENAI_API_KEY"),
		})
	}
	if creds.OpenRouter {
		collect(&res, "openrouter", OpenRouterModelsURL, map[string]string{
			"Authorization": "Bearer " + os.Getenv("OPENROUTER_API_KEY"),
		})
	}
	if len(res.Errors) == 0 {
		res.Errors = nil
	}
	return res
}

// modelsResponse matches the {"data":[{"id":...}]} shape all three providers use.
type modelsResponse struct {
	Data []struct {
		ID string `json:"id"`
	} `json:"data"`
}

func collect(res *Result, provider, url string, headers map[string]string) {
	var mr modelsResponse
	if err := getJSON(url, headers, &mr); err != nil {
		res.Errors[provider] = err.Error()
		return
	}
	ids := make([]string, 0, len(mr.Data))
	for _, d := range mr.Data {
		if d.ID != "" {
			ids = append(ids, d.ID)
		}
	}
	sort.Strings(ids)
	res.ByProvider[provider] = ids
}

func getJSON(url string, headers map[string]string, out interface{}) error {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	client := &http.Client{Timeout: httpTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		msg := strings.TrimSpace(string(body))
		if len(msg) > 200 {
			msg = msg[:200]
		}
		return fmt.Errorf("status %d: %s", resp.StatusCode, msg)
	}
	return json.Unmarshal(body, out)
}

// --- IDE cache ---

type ideCache struct {
	Models    []string `yaml:"models"`
	UpdatedAt string   `yaml:"updated_at"`
}

// CachePath returns the path of the agent-maintained IDE model cache.
func CachePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "prismag", "ide-models.yaml"), nil
}

func discoverIDE() Result {
	res := Result{
		Context:    availability.ContextIDE.String(),
		Source:     "ide-cache",
		ByProvider: map[string][]string{},
	}
	path, err := CachePath()
	if err != nil {
		res.Errors = map[string]string{"ide": err.Error()}
		return res
	}
	data, err := os.ReadFile(path)
	if err != nil {
		// No cache yet — not an error, just empty (agent should populate it).
		return res
	}
	var c ideCache
	if err := yaml.Unmarshal(data, &c); err != nil {
		res.Errors = map[string]string{"ide": err.Error()}
		return res
	}
	res.ByProvider["ide"] = c.Models
	res.UpdatedAt = c.UpdatedAt
	return res
}

// SetIDEModels writes the IDE model cache (called by the agent to keep prismag's
// view of the IDE's model picker fresh).
func SetIDEModels(models []string) (string, error) {
	clean := normalize(models)
	if len(clean) == 0 {
		return "", fmt.Errorf("no model ids given")
	}
	path, err := CachePath()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return "", err
	}
	c := ideCache{Models: clean, UpdatedAt: time.Now().UTC().Format(time.RFC3339)}
	out, err := yaml.Marshal(c)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, out, 0644); err != nil {
		return "", err
	}
	return path, nil
}

// normalize splits comma/space-separated input, trims, de-dupes, and sorts.
func normalize(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, raw := range in {
		for _, tok := range strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' || r == '\n' }) {
			t := strings.TrimSpace(tok)
			if t != "" && !seen[t] {
				seen[t] = true
				out = append(out, t)
			}
		}
	}
	sort.Strings(out)
	return out
}
