package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/rufus-SD/prismag/internal/availability"
	"github.com/rufus-SD/prismag/internal/discovery"
	"github.com/rufus-SD/prismag/internal/registry"
	"github.com/rufus-SD/prismag/internal/secrets"
	"github.com/spf13/cobra"
)

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Diagnose your PRISMAG setup",
	Long:  "Checks keys, maind, the registry, model availability, and project wiring — the 'why isn't it working' command.",
	Args:  cobra.NoArgs,
	RunE:  func(cmd *cobra.Command, args []string) error { return runDoctor() },
}

func init() {
	rootCmd.AddCommand(doctorCmd)
}

func runDoctor() error {
	ok, warn := "✓", "•"

	fmt.Println("PRISMAG doctor")
	fmt.Println()

	// Context
	ctx := availability.DetectContext(false, false)
	fmt.Printf("%s context: %s (auto-detected)\n", ok, ctx)

	// maind
	if p, found := secrets.MaindPath(); found {
		if secrets.MaindReady() {
			fmt.Printf("%s maind: installed and unlocked (%s)\n", ok, p)
		} else {
			fmt.Printf("%s maind: installed but LOCKED — run 'maind' to unlock (%s)\n", warn, p)
		}
	} else {
		fmt.Printf("%s maind: not installed (optional — no cross-session memory)\n", warn)
	}

	// Registry
	regPath, _ := registry.GlobalPath()
	var reg *registry.Registry
	if _, err := os.Stat(regPath); err == nil {
		r, lerr := registry.LoadDefault()
		if lerr != nil {
			fmt.Printf("✗ registry: %s is invalid: %v\n", regPath, lerr)
		} else {
			reg = r
			fmt.Printf("%s registry: %d aliases (%s)\n", ok, len(r.Names()), r.Path())
		}
	} else {
		fmt.Printf("%s registry: none yet — run 'prismag setup' or 'prismag init'\n", warn)
	}

	// Keys
	fmt.Println()
	fmt.Println("Provider keys:")
	for _, p := range secrets.ProviderOrder {
		src := secrets.Source(p)
		if src == secrets.BackendNone {
			fmt.Printf("  %s %-10s not configured\n", warn, p)
		} else {
			fmt.Printf("  %s %-10s via %s\n", ok, p, src)
		}
	}

	// Discovery + registry validation
	fmt.Println()
	fmt.Println("Model availability:")
	creds := availability.FromEnv()
	models := discovery.Discover(ctx, creds)
	if models.Empty() {
		if ctx == availability.ContextIDE {
			fmt.Printf("  %s IDE model cache empty — run 'prismag models --set ...'\n", warn)
		} else {
			fmt.Printf("  %s no models discovered — add a key or check connectivity\n", warn)
		}
	} else {
		fmt.Printf("  %s %d models available (source: %s)\n", ok, len(models.All()), models.Source)
		if reg != nil {
			var stale []string
			for _, name := range reg.Names() {
				a, _ := reg.Resolve(name)
				if !models.Has(a.Model) {
					stale = append(stale, fmt.Sprintf("@@%s→%s", name, a.Model))
				}
			}
			if len(stale) > 0 {
				fmt.Printf("  %s %d alias(es) point at unavailable models: %v\n", warn, len(stale), stale)
			} else {
				fmt.Printf("  %s every alias maps to an available model\n", ok)
			}
		}
	}

	// Project wiring
	fmt.Println()
	rule := filepath.Join(".cursor", "rules", "prismag-routing.mdc")
	if fileExists(rule) {
		fmt.Printf("%s project: PRISMAG rule installed (%s)\n", ok, rule)
	} else {
		fmt.Printf("%s project: no PRISMAG rule here — run 'prismag init' to wire this repo\n", warn)
	}

	return nil
}
