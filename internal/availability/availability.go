// Package availability resolves which @@aliases are usable given credentials and context.
package availability

import (
	"os"
	"strings"

	"github.com/rufus-SD/prismag/internal/registry"
)

// Context is where PRISMAG runs — credentials and routing differ.
type Context int

const (
	// ContextCLI is standalone terminal use (API keys / SDK required).
	ContextCLI Context = iota
	// ContextIDE is inside an IDE agent (subscription models via subagents).
	ContextIDE
)

func (c Context) String() string {
	if c == ContextIDE {
		return "ide"
	}
	return "cli"
}

// DetectContext decides where PRISMAG is running. Explicit signals win:
// the --ide/--cli flags, then PRISMAG_CONTEXT=ide|cli. Otherwise it sniffs the
// environment for an IDE agent (Cursor/VSCode). Defaults to CLI.
func DetectContext(forceIDE, forceCLI bool) Context {
	switch {
	case forceCLI:
		return ContextCLI
	case forceIDE:
		return ContextIDE
	}
	switch strings.ToLower(strings.TrimSpace(os.Getenv("PRISMAG_CONTEXT"))) {
	case "ide":
		return ContextIDE
	case "cli":
		return ContextCLI
	}
	if detectIDEEnv() {
		return ContextIDE
	}
	return ContextCLI
}

// detectIDEEnv reports whether the process looks like it's running under an IDE
// agent (Cursor / VSCode integrated terminal).
func detectIDEEnv() bool {
	if strings.EqualFold(os.Getenv("TERM_PROGRAM"), "vscode") {
		return true
	}
	for _, k := range []string{
		"CURSOR_AGENT",
		"CURSOR_CONVERSATION_ID",
		"CURSOR_TRACE_ID",
		"VSCODE_PID",
		"VSCODE_IPC_HOOK",
	} {
		if strings.TrimSpace(os.Getenv(k)) != "" {
			return true
		}
	}
	return false
}

// Status reports whether an alias can be routed right now.
type Status string

const (
	StatusReady    Status = "ready"
	StatusNeedsKey Status = "needs-key"
	StatusNeedsSDK Status = "needs-sdk"
)

// Result is the availability of one alias.
type Result struct {
	Status Status
	Detail string // e.g. "ANTHROPIC_API_KEY", "Cursor SDK"
}

// Credentials captures which backends the user has configured.
type Credentials struct {
	Anthropic  bool
	OpenAI     bool
	OpenRouter bool
	CursorSDK  bool
}

// FromEnv reads credential presence from the environment.
func FromEnv() Credentials {
	return Credentials{
		Anthropic:  hasEnv("ANTHROPIC_API_KEY"),
		OpenAI:     hasEnv("OPENAI_API_KEY"),
		OpenRouter: hasEnv("OPENROUTER_API_KEY"),
		// Cursor SDK path — optional hook for CLI access to Composer-class models.
		CursorSDK: hasEnv("CURSOR_API_KEY"),
	}
}

func hasEnv(key string) bool {
	return strings.TrimSpace(os.Getenv(key)) != ""
}

// EnvKey returns the env var that unlocks a provider, or "" if not key-based.
func EnvKey(p registry.Provider) string {
	switch p {
	case registry.ProviderAnthropic:
		return "ANTHROPIC_API_KEY"
	case registry.ProviderOpenAI:
		return "OPENAI_API_KEY"
	case registry.ProviderOpenRouter:
		return "OPENROUTER_API_KEY"
	default:
		return ""
	}
}

// Resolve returns availability for one provider in the given context.
func Resolve(p registry.Provider, creds Credentials, ctx Context) Result {
	if ctx == ContextIDE {
		// In the IDE, models come from the subscription via subagents — no API keys.
		return Result{Status: StatusReady}
	}

	switch p {
	case registry.ProviderAnthropic:
		if creds.Anthropic {
			return Result{Status: StatusReady}
		}
		return Result{Status: StatusNeedsKey, Detail: "ANTHROPIC_API_KEY"}
	case registry.ProviderOpenAI:
		if creds.OpenAI {
			return Result{Status: StatusReady}
		}
		return Result{Status: StatusNeedsKey, Detail: "OPENAI_API_KEY"}
	case registry.ProviderOpenRouter:
		if creds.OpenRouter {
			return Result{Status: StatusReady}
		}
		return Result{Status: StatusNeedsKey, Detail: "OPENROUTER_API_KEY"}
	case registry.ProviderCursor:
		if creds.CursorSDK {
			return Result{Status: StatusReady}
		}
		return Result{Status: StatusNeedsSDK, Detail: "Cursor SDK"}
	case registry.ProviderOllama, registry.ProviderVLLM:
		// Local OpenAI-compatible servers need no key. We can't statically
		// probe the endpoint, so treat as ready; a run errors clearly if the
		// server isn't up.
		return Result{Status: StatusReady}
	default:
		return Result{Status: StatusNeedsKey, Detail: "unknown provider"}
	}
}

// ResolveAlias looks up an alias in the registry and resolves its availability.
func ResolveAlias(reg *registry.Registry, alias string, creds Credentials, ctx Context) (Result, bool) {
	a, ok := reg.Resolve(alias)
	if !ok {
		return Result{}, false
	}
	return Resolve(a.Provider, creds, ctx), true
}

// ResolveAll returns availability for every alias in the registry.
func ResolveAll(reg *registry.Registry, creds Credentials, ctx Context) map[string]Result {
	out := make(map[string]Result, len(reg.Names()))
	for _, name := range reg.Names() {
		a, _ := reg.Resolve(name)
		out[name] = Resolve(a.Provider, creds, ctx)
	}
	return out
}

// Format renders a human-readable status for CLI output.
func Format(r Result) string {
	switch r.Status {
	case StatusReady:
		return "ready"
	case StatusNeedsKey:
		if r.Detail != "" {
			return "needs " + r.Detail
		}
		return "needs-key"
	case StatusNeedsSDK:
		if r.Detail != "" {
			return "needs " + r.Detail
		}
		return "needs-sdk"
	default:
		return string(r.Status)
	}
}
