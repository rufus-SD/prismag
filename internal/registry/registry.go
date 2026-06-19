// Package registry loads and validates alias → model mappings from registry.yaml.
package registry

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Provider names the backend that serves an alias.
type Provider string

const (
	ProviderAnthropic  Provider = "anthropic"
	ProviderOpenAI     Provider = "openai"
	ProviderOpenRouter Provider = "openrouter"
	ProviderCursor     Provider = "cursor"
	// ProviderOllama and ProviderVLLM are local, OpenAI-compatible servers.
	// They need no API key — just a reachable base URL.
	ProviderOllama Provider = "ollama"
	ProviderVLLM   Provider = "vllm"
)

var validProviders = map[Provider]bool{
	ProviderAnthropic:  true,
	ProviderOpenAI:     true,
	ProviderOpenRouter: true,
	ProviderCursor:     true,
	ProviderOllama:     true,
	ProviderVLLM:       true,
}

// LocalProviders are providers served by a local, OpenAI-compatible endpoint
// (no API key, no cloud).
func (p Provider) IsLocal() bool {
	return p == ProviderOllama || p == ProviderVLLM
}

// Alias maps one @@tag to a concrete model and backend.
type Alias struct {
	Model string `yaml:"model"`
	// Match is an optional model family/prefix (e.g. "claude-opus-4-8"). When set,
	// PRISMAG resolves it against the live model list for the active context and
	// picks the best concrete id, so the alias self-heals across renames/contexts.
	// If empty, Model doubles as the family. Model still serves as the fallback
	// when no live list is available (offline / no key).
	Match    string   `yaml:"match,omitempty"`
	Provider Provider `yaml:"provider"`
	Agent    string   `yaml:"agent,omitempty"` // IDE subagent that runs this block (delegation target)
	// BaseURL overrides the endpoint for OpenAI-compatible providers
	// (ollama/vllm). Empty falls back to the provider default or its env var.
	BaseURL     string `yaml:"base_url,omitempty"`
	Description string `yaml:"description,omitempty"`
}

// Registry holds normalized alias → model mappings.
type Registry struct {
	path    string
	aliases map[string]Alias // keys are lowercased
}

type file struct {
	Aliases map[string]Alias `yaml:"aliases"`
}

// Load reads and validates a registry YAML file.
func Load(path string) (*Registry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read registry %s: %w", path, err)
	}

	var f file
	if err := yaml.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parse registry %s: %w", path, err)
	}

	r := &Registry{
		path:    path,
		aliases: make(map[string]Alias, len(f.Aliases)),
	}

	if len(f.Aliases) == 0 {
		return nil, fmt.Errorf("registry %s: no aliases defined", path)
	}

	for rawName, entry := range f.Aliases {
		name := strings.ToLower(strings.TrimSpace(rawName))
		if name == "" {
			return nil, fmt.Errorf("registry %s: empty alias name", path)
		}
		if err := validateAlias(path, name, entry); err != nil {
			return nil, err
		}
		if _, exists := r.aliases[name]; exists {
			return nil, fmt.Errorf("registry %s: duplicate alias %q", path, name)
		}
		entry.Provider = Provider(strings.ToLower(string(entry.Provider)))
		r.aliases[name] = entry
	}

	return r, nil
}

func validateAlias(path, name string, a Alias) error {
	if strings.TrimSpace(a.Model) == "" && strings.TrimSpace(a.Match) == "" {
		return fmt.Errorf("registry %s: alias %q: model or match is required", path, name)
	}
	provider := Provider(strings.ToLower(string(a.Provider)))
	if provider == "" {
		return fmt.Errorf("registry %s: alias %q: provider is required", path, name)
	}
	if !validProviders[provider] {
		return fmt.Errorf("registry %s: alias %q: unknown provider %q (want anthropic, openai, openrouter, cursor, ollama, vllm)", path, name, provider)
	}
	return nil
}

// Path returns the file this registry was loaded from.
func (r *Registry) Path() string {
	return r.path
}

// Resolve looks up an alias (case-insensitive).
func (r *Registry) Resolve(alias string) (Alias, bool) {
	a, ok := r.aliases[strings.ToLower(alias)]
	return a, ok
}

// Names returns all alias names sorted alphabetically.
func (r *Registry) Names() []string {
	names := make([]string, 0, len(r.aliases))
	for name := range r.aliases {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Suggest returns up to max alias names closest to the given (unknown) name,
// nearest first — for "did you mean?" hints on a typo'd @@alias.
func (r *Registry) Suggest(name string, max int) []string {
	name = strings.ToLower(strings.TrimSpace(name))
	names := r.Names()
	sort.SliceStable(names, func(i, j int) bool {
		return levenshtein(name, names[i]) < levenshtein(name, names[j])
	})
	if max > 0 && len(names) > max {
		names = names[:max]
	}
	return names
}

// levenshtein is the classic edit distance between two strings.
func levenshtein(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	cur := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min3(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(rb)]
}

func min3(a, b, c int) int {
	m := a
	if b < m {
		m = b
	}
	if c < m {
		m = c
	}
	return m
}

// All returns every alias entry keyed by normalized name.
func (r *Registry) All() map[string]Alias {
	out := make(map[string]Alias, len(r.aliases))
	for k, v := range r.aliases {
		out[k] = v
	}
	return out
}

// DefaultPath returns the first existing registry file on the search path.
func DefaultPath() (string, error) {
	for _, p := range searchPaths() {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("registry not found (looked in %s)", strings.Join(searchPaths(), ", "))
}

// LoadDefault loads the registry from DefaultPath.
func LoadDefault() (*Registry, error) {
	path, err := DefaultPath()
	if err != nil {
		return nil, err
	}
	return Load(path)
}

func searchPaths() []string {
	var paths []string
	if env := os.Getenv("PRISMAG_REGISTRY"); env != "" {
		paths = append(paths, env)
	}
	// Walk up from cwd so `prismag` works from any subdirectory of a project
	// that has a registry.yaml at (or above) it.
	if cwd, err := os.Getwd(); err == nil {
		dir := cwd
		for {
			paths = append(paths, filepath.Join(dir, "registry.yaml"))
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		paths = append(paths, filepath.Join(home, ".config", "prismag", "registry.yaml"))
	}
	return paths
}
