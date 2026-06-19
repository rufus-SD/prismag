package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/rufus-SD/prismag/internal/availability"
	"github.com/rufus-SD/prismag/internal/discovery"
	"github.com/spf13/cobra"
)

var (
	flagModelsIDE  bool
	flagModelsCLI  bool
	flagModelsJSON bool
	flagModelsSet  string
)

var modelsCmd = &cobra.Command{
	Use:   "models",
	Short: "List the models actually available right now",
	Long: `Discover available models for the current context.

  CLI context : live-queries each provider's /models endpoint with your keys.
  IDE context : reads the agent-maintained cache (~/.config/prismag/ide-models.yaml).

Refresh the IDE cache (run by the IDE agent with the current model picker):
  prismag models --set "claude-opus-4-8-thinking-high,composer-2.5-fast,gpt-5.5-medium"`,
	Args: cobra.NoArgs,
	RunE: runModels,
}

func init() {
	modelsCmd.Flags().BoolVar(&flagModelsIDE, "ide", false, "force IDE context (read the model cache)")
	modelsCmd.Flags().BoolVar(&flagModelsCLI, "cli", false, "force CLI context (query provider APIs)")
	modelsCmd.Flags().BoolVar(&flagModelsJSON, "json", false, "output as JSON")
	modelsCmd.Flags().StringVar(&flagModelsSet, "set", "", "write the IDE model cache from a comma-separated list of model ids")
	rootCmd.AddCommand(modelsCmd)
}

func runModels(cmd *cobra.Command, args []string) error {
	if flagModelsSet != "" {
		path, err := discovery.SetIDEModels([]string{flagModelsSet})
		if err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "  IDE model cache updated: %s\n", path)
		return nil
	}

	ctx := availability.DetectContext(flagModelsIDE, flagModelsCLI)
	res := discovery.Discover(ctx, availability.FromEnv())

	if flagModelsJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(res)
	}

	if res.Empty() {
		if ctx == availability.ContextIDE {
			fmt.Printf("No IDE models cached yet (context: ide).\n")
			fmt.Printf("Have the IDE agent run:\n  prismag models --set \"<comma-separated model ids>\"\n")
		} else {
			fmt.Printf("No models discovered (context: cli).\n")
			fmt.Printf("Set ANTHROPIC_API_KEY / OPENAI_API_KEY / OPENROUTER_API_KEY, then retry.\n")
		}
		for p, e := range res.Errors {
			fmt.Fprintf(os.Stderr, "  %s: %s\n", p, e)
		}
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintf(w, "# context: %s (source: %s)\n", res.Context, res.Source)
	if res.UpdatedAt != "" {
		fmt.Fprintf(w, "# cache updated: %s\n", res.UpdatedAt)
	}
	fmt.Fprintln(w, "PROVIDER\tMODEL")
	for _, provider := range sortedKeys(res.ByProvider) {
		for _, m := range res.ByProvider[provider] {
			fmt.Fprintf(w, "%s\t%s\n", provider, m)
		}
	}
	if err := w.Flush(); err != nil {
		return err
	}
	for p, e := range res.Errors {
		fmt.Fprintf(os.Stderr, "  %s: %s\n", p, e)
	}
	return nil
}

func sortedKeys(m map[string][]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	// small, stable order: providers are few
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if out[j] < out[i] {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}
