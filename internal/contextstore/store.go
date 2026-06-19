// Package contextstore holds inter-task context for chained @@ blocks.
package contextstore

// Store persists per-task output and returns a budgeted slice for the next prompt.
type Store interface {
	// Write persists a task's output, tagged by alias + turn so voices don't blur.
	Write(sessionID, alias string, turn int, output string) error
	// Recall returns the relevant slice for the next prompt, never exceeding budget tokens.
	Recall(sessionID, query string, budget int) (string, error)
}
