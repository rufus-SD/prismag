package contextstore

import (
	"fmt"
	"strings"
	"testing"
)

func TestMaindStoreChainingOnly(t *testing.T) {
	// Empty query → behaves like in-memory chaining, no maind call.
	called := false
	m := NewMaindStoreWith(func(q string, b int) ([]MaindEntry, error) {
		called = true
		return nil, nil
	})
	_ = m.Write("s", "opus", 0, "first block output")

	got, err := m.Recall("s", "", 500)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "first block output") {
		t.Fatalf("recall = %q", got)
	}
	if called {
		t.Error("maind should not be queried for empty (chaining) recall")
	}
}

func TestMaindStoreMergesPersistentMemory(t *testing.T) {
	m := NewMaindStoreWith(func(q string, b int) ([]MaindEntry, error) {
		return []MaindEntry{
			{Kind: "decision", Title: "Auth", Body: "Use JWT with refresh tokens", Tags: []string{"auth"}},
		}, nil
	})
	_ = m.Write("s", "opus", 0, "planning output")

	got, err := m.Recall("s", "auth approach", 1000)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "Relevant memory (maind)") {
		t.Fatalf("missing memory section: %q", got)
	}
	if !strings.Contains(got, "Use JWT with refresh tokens") {
		t.Fatalf("missing persistent memory: %q", got)
	}
	if !strings.Contains(got, "planning output") {
		t.Fatalf("missing chained context: %q", got)
	}
}

func TestMaindStoreDegradesOnError(t *testing.T) {
	m := NewMaindStoreWith(func(q string, b int) ([]MaindEntry, error) {
		return nil, fmt.Errorf("maind locked")
	})
	_ = m.Write("s", "opus", 0, "chained only")

	got, err := m.Recall("s", "query", 500)
	if err != nil {
		t.Fatalf("should degrade gracefully, got %v", err)
	}
	if !strings.Contains(got, "chained only") {
		t.Fatalf("recall = %q", got)
	}
	if strings.Contains(got, "Relevant memory") {
		t.Error("should not show memory section when maind failed")
	}
}
