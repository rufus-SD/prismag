package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/rufus-SD/prismag/internal/registry"
	"github.com/spf13/cobra"
)

var initCmd = &cobra.Command{
	Use:   "init [tool]",
	Short: "Set up PRISMAG in the current project (auto-detects your AI tool)",
	Long: `Wire PRISMAG into this project so your AI tool auto-routes @@alias: blocks.

Auto-detects the tool from the project (.cursor/, CLAUDE.md, .windsurfrules, ...)
unless you pass one explicitly. Also ensures a global registry exists.

Examples:
  prismag init            # detect and wire
  prismag init cursor     # force a specific tool`,
	Args: cobra.MaximumNArgs(1),
	RunE: runInit,
}

func init() {
	rootCmd.AddCommand(initCmd)
}

func runInit(cmd *cobra.Command, args []string) error {
	// Make sure a global registry exists so routing has aliases to resolve.
	if path, created, err := registry.ScaffoldGlobal(); err == nil && created {
		fmt.Fprintf(os.Stderr, "  Wrote starter registry: %s\n", path)
	}

	var a ideAdapter
	if len(args) == 1 {
		found, ok := adapterByID(args[0])
		if !ok {
			return fmt.Errorf("unknown tool %q — try: %s", args[0], strings.Join(adapterIDs(), ", "))
		}
		a = found
	} else {
		a = detectAdapter()
		fmt.Fprintf(os.Stderr, "  Detected tool: %s\n", a.name)
	}

	if err := a.connect(); err != nil {
		return err
	}

	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "  Tip: have your IDE agent refresh the model cache with the models it can see:")
	fmt.Fprintln(os.Stderr, "    prismag models --set \"<comma-separated model ids>\"")
	return nil
}

// isIDEEnv mirrors availability's heuristic for a Cursor/VSCode terminal.
func isIDEEnv() bool {
	for _, k := range []string{"CURSOR_AGENT", "CURSOR_CONVERSATION_ID", "VSCODE_PID"} {
		if os.Getenv(k) != "" {
			return true
		}
	}
	return false
}

func dirExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}

func fileExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}
