// Package orchestrator dispatches tagged blocks to models and aggregates results.
package orchestrator

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/rufus-SD/prismag/internal/availability"
	"github.com/rufus-SD/prismag/internal/backend"
	"github.com/rufus-SD/prismag/internal/contextstore"
	"github.com/rufus-SD/prismag/internal/parser"
	"github.com/rufus-SD/prismag/internal/registry"
)

const DefaultContextBudget = 2000

// BackendFactory resolves a provider to a live backend (real APIs or test mocks).
type BackendFactory func(provider registry.Provider) (backend.Backend, error)

// EnvBackendFactory uses provider env credentials.
func EnvBackendFactory() BackendFactory {
	return backend.New
}

// Options configure a run.
type Options struct {
	Parallel      bool
	SessionID     string
	ContextBudget int
	Registry      *registry.Registry
	Store         contextstore.Store
	Factory       BackendFactory
	Creds         availability.Credentials
	Context       availability.Context
	SharedContext string // workspace context (git diff, files) merged into preamble

	// Stream, if set, receives each text delta as a block streams. Only used
	// for serial runs where a backend supports streaming; falls back to a
	// blocking Complete otherwise.
	Stream func(rawAlias, delta string)
}

// TaskResult is one block's outcome.
type TaskResult struct {
	Alias     string
	RawAlias  string
	Model     string
	Index     int
	Output    string
	Err       error
	Skipped   bool   // true when the alias was not ready (credentials/SDK)
	SkipNote  string // human reason for the skip, e.g. "needs ANTHROPIC_API_KEY"
	InTokens  int    // prompt tokens reported by the provider (0 if unknown)
	OutTokens int    // completion tokens reported by the provider (0 if unknown)
}

// Result is the full run outcome.
type Result struct {
	Preamble string
	Tasks    []TaskResult
}

// Run parses and executes a tagged prompt.
func Run(ctx context.Context, input string, opts Options) (Result, error) {
	if opts.Registry == nil {
		return Result{}, fmt.Errorf("registry is required")
	}
	if opts.Store == nil {
		opts.Store = contextstore.NewMemoryStore()
	}
	if opts.Factory == nil {
		opts.Factory = EnvBackendFactory()
	}
	if opts.ContextBudget <= 0 {
		opts.ContextBudget = DefaultContextBudget
	}
	if opts.SessionID == "" {
		opts.SessionID = "default"
	}
	if opts.Creds == (availability.Credentials{}) {
		opts.Creds = availability.FromEnv()
	}

	parsed := parser.Parse(input)
	if len(parsed.Tasks) == 0 {
		if parser.LooksTagged(input) {
			return Result{}, fmt.Errorf("a line starts with @@ but isn't a valid tag — use '@@alias: task' (alias = letters, digits, . _ -)")
		}
		return Result{}, fmt.Errorf("no @@alias: tags found in prompt")
	}

	// Merge workspace context (git diff, files) ahead of any inline preamble.
	if strings.TrimSpace(opts.SharedContext) != "" {
		if strings.TrimSpace(parsed.Preamble) != "" {
			parsed.Preamble = opts.SharedContext + "\n\n" + parsed.Preamble
		} else {
			parsed.Preamble = opts.SharedContext
		}
	}

	// Unknown aliases are a mistake (typo) — fail hard and report them all.
	if err := checkUnknown(parsed, opts.Registry); err != nil {
		return Result{}, err
	}

	// Unavailable aliases (missing key/SDK) are skipped, not fatal — a mixed
	// setup should still run the blocks it can.
	skip := make(map[int]string, len(parsed.Tasks))
	ready := 0
	for _, t := range parsed.Tasks {
		st, _ := availability.ResolveAlias(opts.Registry, t.Alias, opts.Creds, opts.Context)
		if st.Status != availability.StatusReady {
			skip[t.Index] = availability.Format(st)
		} else {
			ready++
		}
	}
	if ready == 0 {
		return Result{}, fmt.Errorf("no @@alias is ready — run 'prismag list' to see what each needs")
	}

	if opts.Parallel {
		return runParallel(ctx, parsed, opts, skip)
	}
	return runSerial(ctx, parsed, opts, skip)
}

func runSerial(ctx context.Context, parsed parser.ParsedPrompt, opts Options, skip map[int]string) (Result, error) {
	out := Result{Preamble: parsed.Preamble}
	for _, task := range parsed.Tasks {
		if note, skipped := skip[task.Index]; skipped {
			out.Tasks = append(out.Tasks, skippedResult(task, opts, note))
			continue
		}
		tr, err := executeTask(ctx, parsed.Preamble, task, opts, true)
		out.Tasks = append(out.Tasks, tr)
		if err != nil {
			return out, fmt.Errorf("block %d (@@%s): %w", task.Index, task.RawAlias, err)
		}
		if err := opts.Store.Write(opts.SessionID, task.Alias, task.Index, tr.Output); err != nil {
			return out, fmt.Errorf("store context for @@%s: %w", task.RawAlias, err)
		}
	}
	return out, nil
}

