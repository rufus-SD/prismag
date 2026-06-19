package cli

import (
	"fmt"
	"strings"

	"github.com/rufus-SD/prismag/internal/orchestrator"
	"github.com/rufus-SD/prismag/internal/workspace"
	"github.com/spf13/cobra"
)

var (
	flagRouteJSON     bool
	flagRouteParallel bool
	flagRouteDiff     bool
	flagRouteFiles    []string
)

var routeCmd = &cobra.Command{
	Use:   "route [prompt...]",
	Short: "Show the delegation plan for a @@tagged prompt (no execution)",
	Long: `Parse a @@tagged prompt and print which subagent + model each block routes
to, without calling any backend. Use this to see how PRISMAG will dispatch work
in the IDE, or to drive your own tooling with --json.

Examples:
  prismag route "@@opus: design auth" "@@composer: implement it"
  prismag route --json "@@opus4.8: review" | jq .`,
	Args: cobra.ArbitraryArgs,
	RunE: runRoute,
}

func init() {
	routeCmd.Flags().BoolVar(&flagRouteJSON, "json", false, "output the plan as JSON")
	routeCmd.Flags().BoolVar(&flagRouteParallel, "parallel", false, "mark blocks as independent (parallel)")
	routeCmd.Flags().BoolVar(&flagRouteDiff, "diff", false, "include the working-tree git diff as shared context")
	routeCmd.Flags().StringArrayVar(&flagRouteFiles, "file", nil, "file or glob to include as shared context (repeatable)")
	routeCmd.Flags().StringVar(&flagRegistry, "registry", "", "path to registry.yaml")
	rootCmd.AddCommand(routeCmd)
}

func runRoute(cmd *cobra.Command, args []string) error {
	input, err := readPrompt(args)
	if err != nil {
		return err
	}
	if strings.TrimSpace(input) == "" {
		return fmt.Errorf("empty prompt — pass @@tagged text as arguments or on stdin")
	}

	reg, err := loadRegistry()
	if err != nil {
		return err
	}

	shared, err := workspace.GatherContext(flagRouteDiff, flagRouteFiles, 0)
	if err != nil {
		return err
	}

	plan, err := orchestrator.BuildPlan(input, orchestrator.Options{
		Parallel:      flagRouteParallel,
		Registry:      reg,
		SharedContext: shared,
	})
	if err != nil {
		return err
	}

	if flagRouteJSON {
		out, jerr := plan.JSON()
		if jerr != nil {
			return jerr
		}
		fmt.Println(out)
		return nil
	}
	fmt.Println(plan.Markdown())
	return nil
}
