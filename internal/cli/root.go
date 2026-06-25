package cli

import (
	"fmt"
	"os"
	"runtime/debug"

	"github.com/rufus-SD/prismag/internal/secrets"
	"github.com/spf13/cobra"
)

// Version is the build version, overridden at release time via -ldflags.
var Version = "dev"

// resolveVersion prefers the ldflags-stamped Version (release builds); for
// `go install` builds it falls back to Go's embedded VCS build info so the
// output pins a module version/commit instead of a bare "dev".
func resolveVersion() string {
	if Version != "" && Version != "dev" {
		return Version
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "dev"
	}
	var rev, dirty string
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			if s.Value == "true" {
				dirty = "+dirty"
			}
		}
	}
	if len(rev) > 12 {
		rev = rev[:12]
	}
	mod := info.Main.Version
	switch {
	case rev != "" && (mod == "" || mod == "(devel)"):
		return "dev (" + rev + dirty + ")"
	case rev != "":
		return mod + " (" + rev + dirty + ")"
	case mod != "" && mod != "(devel)":
		return mod
	default:
		return "dev"
	}
}

var rootCmd = &cobra.Command{
	Use:     "prismag",
	Version: resolveVersion(),
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
