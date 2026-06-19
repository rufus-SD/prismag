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

// Pick chooses the best concrete model id from pool for a given family, deterministically.
// Preference order: a valid explicit pin, then an exact family match, then the
// shortest id that has family as a prefix (tie-break lexicographic). Returns
// (id, true) on a match; (\"\", false) when pool is empty or nothing matches.
func Pick(pool []string, family, pinned string) (string, bool) {
	if len(pool) == 0 {
		return "", false
	}
	set := make(map[string]bool, len(pool))
	for _, m := range pool {
		set[m] = true
	}
	if pinned != "" && set[pinned] {
		return pinned, true
	}
	if family != "" && set[family] {
		return family, true
	}
	if family != "" {
		best := ""
		for _, m := range pool {
			if strings.HasPrefix(m, family) && (best == "" || preferModel(m, best)) {
				best = m
			}
		}
		if best != "" {
			return best, true
		}
	}
	return "", false
}

// preferModel ranks two prefix candidates: shorter (least extra suffix) wins,
// then lexicographic order for stability.
func preferModel(a, b string) bool {
	if len(a) != len(b) {
		return len(a) < len(b)
	}
	return a < b
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

// Discover returns available models for the given context (live, uncached).
func Discover(ctx availability.Context, creds availability.Credentials) Result {
	if ctx == availability.ContextIDE {
		return discoverIDE()
	}
	return discoverAPI(creds)
}

// Cached returns available models for the given context, using an on-disk cache
// to keep routing cheap. IDE context reads the agent-maintained cache directly;
// CLI context caches provider /models responses for apiCacheTTL and refreshes
// lazily. It degrades gracefully: on a failed/empty refresh it returns the last
// good cache if present, else an empty result (callers fall back to pinned ids).
func Cached(ctx availability.Context, creds availability.Credentials) Result {
	if ctx == availability.ContextIDE {
		return discoverIDE()
	}
	return cachedAPI(creds)
}

const apiCacheTTL = 12 * time.Hour

type apiCacheFile struct {
	UpdatedAt  time.Time           `json:"updated_at"`
	ByProvider map[string][]string `json:"byProvider"`
}

func apiCachePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "prismag", "models-cache.json"), nil
}

func cachedAPI(creds availability.Credentials) Result {
	path, _ := apiCachePath()
	if path != "" {
		if cf, ok := readAPICache(path); ok && time.Since(cf.UpdatedAt) < apiCacheTTL {
			return cacheResult(cf)
		}
	}
	res := discoverAPI(creds)
	if !res.Empty() {
		if path != "" {
			_ = writeAPICache(path, res.ByProvider)
		}
		return res
	}
	// Refresh failed or yielded nothing — serve the last good cache if we have one.
	if path != "" {
		if cf, ok := readAPICache(path); ok {
			return cacheResult(cf)
		}
	}
	return res
}

func cacheResult(cf apiCacheFile) Result {
	return Result{
		Context:    availability.ContextCLI.String(),
		Source:     "api-cache",
		ByProvider: cf.ByProvider,
		UpdatedAt:  cf.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

func readAPICache(path string) (apiCacheFile, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return apiCacheFile{}, false
	}
	var cf apiCacheFile
	if err := json.Unmarshal(data, &cf); err != nil || len(cf.ByProvider) == 0 {
		return apiCacheFile{}, false
	}
	return cf, true
}

func writeAPICache(path string, byProvider map[string][]string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(apiCacheFile{UpdatedAt: time.Now().UTC(), ByProvider: byProvider}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
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
