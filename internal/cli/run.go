package cli

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/rufus-SD/prismag/internal/agent"
	"github.com/rufus-SD/prismag/internal/availability"
	"github.com/rufus-SD/prismag/internal/contextstore"
	"github.com/rufus-SD/prismag/internal/discovery"
	"github.com/rufus-SD/prismag/internal/orchestrator"
	"github.com/rufus-SD/prismag/internal/registry"
	"github.com/rufus-SD/prismag/internal/secrets"
	"github.com/rufus-SD/prismag/internal/workspace"
	"github.com/spf13/cobra"
)

var (
	flagParallel      bool
	flagSession       string
	flagContextBudget int
	flagStore         string
	flagDiff          bool
	flagFiles         []string
	flagRunIDE        bool
	flagRunCLI        bool
	flagRunAPI        bool
	flagExec          bool
	flagExecShell     bool
	flagExecYes       bool
)

var runCmd = &cobra.Command{
	Use:   "run [prompt...]",
	Short: "Route a @@tagged prompt to the right models",
	Long: `Execute each @@alias: block against its configured model.

Serial + chained by default — block N's output becomes context for block N+1.
Use --parallel for independent blocks (shared preamble only).

Examples:
  prismag run "@@opus: design auth" "@@fast: summarize"
  echo '@@opus: review' | prismag run
  prismag run --parallel '@@opus: plan' '@@fast: summarize'`,
	Args: cobra.ArbitraryArgs,
	RunE: runPrompt,
}

func init() {
	runCmd.Flags().BoolVar(&flagParallel, "parallel", false, "run independent blocks concurrently (no chaining)")
	runCmd.Flags().StringVar(&flagSession, "session", "", "session id for context chaining (default: git repo + branch)")
	runCmd.Flags().IntVar(&flagContextBudget, "budget", orchestrator.DefaultContextBudget, "max tokens of prior context to recall per block")
	runCmd.Flags().StringVar(&flagRegistry, "registry", "", "path to registry.yaml")
	runCmd.Flags().StringVar(&flagStore, "store", "auto", "context store: auto|memory|maind")
	runCmd.Flags().BoolVar(&flagDiff, "diff", false, "include the working-tree git diff as shared context")
	runCmd.Flags().StringArrayVar(&flagFiles, "file", nil, "file or glob to include as shared context (repeatable)")
	runCmd.Flags().BoolVar(&flagRunIDE, "ide", false, "force IDE context (emit a subagent delegation plan)")
	runCmd.Flags().BoolVar(&flagRunCLI, "cli", false, "force CLI context (execute via provider APIs)")
	runCmd.Flags().BoolVar(&flagRunAPI, "api", false, "in IDE context, execute via provider APIs instead of delegating")
	runCmd.Flags().BoolVar(&flagExec, "exec", false, "enable the permission-gated tool loop (CLI only): blocks can write files and take actions")
	runCmd.Flags().BoolVar(&flagExecShell, "exec-shell", false, "with --exec, also allow the run_shell tool")
	runCmd.Flags().BoolVarP(&flagExecYes, "yes", "y", false, "auto-approve every exec action (use with care)")

	rootCmd.AddCommand(runCmd)
	rootCmd.RunE = runPrompt
	rootCmd.Args = cobra.ArbitraryArgs
}

func runPrompt(cmd *cobra.Command, args []string) error {
	input, err := readPrompt(args)
	if err != nil {
		return err
	}
	if strings.TrimSpace(input) == "" {
		// First run (no global registry yet) → onboard.
		if path, gerr := registry.GlobalPath(); gerr == nil {
			if _, serr := os.Stat(path); serr != nil {
				return runSetup()
			}
		}
		// Bare, interactive `prismag` (configured) → drop into a live session.
		if cmd.Name() == rootCmd.Name() && isInteractive() {
			return startREPL("")
		}
		return cmd.Help()
	}

	reg, err := loadRegistry()
	if err != nil {
		return err
	}

	sessionID := flagSession
	if sessionID == "" {
		sessionID = workspace.SessionID()
	}

	shared, err := workspace.GatherContext(flagDiff, flagFiles, 0)
	if err != nil {
		return err
	}

	ctxKind := availability.DetectContext(flagRunIDE, flagRunCLI)
	creds := availability.FromEnv()
	execPolicy := buildExecPolicy(reg)

	// IDE context: delegate to subagents (the agent executes the plan), unless
	// --api or exec mode forces the standalone provider path. Exec means "let
	// prismag act on this machine", so it always runs the tool loop here even
	// from an IDE terminal (where there's no agent to pick up a plan).
	if ctxKind == availability.ContextIDE && !flagRunAPI && execPolicy == nil {
		plan, perr := orchestrator.BuildPlan(input, orchestrator.Options{
			Parallel:      flagParallel,
			Registry:      reg,
			SharedContext: shared,
			Models:        discovery.Cached(availability.ContextIDE, creds),
		})
		if perr != nil {
			return perr
		}
		fmt.Println(plan.Markdown())
		return nil
	}

	// CLI execution path: a locked maind means both the stored keys and the
	// persistent-memory store are unavailable, so offer the unlock bridge before
	// silently degrading to env keys + the in-memory store.
	if secrets.MaindLocked() {
		if maybeUnlockMaind() {
			creds = availability.FromEnv()
		}
	}

	store, storeName := selectStore(flagStore)
	if storeName == "maind" {
		fmt.Fprintln(os.Stderr, "  context store: maind (persistent memory)")
	}

	result, err := orchestrator.Run(context.Background(), input, orchestrator.Options{
		Parallel:      flagParallel,
		SessionID:     sessionID,
		ContextBudget: flagContextBudget,
		Registry:      reg,
		Store:         store,
		Creds:         creds,
		Context:       availability.ContextCLI,
		SharedContext: shared,
		Models:        discovery.Cached(availability.ContextCLI, creds),
		Exec:          execPolicy,
	})

	fmt.Println(orchestrator.FormatMarkdown(result))
	if err != nil {
		return enrichKeyError(err)
	}
	return nil
}

