// Package secrets loads and stores provider API keys. With maind present, keys
// live ONLY in maind's encrypted brain (C-only); without maind, they fall back
// to ~/.config/prismag/.env (chmod 0600). Stored keys are hydrated into the
// process environment so the rest of PRISMAG can read them via os.Getenv.
package secrets

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Providers maps a provider name to its environment variable.
var Providers = map[string]string{
	"anthropic":  "ANTHROPIC_API_KEY",
	"openai":     "OPENAI_API_KEY",
	"openrouter": "OPENROUTER_API_KEY",
}

// ProviderOrder is a stable display/iteration order.
var ProviderOrder = []string{"anthropic", "openai", "openrouter"}

// Backend names where a key is stored or loaded from.
type Backend string

const (
	BackendEnv    Backend = "env"
	BackendMaind  Backend = "maind"
	BackendDotenv Backend = ".env"
	BackendNone   Backend = "none"
)

// EnvVar returns the environment variable for a provider ("" if unknown).
func EnvVar(provider string) string { return Providers[provider] }

// MaindPath returns the maind binary path and whether it's on PATH.
func MaindPath() (string, bool) {
	p, err := exec.LookPath("maind")
	if err != nil {
		return "", false
	}
	return p, true
}

// MaindReady reports whether maind is installed AND unlocked.
func MaindReady() bool {
	p, ok := MaindPath()
	if !ok {
		return false
	}
	out, err := exec.Command(p, "status").Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) == "ready"
}

// MaindLocked reports whether maind is installed but currently locked — the case
// where stored keys exist but can't be read until the user unlocks.
func MaindLocked() bool {
	_, ok := MaindPath()
	return ok && !MaindReady()
}

// Unlock launches `maind unlock` with the terminal attached so the user enters
// their passphrase into maind's own no-echo prompt — PRISMAG never sees or
// handles the secret. On success it re-hydrates keys into the process env.
func Unlock() error {
	p, ok := MaindPath()
	if !ok {
		return fmt.Errorf("maind not found on PATH")
	}
	cmd := exec.Command(p, "unlock")
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return err
	}
	if !MaindReady() {
		return fmt.Errorf("maind is still locked")
	}
	Hydrate()
	return nil
}

// Hydrate loads any stored keys into the process env without overriding values
// already present. Best-effort: a failure for one provider never blocks others.
func Hydrate() {
	maindReady := MaindReady()
	dotenv := readDotenv()
	for _, provider := range ProviderOrder {
		env := Providers[provider]
		if strings.TrimSpace(os.Getenv(env)) != "" {
			continue // explicit env wins
		}
		if maindReady {
			if key, err := maindLoad(provider); err == nil && key != "" {
				os.Setenv(env, key)
				continue
			}
		}
		if v, ok := dotenv[env]; ok && v != "" {
			os.Setenv(env, v)
		}
	}
}

// Source reports where a provider's key currently resolves from.
func Source(provider string) Backend {
	env := Providers[provider]
	if strings.TrimSpace(os.Getenv(env)) != "" {
		return BackendEnv
	}
	if MaindReady() {
		if key, err := maindLoad(provider); err == nil && key != "" {
			return BackendMaind
		}
	}
	if v, ok := readDotenv()[env]; ok && v != "" {
		return BackendDotenv
	}
	return BackendNone
}

// Store saves a provider key following the policy: maind if available (C-only),
// otherwise ~/.config/prismag/.env. Returns where it was stored.
func Store(provider, key string) (Backend, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return BackendNone, fmt.Errorf("empty key")
	}
	if _, ok := Providers[provider]; !ok {
		return BackendNone, fmt.Errorf("unknown provider %q", provider)
	}
	if _, ok := MaindPath(); ok {
		if !MaindReady() {
			return BackendNone, fmt.Errorf("maind is installed but locked — run 'maind' to unlock, then retry")
		}
		if err := maindStore(provider, key); err != nil {
			return BackendNone, err
		}
		return BackendMaind, nil
	}
	if err := writeDotenvKey(Providers[provider], key); err != nil {
		return BackendNone, err
	}
	return BackendDotenv, nil
}

const secretTitlePrefix = "PRISMAG_SECRET:"

func maindStore(provider, key string) error {
	p, _ := MaindPath()
	cmd := exec.Command(p, "remember", key,
		"--title", secretTitlePrefix+provider,
		"--kind", "context",
		"--tags", "prismag-secret,"+provider,
		"--importance", "9",
		"--source", "ide",
	)
	cmd.Stdout = nil
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("store key in maind: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

type maindEntry struct {
	Title     string    `json:"title"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
}

func maindLoad(provider string) (string, error) {
	p, ok := MaindPath()
	if !ok {
		return "", fmt.Errorf("maind not found")
	}
	out, err := exec.Command(p, "recall", secretTitlePrefix+provider, "--tag", provider, "--json").Output()
	if err != nil {
		return "", err
	}
	var entries []maindEntry
	if err := json.Unmarshal(out, &entries); err != nil {
		return "", err
	}
	// Most recent matching entry wins.
	sort.SliceStable(entries, func(i, j int) bool {
		return entries[i].CreatedAt.After(entries[j].CreatedAt)
	})
	for _, e := range entries {
		if e.Title == secretTitlePrefix+provider {
			return strings.TrimSpace(e.Body), nil
		}
	}
	return "", fmt.Errorf("no stored key for %s", provider)
}

// --- .env fallback ---

// DotenvPath returns ~/.config/prismag/.env.
func DotenvPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "prismag", ".env"), nil
}

func readDotenv() map[string]string {
	out := map[string]string{}
	path, err := DotenvPath()
	if err != nil {
		return out
	}
	f, err := os.Open(path)
	if err != nil {
		return out
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		out[strings.TrimSpace(k)] = strings.Trim(strings.TrimSpace(v), `"'`)
	}
	return out
}

func writeDotenvKey(envVar, key string) error {
	path, err := DotenvPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	existing := readDotenv()
	existing[envVar] = key

	keys := make([]string, 0, len(existing))
	for k := range existing {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var b strings.Builder
	b.WriteString("# PRISMAG provider keys — keep private, never commit.\n")
	for _, k := range keys {
		fmt.Fprintf(&b, "%s=%s\n", k, existing[k])
	}
	return os.WriteFile(path, []byte(b.String()), 0600)
}
