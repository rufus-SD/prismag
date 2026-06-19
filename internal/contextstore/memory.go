package contextstore

import (
	"fmt"
	"sort"
	"strings"
	"sync"
)

type record struct {
	alias  string
	turn   int
	output string
}

// MemoryStore keeps outputs for the current run in memory.
type MemoryStore struct {
	mu     sync.RWMutex
	bySess map[string][]record
}

// NewMemoryStore returns an empty in-memory context store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{bySess: make(map[string][]record)}
}

// Write persists a task's output for the session.
func (m *MemoryStore) Write(sessionID, alias string, turn int, output string) error {
	if sessionID == "" {
		return fmt.Errorf("sessionID is required")
	}
	if alias == "" {
		return fmt.Errorf("alias is required")
	}
	if turn < 0 {
		return fmt.Errorf("turn must be >= 0")
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.bySess[sessionID] = append(m.bySess[sessionID], record{
		alias:  strings.ToLower(alias),
		turn:   turn,
		output: output,
	})
	return nil
}

// Recall returns prior outputs for the session, filtered by query and capped at budget tokens.
func (m *MemoryStore) Recall(sessionID, query string, budget int) (string, error) {
	if sessionID == "" {
		return "", fmt.Errorf("sessionID is required")
	}
	if budget <= 0 {
		return "", fmt.Errorf("budget must be > 0")
	}

	m.mu.RLock()
	records := append([]record(nil), m.bySess[sessionID]...)
	m.mu.RUnlock()

	if len(records) == 0 {
		return "", nil
	}

	sort.SliceStable(records, func(i, j int) bool {
		if records[i].turn != records[j].turn {
			return records[i].turn < records[j].turn
		}
		return records[i].alias < records[j].alias
	})

	matched := filterRecords(records, query)
	if len(matched) == 0 {
		return "", nil
	}

	return formatUnderBudget(matched, budget), nil
}

func filterRecords(records []record, query string) []record {
	query = strings.TrimSpace(query)
	if query == "" {
		return records
	}

	terms := queryTerms(query)
	if len(terms) == 0 {
		return records
	}

	var out []record
	for _, r := range records {
		if recordMatches(r, terms) {
			out = append(out, r)
		}
	}
	return out
}

func queryTerms(query string) []string {
	var terms []string
	for _, w := range strings.Fields(strings.ToLower(query)) {
		if len(w) >= 2 {
			terms = append(terms, w)
		}
	}
	return terms
}

func recordMatches(r record, terms []string) bool {
	hay := strings.ToLower(r.output + " " + r.alias)
	for _, t := range terms {
		if strings.Contains(hay, t) {
			return true
		}
	}
	return false
}

func formatUnderBudget(records []record, budget int) string {
	var parts []string
	used := 0
	for _, r := range records {
		chunk := formatRecord(r)
		cost := EstimateTokens(chunk)
		if used+cost > budget && len(parts) > 0 {
			break
		}
		parts = append(parts, chunk)
		used += cost
	}
	return strings.TrimSpace(strings.Join(parts, "\n\n"))
}

func formatRecord(r record) string {
	return fmt.Sprintf("### @@%s (block %d)\n%s", r.alias, r.turn, strings.TrimSpace(r.output))
}