// maybeUnlockMaind offers to unlock a locked maind so its stored keys hydrate.
// Returns true when it unlocked successfully. Interactive only — a non-interactive
// shell just prints how to unlock and returns false.
func maybeUnlockMaind() bool {
	if !secrets.MaindLocked() {
		return false
	}
	if !isInteractive() {
		fmt.Fprintln(os.Stderr, "  maind is locked — your API keys are stored there. Run 'maind unlock' (or 'maind'), then retry.")
		return false
	}
	if !confirmYesNo("  maind is locked — unlock now to load your stored keys? [Y/n] ", true) {
		return false
	}
	if err := secrets.Unlock(); err != nil {
		fmt.Fprintln(os.Stderr, "  unlock failed:", err)
		return false
	}
	return true
}

// enrichKeyError clarifies the "nothing ready" error when the real cause is a
// locked maind holding the keys.
func enrichKeyError(err error) error {
	if err != nil && secrets.MaindLocked() && strings.Contains(err.Error(), "no @@alias is ready") {
		return fmt.Errorf("%w\n  (your API keys are stored in maind, which is locked — run 'maind unlock' or 'maind', then retry)", err)
	}
	return err
}

// confirmYesNo prompts on stderr and reads a y/n from stdin without buffering
// ahead (so a following `maind unlock` keeps full access to stdin for its
// passphrase).
func confirmYesNo(prompt string, def bool) bool {
	fmt.Fprint(os.Stderr, prompt)
	var ans string
	fmt.Fscanln(os.Stdin, &ans)
	switch strings.ToLower(strings.TrimSpace(ans)) {
	case "":
		return def
	case "y", "yes":
		return true
	default:
		return false
	}
}

// buildExecPolicy returns the tool-loop policy, merging the registry's exec:
// defaults with command-line flags (flags win). Returns nil — plain text
// completion — when exec is neither configured nor flagged on. Actions are
// approved interactively unless auto-approval is set.
func buildExecPolicy(reg *registry.Registry) *agent.Policy {
	cfg := reg.Exec()
	if !flagExec && !cfg.Enabled {
		return nil
	}
	return &agent.Policy{
		AllowShell: flagExecShell || cfg.Shell,
		Root:       expandHome(cfg.Root),
		MaxSteps:   cfg.MaxSteps,
		Approve:    cliApprover(flagExecYes || cfg.AutoApprove()),
		Emit:       func(s string) { fmt.Fprintln(os.Stderr, s) },
	}
}

// expandHome resolves a leading ~ so a config root confines actions correctly
// (the agent compares the resolved root against absolute paths).
func expandHome(p string) string {
	p = strings.TrimSpace(p)
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(p, "~"), "/"))
		}
	}
	return p
}

// cliApprover prompts on stderr for each side-effecting action. With autoYes it
// approves everything; when not attached to a terminal it denies (safe default).
func cliApprover(autoYes bool) func(agent.Action) (bool, string) {
	reader := bufio.NewReader(os.Stdin)
	return func(a agent.Action) (bool, string) {
		if autoYes {
			return true, ""
		}
		if !isInteractive() {
			return false, "non-interactive; re-run with --yes to allow actions"
		}
		fmt.Fprintf(os.Stderr, "  ⚠ allow %s ? [y/N] ", agent.Describe(a))
		line, _ := reader.ReadString('\n')
		ans := strings.ToLower(strings.TrimSpace(line))
		if ans == "y" || ans == "yes" {
			return true, ""
		}
		return false, "user declined"
	}
}

// selectStore picks the context store. "auto" uses maind when it's available and
// unlocked, otherwise the in-memory store.
func selectStore(mode string) (contextstore.Store, string) {
	switch strings.ToLower(mode) {
	case "maind":
		return contextstore.NewMaindStore(), "maind"
	case "memory":
		return contextstore.NewMemoryStore(), "memory"
	default: // auto
		if contextstore.MaindAvailable() {
			return contextstore.NewMaindStore(), "maind"
		}
		return contextstore.NewMemoryStore(), "memory"
	}
}

func readPrompt(args []string) (string, error) {
	if len(args) > 0 {
		return strings.Join(args, "\n"), nil
	}
	stat, _ := os.Stdin.Stat()
	if (stat.Mode() & os.ModeCharDevice) != 0 {
		return "", nil
	}
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func loadRegistry() (*registry.Registry, error) {
	if flagRegistry != "" {
		return registry.Load(flagRegistry)
	}
	return registry.LoadDefault()
}
