package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rufus-SD/prismag/internal/contextstore"
)

func TestParseTranscriptRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "2026-06-18-demo-120000.md")
	content := `# PRISMAG session demo-120000

- started: 2026-06-18T12:00:00+02:00
- cwd: /tmp/demo
- store: memory

---

## you · 12:00:01

@@opus: design a cache

## prismag

## @@opus → ` + "`claude`" + `

Use an LRU with a ring buffer.

---

## you · 12:01:00

@@composer: implement it

## prismag

done.

---

_session ended: 2026-06-18T12:02:00+02:00
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	meta, turns, err := parseTranscript(path)
	if err != nil {
		t.Fatal(err)
	}
	if meta.ID != "demo-120000" {
		t.Fatalf("id = %q", meta.ID)
	}
	if meta.Cwd != "/tmp/demo" {
		t.Fatalf("cwd = %q", meta.Cwd)
	}
	if len(turns) != 2 {
		t.Fatalf("turns = %d, want 2", len(turns))
	}
	if turns[0].input != "@@opus: design a cache" {
		t.Fatalf("turn0 input = %q", turns[0].input)
	}
	if !strings.Contains(turns[0].output, "LRU with a ring buffer") {
		t.Fatalf("turn0 output = %q", turns[0].output)
	}
	if turns[1].input != "@@composer: implement it" || turns[1].output != "done." {
		t.Fatalf("turn1 = %+v", turns[1])
	}
	if meta.FirstPrompt != "@@opus: design a cache" {
		t.Fatalf("firstPrompt = %q", meta.FirstPrompt)
	}
}

// TestOffsetStoreOrdering verifies the offset wrapper keeps blocks chronological
// across turns even though each Run resets its turn index to 0.
func TestOffsetStoreOrdering(t *testing.T) {
	inner := contextstore.NewMemoryStore()
	ost := &offsetStore{inner: inner}

	// Turn 1: one block at local turn 0.
	if err := ost.Write("s", "opus", 0, "FIRST"); err != nil {
		t.Fatal(err)
	}
	ost.base++ // advance after the turn

	// Turn 2: another block, again at local turn 0.
	if err := ost.Write("s", "composer", 0, "SECOND"); err != nil {
		t.Fatal(err)
	}

	got, err := ost.Recall("s", "", 4000)
	if err != nil {
		t.Fatal(err)
	}
	iFirst := strings.Index(got, "FIRST")
	iSecond := strings.Index(got, "SECOND")
	if iFirst < 0 || iSecond < 0 {
		t.Fatalf("missing blocks: %q", got)
	}
	if iFirst > iSecond {
		t.Fatalf("expected FIRST before SECOND, got:\n%s", got)
	}
}

func TestSeedAdvancesBaseAndSkipsErrors(t *testing.T) {
	st := &replState{
		store:   &offsetStore{inner: contextstore.NewMemoryStore()},
		session: "s",
	}
	turns := []turnRec{
		{input: "@@opus: a", output: "answer A"},
		{input: "@@x: b", output: "_(error: no @@alias is ready)_"}, // skipped
		{input: "@@composer: c", output: "answer C"},
	}
	n := st.seed(turns)
	if n != 2 {
		t.Fatalf("seeded = %d, want 2 (error turn skipped)", n)
	}
	if st.store.base != 2 {
		t.Fatalf("base = %d, want 2", st.store.base)
	}
	ctx, err := st.store.Recall("s", "", 4000)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ctx, "answer A") || !strings.Contains(ctx, "answer C") {
		t.Fatalf("recall missing seeded answers:\n%s", ctx)
	}
	if strings.Contains(ctx, "no @@alias is ready") {
		t.Fatalf("error turn should not be seeded:\n%s", ctx)
	}
}
