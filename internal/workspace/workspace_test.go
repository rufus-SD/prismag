package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSessionIDStableAndNonEmpty(t *testing.T) {
	a := SessionID()
	b := SessionID()
	if a == "" || !strings.HasPrefix(a, "ws-") {
		t.Fatalf("session id = %q", a)
	}
	if a != b {
		t.Errorf("session id not stable: %q vs %q", a, b)
	}
}

func TestGatherContextFiles(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "note.txt")
	if err := os.WriteFile(f, []byte("hello prismag"), 0644); err != nil {
		t.Fatal(err)
	}

	ctx, err := GatherContext(false, []string{f}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ctx, "hello prismag") || !strings.Contains(ctx, "## File:") {
		t.Fatalf("context = %q", ctx)
	}
}

func TestGatherContextTruncates(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "big.txt")
	if err := os.WriteFile(f, []byte(strings.Repeat("x", 1000)), 0644); err != nil {
		t.Fatal(err)
	}
	ctx, err := GatherContext(false, []string{f}, 100)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ctx, "truncated") {
		t.Errorf("expected truncation marker, got %q", ctx)
	}
}

func TestGatherContextEmpty(t *testing.T) {
	ctx, err := GatherContext(false, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if ctx != "" {
		t.Errorf("expected empty context, got %q", ctx)
	}
}