func runParallel(ctx context.Context, parsed parser.ParsedPrompt, opts Options, skip map[int]string) (Result, error) {
	out := Result{Preamble: parsed.Preamble}
	results := make([]TaskResult, len(parsed.Tasks))
	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error

	for _, task := range parsed.Tasks {
		if note, skipped := skip[task.Index]; skipped {
			results[task.Index] = skippedResult(task, opts, note)
			continue
		}
		wg.Add(1)
		go func(task parser.RoutedTask) {
			defer wg.Done()
			tr, err := executeTask(ctx, parsed.Preamble, task, opts, false)
			mu.Lock()
			results[task.Index] = tr
			if err != nil && firstErr == nil {
				firstErr = fmt.Errorf("block %d (@@%s): %w", task.Index, task.RawAlias, err)
			}
			mu.Unlock()
		}(task)
	}
	wg.Wait()

	out.Tasks = results
	return out, firstErr // partial results even on error
}

func skippedResult(task parser.RoutedTask, opts Options, note string) TaskResult {
	model := ""
	if a, ok := opts.Registry.Resolve(task.Alias); ok {
		model = a.Model
	}
	return TaskResult{
		Alias:    task.Alias,
		RawAlias: task.RawAlias,
		Model:    model,
		Index:    task.Index,
		Skipped:  true,
		SkipNote: note,
	}
}

func executeTask(ctx context.Context, preamble string, task parser.RoutedTask, opts Options, chained bool) (TaskResult, error) {
	alias, ok := opts.Registry.Resolve(task.Alias)
	if !ok {
		return TaskResult{}, fmt.Errorf("alias not found")
	}

	b, err := opts.Factory(alias.Provider)
	if err != nil {
		return TaskResult{Alias: task.Alias, RawAlias: task.RawAlias, Model: alias.Model, Index: task.Index}, err
	}

	system := buildSystem(preamble, opts, task, chained)
	breq := backend.Request{
		Model:  alias.Model,
		System: system,
		Prompt: task.Task,
	}
	var resp backend.Response
	if opts.Stream != nil {
		if sb, ok := b.(backend.Streamer); ok {
			resp, err = sb.Stream(ctx, breq, func(d string) { opts.Stream(task.RawAlias, d) })
		} else {
			resp, err = b.Complete(ctx, breq)
		}
	} else {
		resp, err = b.Complete(ctx, breq)
	}
	tr := TaskResult{
		Alias:    task.Alias,
		RawAlias: task.RawAlias,
		Model:    alias.Model,
		Index:    task.Index,
	}
	if err != nil {
		tr.Err = err
		return tr, err
	}
	tr.Output = resp.Text
	tr.InTokens = resp.InTokens
	tr.OutTokens = resp.OutTokens
	return tr, nil
}

func buildSystem(preamble string, opts Options, task parser.RoutedTask, chained bool) string {
	var parts []string
	if strings.TrimSpace(preamble) != "" {
		parts = append(parts, preamble)
	}
	if chained {
		// Serial chain: recall all prior blocks under budget (orchestrator policy).
		ctx, err := opts.Store.Recall(opts.SessionID, "", opts.ContextBudget)
		if err != nil {
			return strings.Join(parts, "\n\n")
		}
		if ctx != "" {
			parts = append(parts, "## Prior blocks\n\n"+ctx)
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n\n"))
}

// FormatMarkdown renders a sectioned report for CLI output.
func FormatMarkdown(r Result) string {
	var b strings.Builder
	if r.Preamble != "" {
		b.WriteString("## Shared context\n\n")
		b.WriteString(r.Preamble)
		b.WriteString("\n\n")
	}
	for _, t := range r.Tasks {
		b.WriteString(fmt.Sprintf("## @@%s → `%s`\n\n", t.RawAlias, t.Model))
		switch {
		case t.Skipped:
			b.WriteString(fmt.Sprintf("_Skipped — %s. Run `prismag list` for details._\n\n", t.SkipNote))
		case t.Err != nil:
			b.WriteString(fmt.Sprintf("_Error: %v_\n\n", t.Err))
		default:
			b.WriteString(strings.TrimSpace(t.Output))
			b.WriteString("\n\n")
		}
	}
	return strings.TrimSpace(b.String())
}
