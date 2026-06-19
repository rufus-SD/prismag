package contextstore

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// MaindEntry is the subset of `maind recall --json` output we use.
type MaindEntry struct {
	Kind  string   `json:"kind"`
	Title string   `json:"title"`
	Body  string   `json:"body"`
	Tags  []string `json:"tags"`
}

// MaindRecaller fetches persistent memories for a query under a token budget.
type MaindRecaller func(query string, budget int) ([]MaindEntry, error)

// MaindStore augments in-run chaining (in-memory) with persistent memory from
// maind. Within-run block outputs chain exactly like MemoryStore; on a non-empty
// query it also pulls relevant long-term memories from `maind recall --json`.
type MaindStore struct {
	mem    *MemoryStore
	recall MaindRecaller
}

// NewMaindStore returns a store backed by the maind binary on PATH.
func NewMaindStore() *MaindStore {
	return &MaindStore{mem: NewMemoryStore(), recall: execMaindRecall}
}

// NewMaindStoreWith injects a custom recaller (for tests).
func NewMaindStoreWith(r MaindRecaller) *MaindStore {
	return &MaindStore{mem: NewMemoryStore(), recall: r}
}

// MaindAvailable reports whether the maind binary is on PATH and unlocked.
func MaindAvailable() bool {
	path, err := exec.LookPath("maind")
	if err != nil {
		return false
	}
	out, err := exec.Command(path, "status").Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) == "ready"
}

// Write persists a block output for in-run chaining.
func (m *MaindStore) Write(sessionID, alias string, turn int, output string) error {
	return m.mem.Write(sessionID, alias, turn, output)
}

// Recall returns in-run chained context plus, for a non-empty query, relevant
// persistent memories from maind — all capped at budget tokens.
func (m *MaindStore) Recall(sessionID, query string, budget int) (string, error) {
	chained, err := m.mem.Recall(sessionID, "", budget)
	if err != nil {
		return "", err
	}
	if query == "" || m.recall == nil {
		return chained, nil
	}

	entries, rerr := m.recall(query, budget/2)
	if rerr != nil || len(entries) == 0 {
		return chained, nil // best-effort: degrade to chaining only
	}

	var b strings.Builder
	used := 0
	b.WriteString("## Relevant memory (maind)\n")
	for _, e := range entries {
		line := formatMaindEntry(e)
		cost := EstimateTokens(line)
		if used+cost > budget/2 {
			break
		}
		b.WriteString("\n" + line + "\n")
		used += cost
	}

	memBlock := strings.TrimSpace(b.String())
	parts := []string{}
	if memBlock != "" && memBlock != "## Relevant memory (maind)" {
		parts = append(parts, memBlock)
	}
	if chained != "" {
		parts = append(parts, chained)
	}
	return strings.TrimSpace(strings.Join(parts, "\n\n")), nil
}

func formatMaindEntry(e MaindEntry) string {
	head := "- "
	if e.Kind != "" {
		head += "(" + e.Kind + ") "
	}
	if e.Title != "" {
		head += e.Title + ": "
	}
	body := strings.TrimSpace(e.Body)
	tags := ""
	if len(e.Tags) > 0 {
		tags = " [" + strings.Join(e.Tags, ", ") + "]"
	}
	return head + body + tags
}

func execMaindRecall(query string, budget int) ([]MaindEntry, error) {
	path, err := exec.LookPath("maind")
	if err != nil {
		return nil, err
	}
	args := []string{"recall", query, "--json"}
	if budget > 0 {
		args = append(args, "--budget", fmt.Sprintf("%d", budget))
	}
	out, err := exec.Command(path, args...).Output()
	if err != nil {
		return nil, err
	}
	var entries []MaindEntry
	if err := json.Unmarshal(out, &entries); err != nil {
		return nil, err
	}
	return entries, nil
}
