package contextstore

import (
	"strings"
	"testing"
)

func TestMemoryStoreWriteRecallChained(t *testing.T) {
	m := NewMemoryStore()
	sid := "sess-1"

	if err := m.Write(sid, "opus", 0, "Use JWT with refresh tokens."); err != nil {
		t.Fatal(err)
	}
	if err := m.Write(sid, "composer", 1, "Implement AuthMiddleware with JWT validation."); err != nil {
		t.Fatal(err)
	}

	got, err := m.Recall(sid, "JWT middleware", 500)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "@@opus") || !strings.Contains(got, "JWT") {
		t.Errorf("recall = %q", got)
	}
}

func TestMemoryStoreBudgetTruncation(t *testing.T) {
	m := NewMemoryStore()
	sid := "sess-1"

	_ = m.Write(sid, "opus", 0, stringsRepeat("alpha ", 50))
	_ = m.Write(sid, "fast", 1, stringsRepeat("beta ", 50))

	got, err := m.Recall(sid, "", 20)
	if err != nil {
		t.Fatal(err)
	}
	if got == "" {
		t.Fatal("expected at least one block under tiny budget")
	}
	if strings.Contains(got, "beta") {
		t.Errorf("expected budget to drop later block, got %q", got)
	}
}

func TestMemoryStoreQueryFilter(t *testing.T) {
	m := NewMemoryStore()
	sid := "sess-1"

	_ = m.Write(sid, "opus", 0, "Database uses PostgreSQL with pgx pool.")
	_ = m.Write(sid, "fast", 1, "Summarize the UI color palette.")

	got, err := m.Recall(sid, "PostgreSQL database", 500)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "PostgreSQL") {
		t.Errorf("recall = %q", got)
	}
	if strings.Contains(got, "color palette") {
		t.Errorf("unrelated block leaked into recall: %q", got)
	}
}

func TestMemoryStoreSessionIsolation(t *testing.T) {
	m := NewMemoryStore()
	_ = m.Write("a", "opus", 0, "session A secret")
	_ = m.Write("b", "opus", 0, "session B secret")

	got, _ := m.Recall("a", "secret", 500)
	if strings.Contains(got, "session B") {
		t.Errorf("cross-session leak: %q", got)
	}
}

func TestMemoryStoreEmpty(t *testing.T) {
	m := NewMemoryStore()
	got, err := m.Recall("missing", "anything", 100)
	if err != nil {
		t.Fatal(err)
	}
	if got != "" {
		t.Errorf("got = %q, want empty", got)
	}
}

func TestMemoryStoreValidation(t *testing.T) {
	m := NewMemoryStore()
	if err := m.Write("", "opus", 0, "x"); err == nil {
		t.Fatal("expected error for empty session")
	}
	if err := m.Write("s", "", 0, "x"); err == nil {
		t.Fatal("expected error for empty alias")
	}
	if _, err := m.Recall("s", "x", 0); err == nil {
		t.Fatal("expected error for zero budget")
	}
}

func TestEstimateTokens(t *testing.T) {
	if EstimateTokens("") != 0 {
		t.Fatal("empty string should be 0 tokens")
	}
	if EstimateTokens("abcd") != 1 {
		t.Fatalf("4 chars = 1 token, got %d", EstimateTokens("abcd"))
	}
}

func stringsRepeat(s string, n int) string {
	return strings.Repeat(s, n)
}
