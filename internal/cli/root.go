package cli

import (
	"fmt"
	"os"

	"github.com/rufus-SD/prismag/internal/secrets"
	"github.com/spf13/cobra"
)

// Version is the build version, overridden at release time via -ldflags.
var Version = "dev"

var rootCmd = &cobra.Command{
	Use:     "prismag",
	Version: Version,
	Short:   "Tag models in one prompt. Route each block to the right model.",
	Long: `PRISMAG routes each @@alias-tagged block of a prompt to the right model.

  prismag "@@opus: design the auth flow" "@@composer: implement the middleware"

Pairs with maind for shared, persistent memory across blocks and sessions.`,
	SilenceUsage:  true,
	SilenceErrors: true,
	// Load stored provider keys into the env so the rest of PRISMAG sees them.
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		secrets.Hydrate()
	},
}

// Execute runs the root command.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}
