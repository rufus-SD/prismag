package cli

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/rufus-SD/prismag/internal/availability"
	"github.com/rufus-SD/prismag/internal/discovery"
	"github.com/rufus-SD/prismag/internal/registry"
	"github.com/rufus-SD/prismag/internal/secrets"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

var setupCmd = &cobra.Command{
	Use:   "setup",
	Short: "First-time setup: keys, model discovery, and a starter registry",
	Long:  "Interactive onboarding. Detects your environment and maind, optionally stores provider API keys, discovers available models, and writes a starter registry.",
	Args:  cobra.NoArgs,
	RunE:  func(cmd *cobra.Command, args []string) error { return runSetup() },
}

func init() {
	rootCmd.AddCommand(setupCmd)
}

func runSetup() error {
	fmt.Print(Banner())
	fmt.Println()

	in := bufio.NewReader(os.Stdin)

	// [1/4] Environment
	fmt.Println("[1/4] Environment")
	ide := availability.DetectContext(false, false) == availability.ContextIDE
	if ide {
		fmt.Println("  ✓ IDE detected (Cursor/VSCode) — IDE routing works now, no keys needed")
	} else {
		fmt.Println("  • No IDE detected — standalone CLI mode")
	}
	maindReady := secrets.MaindReady()
	if _, ok := secrets.MaindPath(); ok {
		if maindReady {
			fmt.Println("  ✓ maind detected and unlocked — keys are stored encrypted; persistent memory enabled")
		} else {
			fmt.Println("  • maind detected but LOCKED — run 'maind' to unlock if you want encrypted key storage")
		}
	} else {
		fmt.Println("  • maind not found (optional) — keys fall back to ~/.config/prismag/.env; no cross-session memory")
	}
	for _, p := range secrets.ProviderOrder {
		if v := strings.TrimSpace(os.Getenv(secrets.EnvVar(p))); v != "" {
			fmt.Printf("  ✓ %s key present in env\n", p)
		}
	}
	fmt.Println()

	// [2/4] Keys
	fmt.Println("[2/4] Provider API keys (needed only for standalone CLI / CI; skip if IDE-only)")
	if askYesNo(in, "  Add or update provider keys now?", false) {
		if err := promptKeys(in); err != nil {
			return err
		}
	} else {
		fmt.Println("  Skipped. You can run 'prismag setup' again anytime.")
	}
	fmt.Println()

	// Load whatever is now stored into this process so discovery can use it.
	secrets.Hydrate()

	// [3/4] Discovery
	fmt.Println("[3/4] Discovering available models")
	creds := availability.FromEnv()
	api := discovery.Discover(availability.ContextCLI, creds)
	if api.Empty() {
		fmt.Println("  • API: no provider keys configured (that's fine for IDE-only use)")
	} else {
		for _, prov := range secrets.ProviderOrder {
			if ms, ok := api.ByProvider[prov]; ok {
				fmt.Printf("  ✓ %s: %d models\n", prov, len(ms))
			}
		}
	}
	idecache := discovery.Discover(availability.ContextIDE, creds)
	if idecache.Empty() {
		fmt.Println("  • IDE: model cache empty — your IDE agent will populate it on 'prismag connect'")
	} else {
		fmt.Printf("  ✓ IDE: %d models cached\n", len(idecache.All()))
	}
	fmt.Println()

	// [4/4] Registry
	fmt.Println("[4/4] Registry")
	path, created, err := registry.ScaffoldGlobal()
	if err != nil {
		return err
	}
	if created {
		fmt.Printf("  ✓ Wrote starter registry: %s\n", path)
	} else {
		fmt.Printf("  • Registry already exists: %s (left untouched)\n", path)
	}
	fmt.Println()

	fmt.Println("Done. Next:")
	fmt.Println("  prismag list                 # your @@aliases + availability")
	fmt.Println("  prismag connect cursor       # wire routing into this project's IDE")
	fmt.Println("  prismag run --cli \"@@fast: say hi\"   # standalone (needs a key)")
	return nil
}

func promptKeys(in *bufio.Reader) error {
	for _, p := range secrets.ProviderOrder {
		env := secrets.EnvVar(p)
		cur := secrets.Source(p)
		label := fmt.Sprintf("  %s (%s)", p, env)
		if cur != secrets.BackendNone {
			label += fmt.Sprintf(" [already set via %s]", cur)
		}
		fmt.Printf("%s — paste key or press Enter to skip:\n  > ", label)
		key, err := readSecret()
		if err != nil {
			return err
		}
		key = strings.TrimSpace(key)
		if key == "" {
			fmt.Println("  (skipped)")
			continue
		}
		where, err := secrets.Store(p, key)
		if err != nil {
			fmt.Printf("  ✗ %v\n", err)
			continue
		}
		fmt.Printf("  ✓ stored in %s\n", where)
	}
	return nil
}

// readSecret reads a line without echoing it when stdin is a terminal.
func readSecret() (string, error) {
	fd := int(os.Stdin.Fd())
	if term.IsTerminal(fd) {
		b, err := term.ReadPassword(fd)
		fmt.Println()
		return string(b), err
	}
	r := bufio.NewReader(os.Stdin)
	line, err := r.ReadString('\n')
	return strings.TrimRight(line, "\r\n"), err
}

func askYesNo(in *bufio.Reader, prompt string, def bool) bool {
	suffix := " [y/N] "
	if def {
		suffix = " [Y/n] "
	}
	fmt.Print(prompt + suffix)
	line, _ := in.ReadString('\n')
	line = strings.ToLower(strings.TrimSpace(line))
	if line == "" {
		return def
	}
	return line == "y" || line == "yes"
}
