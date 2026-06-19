package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

var connectCmd = &cobra.Command{
	Use:   "connect [tool]",
	Short: "Wire PRISMAG into your AI tool",
	Long: `Generate integration rules so your AI assistant routes @@alias: blocks
through the prismag CLI.

Supported tools:
  cursor    — .cursor/rules/prismag-routing.mdc + subagent templates
  claude    — CLAUDE.md + .claude/agents/ subagent templates
  windsurf  — .windsurf/rules/prismag-routing.md
  copilot   — .github/copilot-instructions.md
  cline     — .clinerules/prismag-routing.md
  roo       — .roo/rules/prismag-routing.md
  aider     — CONVENTIONS.md
  generic   — .prismag/rules.md

Examples:
  prismag connect cursor
  prismag connect claude`,
	Args:      cobra.ExactArgs(1),
	ValidArgs: adapterIDs(),
	RunE:      runConnect,
}

func init() {
	rootCmd.AddCommand(connectCmd)
}

func runConnect(cmd *cobra.Command, args []string) error {
	a, ok := adapterByID(args[0])
	if !ok {
		return fmt.Errorf("unknown tool %q — try: %s", args[0], strings.Join(adapterIDs(), ", "))
	}
	return a.connect()
}
