package availability

import (
	"testing"

	"github.com/rufus-SD/prismag/internal/registry"
)

func TestResolveAnthropicCLI(t *testing.T) {
	creds := Credentials{Anthropic: true}
	got := Resolve(registry.ProviderAnthropic, creds, ContextCLI)
	if got.Status != StatusReady {
		t.Fatalf("status = %q, want ready", got.Status)
	}

	got = Resolve(registry.ProviderAnthropic, Credentials{}, ContextCLI)
	if got.Status != StatusNeedsKey || got.Detail != "ANTHROPIC_API_KEY" {
		t.Fatalf("got = %+v", got)
	}
}

func TestResolveOpenAICLI(t *testing.T) {
	got := Resolve(registry.ProviderOpenAI, Credentials{OpenAI: true}, ContextCLI)
	if got.Status != StatusReady {
		t.Fatalf("status = %q", got.Status)
	}
	got = Resolve(registry.ProviderOpenAI, Credentials{}, ContextCLI)
	if got.Detail != "OPENAI_API_KEY" {
		t.Fatalf("detail = %q", got.Detail)
	}
}

func clearIDEEnv(t *testing.T) {
	for _, k := range []string{
		"TERM_PROGRAM", "CURSOR_AGENT", "CURSOR_CONVERSATION_ID",
		"CURSOR_TRACE_ID", "VSCODE_PID", "VSCODE_IPC_HOOK", "PRISMAG_CONTEXT",
	} {
		t.Setenv(k, "")
	}
}

func TestDetectContextFlagsWin(t *testing.T) {
	clearIDEEnv(t)
	t.Setenv("CURSOR_AGENT", "1") // would say IDE, but --cli wins
	if DetectContext(false, true) != ContextCLI {
		t.Error("--cli should force CLI")
	}
	if DetectContext(true, false) != ContextIDE {
		t.Error("--ide should force IDE")
	}
}

func TestDetectContextEnvOverride(t *testing.T) {
	clearIDEEnv(t)
	t.Setenv("PRISMAG_CONTEXT", "ide")
	if DetectContext(false, false) != ContextIDE {
		t.Error("PRISMAG_CONTEXT=ide should select IDE")
	}
	t.Setenv("PRISMAG_CONTEXT", "cli")
	t.Setenv("CURSOR_AGENT", "1")
	if DetectContext(false, false) != ContextCLI {
		t.Error("PRISMAG_CONTEXT=cli should override heuristic")
	}
}

func TestDetectContextHeuristicAndDefault(t *testing.T) {
	clearIDEEnv(t)
	if DetectContext(false, false) != ContextCLI {
		t.Error("clean env should default to CLI")
	}
	t.Setenv("CURSOR_AGENT", "abc")
	if DetectContext(false, false) != ContextIDE {
		t.Error("CURSOR_AGENT should be detected as IDE")
	}
}

func TestResolveIDEAllReady(t *testing.T) {
	for _, p := range []registry.Provider{
		registry.ProviderAnthropic,
		registry.ProviderOpenAI,
		registry.ProviderOpenRouter,
		registry.ProviderCursor,
	} {
		got := Resolve(p, Credentials{}, ContextIDE)
		if got.Status != StatusReady {
			t.Fatalf("provider %s IDE status = %q, want ready", p, got.Status)
		}
	}
}

func TestResolveCursorCLIvsIDE(t *testing.T) {
	cli := Resolve(registry.ProviderCursor, Credentials{}, ContextCLI)
	if cli.Status != StatusNeedsSDK {
		t.Fatalf("CLI status = %q, want needs-sdk", cli.Status)
	}

	withSDK := Resolve(registry.ProviderCursor, Credentials{CursorSDK: true}, ContextCLI)
	if withSDK.Status != StatusReady {
		t.Fatalf("CLI+SDK status = %q, want ready", withSDK.Status)
	}
}

func TestFromEnv(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "sk-test")
	t.Setenv("OPENAI_API_KEY", "")
	creds := FromEnv()
	if !creds.Anthropic || creds.OpenAI {
		t.Fatalf("creds = %+v", creds)
	}
}

func TestResolveAllBundled(t *testing.T) {
	reg, err := registry.LoadDefault()
	if err != nil {
		t.Skip(err)
	}

	// Anthropic only — opus ready, openai aliases need key, composer needs SDK.
	creds := Credentials{Anthropic: true}
	all := ResolveAll(reg, creds, ContextCLI)

	opus, ok := all["opus"]
	if !ok || opus.Status != StatusReady {
		t.Fatalf("opus = %+v", opus)
	}
	if all["fast"].Status != StatusNeedsKey {
		t.Fatalf("fast = %+v, want needs-key", all["fast"])
	}
	if all["composer"].Status != StatusNeedsSDK {
		t.Fatalf("composer = %+v, want needs-sdk", all["composer"])
	}
}

func TestFormat(t *testing.T) {
	if Format(Result{Status: StatusReady}) != "ready" {
		t.Fatal("ready format")
	}
	if Format(Result{Status: StatusNeedsKey, Detail: "OPENAI_API_KEY"}) != "needs OPENAI_API_KEY" {
		t.Fatal("needs-key format")
	}
}
