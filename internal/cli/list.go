package cli

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/rufus-SD/prismag/internal/availability"
	"github.com/rufus-SD/prismag/internal/discovery"
	"github.com/rufus-SD/prismag/internal/registry"
	"github.com/spf13/cobra"
)

var (
	flagRegistry string
	flagIDE      bool
)

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List configured @@aliases and their models",
	RunE:  runList,
}

func init() {
	listCmd.Flags().StringVar(&flagRegistry, "registry", "", "path to registry.yaml (default: search PRISMAG_REGISTRY, ./registry.yaml, ~/.config/prismag/registry.yaml)")
	listCmd.Flags().BoolVar(&flagIDE, "ide", false, "force in-IDE view (subscription/subagents); otherwise context is auto-detected")
	rootCmd.AddCommand(listCmd)
}

func runList(cmd *cobra.Command, args []string) error {
	var (
		reg *registry.Registry
		err error
	)
	if flagRegistry != "" {
		reg, err = registry.Load(flagRegistry)
	} else {
		reg, err = registry.LoadDefault()
	}
	if err != nil {
		return err
	}

	creds := availability.FromEnv()
	// Auto-detect like run/route/models; --ide forces the in-IDE view.
	ctx := availability.DetectContext(flagIDE, false)
	statuses := availability.ResolveAll(reg, creds, ctx)

	// Best-effort discovery so we can flag aliases whose model isn't available.
	models := discovery.Discover(ctx, creds)

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ALIAS\tMODEL\tPROVIDER\tSTATUS\tAVAIL\tDESCRIPTION")
	for _, name := range reg.Names() {
		a, _ := reg.Resolve(name)
		st := availability.Format(statuses[name])
		fmt.Fprintf(w, "@@%s\t%s\t%s\t%s\t%s\t%s\n", name, a.Model, a.Provider, st, availMark(models, a.Model), a.Description)
	}
	if err := w.Flush(); err != nil {
		return err
	}
	if models.Empty() {
		if ctx == availability.ContextIDE {
			fmt.Fprintln(os.Stderr, "  AVAIL unknown — no IDE model cache. Run 'prismag models --set ...' to populate it.")
		} else {
			fmt.Fprintln(os.Stderr, "  AVAIL unknown — no API keys to query providers. See 'prismag models'.")
		}
	}
	return nil
}

// availMark reports whether an alias's model was found in the discovered set:
// ✓ available, ✗ not found, ? discovery unavailable.
func availMark(models discovery.Result, model string) string {
	if models.Empty() {
		return "?"
	}
	if models.Has(model) {
		return "✓"
	}
	return "✗"
}
