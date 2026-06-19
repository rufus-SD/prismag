package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func connectInTemp(t *testing.T, id string) {
	t.Helper()
	dir := t.TempDir()
	oldWD, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(oldWD) })

	a, ok := adapterByID(id)
	if !ok {
		t.Fatalf("adapter %q not found", id)
	}
	if err := a.connect(); err != nil {
		t.Fatal(err)
	}
}

func TestConnectCursor(t *testing.T) {
	connectInTemp(t, "cursor")

	rule := filepath.Join(".cursor", "rules", "prismag-routing.mdc")
	data, err := os.ReadFile(rule)
	if err != nil {
		t.Fatal(err)
	}
	body := string(data)
	for _, want := range []string{"alwaysApply: true", "@@alias:", "prismag run", "prismag route", "subagent"} {
		if !strings.Contains(body, want) {
			t.Fatalf("rule missing %q", want)
		}
	}

	agent := filepath.Join(".cursor", "agents", "opus-planner.md")
	if _, err := os.Stat(agent); err != nil {
		t.Fatalf("opus-planner agent: %v", err)
	}
}

func TestConnectClaudeSubagents(t *testing.T) {
	connectInTemp(t, "claude")

	data, err := os.ReadFile("CLAUDE.md")
	if err != nil {
		t.Fatal(err)
	}
	body := string(data)
	for _, want := range []string{managedBegin, managedEnd, "prismag route", "not a Claude model", "prismag run --api"} {
		if !strings.Contains(body, want) {
			t.Fatalf("CLAUDE.md missing %q", want)
		}
	}

	for _, name := range []string{"opus-planner.md", "sonnet-implementer.md"} {
		if _, err := os.Stat(filepath.Join(".claude", "agents", name)); err != nil {
			t.Fatalf("claude agent %s: %v", name, err)
		}
	}
}

func TestConnectInlineTool(t *testing.T) {
	connectInTemp(t, "windsurf")

	rule := filepath.Join(".windsurf", "rules", "prismag-routing.md")
	data, err := os.ReadFile(rule)
	if err != nil {
		t.Fatal(err)
	}
	body := string(data)
	if strings.Contains(body, "delegation — default") {
		t.Fatal("inline rule should not describe subagent delegation")
	}
	for _, want := range []string{"runs blocks via prismag", "verbatim", "prismag run"} {
		if !strings.Contains(body, want) {
			t.Fatalf("windsurf rule missing %q", want)
		}
	}
}

func TestUpsertManagedBlockIdempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "CLAUDE.md")
	if err := os.WriteFile(path, []byte("# My project\n\nUser notes.\n"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := upsertManagedBlock(path, "BODY v1\n"); err != nil {
		t.Fatal(err)
	}
	if err := upsertManagedBlock(path, "BODY v2\n"); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(path)
	body := string(data)

	if !strings.Contains(body, "User notes.") {
		t.Fatal("user content was lost")
	}
	if strings.Contains(body, "BODY v1") {
		t.Fatal("stale managed block not replaced")
	}
	if strings.Count(body, managedBegin) != 1 {
		t.Fatalf("expected exactly one managed block, got %d", strings.Count(body, managedBegin))
	}
	if !strings.Contains(body, "BODY v2") {
		t.Fatal("new managed block missing")
	}
}
