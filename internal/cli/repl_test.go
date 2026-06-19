package cli

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rufus-SD/prismag/internal/availability"
	"github.com/rufus-SD/prismag/internal/contextstore"
	"github.com/rufus-SD/prismag/internal/registry"
)

func testReplRegistry(t *testing.T) *registry.Registry {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "registry.yaml")
	content := "aliases:\n  opus:\n    model: claude-test\n    provider: anthropic\n  fast:\n    model: gpt-test\n    provider: openai\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	r, err := registry.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// TestRunTurnUnknownAliasNotCancelled guards the bug where cleanup cancel()
// ran before the cancellation check, making every error read as "cancelled".
func TestRunTurnUnknownAliasNotCancelled(t *testing.T) {
	var buf bytes.Buffer
	s := &replState{
		reg:     testReplRegistry(t),
		creds:   availability.Credentials{},
		store:   &offsetStore{inner: contextstore.NewMemoryStore()},
		session: "s",
		tr:      &transcript{},
		out:     &buf,
		tty:     false,
	}
	s.runTurn("@@nope: hello")
	got := buf.String()
	if strings.Contains(got, "cancelled") {
		t.Fatalf("error wrongly reported as cancelled:\n%s", got)
	}
	if !strings.Contains(got, "unknown alias") {
		t.Fatalf("expected unknown-alias error, got:\n%s", got)
	}
}

// TestRunTurnNoReadyAlias verifies a known-but-unavailable alias is reported
// (not cancelled) when no credentials are present.
func TestRunTurnNoReadyAlias(t *testing.T) {
	var buf bytes.Buffer
	s := &replState{
		reg:     testReplRegistry(t),
		creds:   availability.Credentials{}, // no keys
		store:   &offsetStore{inner: contextstore.NewMemoryStore()},
		session: "s",
		tr:      &transcript{},
		out:     &buf,
		tty:     false,
	}
	s.runTurn("@@fast: hello")
	got := buf.String()
	if strings.Contains(got, "cancelled") {
		t.Fatalf("wrongly cancelled:\n%s", got)
	}
	if !strings.Contains(got, "no @@alias is ready") {
		t.Fatalf("expected no-ready-alias message, got:\n%s", got)
	}
}

func TestEditorSingleLine(t *testing.T) {
	e := newScannerEditor(strings.NewReader("@@opus: hello\n"), io.Discard)
	got, ok := e.next()
	if !ok {
		t.Fatal("expected ok")
	}
	if got != "@@opus: hello" {
		t.Fatalf("got %q", got)
	}
}

func TestEditorContinuation(t *testing.T) {
	e := newScannerEditor(strings.NewReader("@@opus: design \\\n@@composer: implement\n"), io.Discard)
	got, ok := e.next()
	if !ok {
		t.Fatal("expected ok")
	}
	want := "@@opus: design \n@@composer: implement"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestEditorEOF(t *testing.T) {
	e := newScannerEditor(strings.NewReader(""), io.Discard)
	if _, ok := e.next(); ok {
		t.Fatal("expected EOF (ok=false)")
	}
}

func TestTranscriptTurn(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s.md")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	tr := &transcript{path: path, f: f}
	if !tr.ok() {
		t.Fatal("transcript should be ok")
	}
	tr.turn("@@opus: hi", "hello back")
	tr.note("remembered → maind: x")
	tr.close()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	for _, want := range []string{"## you", "@@opus: hi", "## prismag", "hello back", "remembered → maind: x", "session ended"} {
		if !strings.Contains(s, want) {
			t.Fatalf("transcript missing %q:\n%s", want, s)
		}
	}
}

func TestTranscriptNilSafe(t *testing.T) {
	var tr *transcript
	if tr.ok() {
		t.Fatal("nil transcript should not be ok")
	}
	// Must not panic.
	tr.turn("a", "b")
	tr.note("n")
	tr.close()
}
