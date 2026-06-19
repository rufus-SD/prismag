package orchestrator

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/rufus-SD/prismag/internal/parser"
	"github.com/rufus-SD/prismag/internal/registry"
)

// PlanBlock is one block's delegation target for the IDE agent to dispatch.
type PlanBlock struct {
	Index    int    `json:"index"`
	Alias    string `json:"alias"`
	RawAlias string `json:"rawAlias"`
	Model    string `json:"model"`
	Provider string `json:"provider"`
	Agent    string `json:"agent,omitempty"` // subagent to run; empty = none configured
	Task     string `json:"task"`
	Note     string `json:"note,omitempty"` // e.g. "no IDE subagent configured"
}

// Plan is a deterministic delegation plan produced in IDE context. The agent
// executes each block via its subagent rather than PRISMAG calling any API.
type Plan struct {
	Context  string      `json:"context"`
	Parallel bool        `json:"parallel"`
	Preamble string      `json:"preamble,omitempty"`
	Blocks   []PlanBlock `json:"blocks"`
}

// BuildPlan parses input and resolves each block to a subagent + model. It never
// calls a backend — it only decides who should run what.
func BuildPlan(input string, opts Options) (Plan, error) {
	if opts.Registry == nil {
		return Plan{}, fmt.Errorf("registry is required")
	}
	parsed := parser.Parse(input)
	if len(parsed.Tasks) == 0 {
		if parser.LooksTagged(input) {
			return Plan{}, fmt.Errorf("a line starts with @@ but isn't a valid tag — use '@@alias: task' (alias = letters, digits, . _ -)")
		}
		return Plan{}, fmt.Errorf("no @@alias: tags found in prompt")
	}

	preamble := parsed.Preamble
	if strings.TrimSpace(opts.SharedContext) != "" {
		if strings.TrimSpace(preamble) != "" {
			preamble = opts.SharedContext + "\n\n" + preamble
		} else {
			preamble = opts.SharedContext
		}
	}

	if err := checkUnknown(parsed, opts.Registry); err != nil {
		return Plan{}, err
	}

	plan := Plan{Context: "ide", Parallel: opts.Parallel, Preamble: preamble}

	for _, t := range parsed.Tasks {
		a, _ := opts.Registry.Resolve(t.Alias)
		blk := PlanBlock{
			Index:    t.Index,
			Alias:    t.Alias,
			RawAlias: t.RawAlias,
			Model:    a.Model,
			Provider: string(a.Provider),
			Agent:    a.Agent,
			Task:     t.Task,
		}
		if a.Agent == "" {
			blk.Note = "no IDE subagent configured — set 'agent:' in registry.yaml, or run with --api for the CLI path"
		}
		plan.Blocks = append(plan.Blocks, blk)
	}
	return plan, nil
}

// checkUnknown returns an error listing any unregistered aliases, with a
// "did you mean?" hint drawn from the closest registered aliases.
func checkUnknown(parsed parser.ParsedPrompt, reg *registry.Registry) error {
	var unknown []string
	for _, t := range parsed.Tasks {
		if _, ok := reg.Resolve(t.Alias); !ok {
			unknown = append(unknown, t.RawAlias)
		}
	}
	if len(unknown) == 0 {
		return nil
	}
	tagged := make([]string, len(unknown))
	for i, u := range unknown {
		tagged[i] = "@@" + u
	}
	suggestions := reg.Suggest(unknown[0], 3)
	hint := ""
	if len(suggestions) > 0 {
		hinted := make([]string, len(suggestions))
		for i, s := range suggestions {
			hinted[i] = "@@" + s
		}
		hint = fmt.Sprintf(" — did you mean %s?", strings.Join(hinted, ", "))
	}
	return fmt.Errorf("unknown alias(es): %s%s (run 'prismag list' or 'prismag models')", strings.Join(tagged, ", "), hint)
}

// JSON renders the plan as indented JSON (for tooling).
func (p Plan) JSON() (string, error) {
	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// Markdown renders the plan as instructions for the IDE agent to execute.
func (p Plan) Markdown() string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("PRISMAG ROUTING PLAN (context: %s)\n", p.Context))
	if p.Parallel {
		b.WriteString("Dispatch each block below to its subagent. Blocks are independent — run them in parallel.\n\n")
	} else {
		b.WriteString("Dispatch each block below to its subagent, in order. Feed each block's output as context to the next (serial chain).\n\n")
	}
	for _, blk := range p.Blocks {
		b.WriteString(fmt.Sprintf("[block %d] @@%s\n", blk.Index, blk.RawAlias))
		if blk.Agent != "" {
			b.WriteString(fmt.Sprintf("  subagent: %s\n", blk.Agent))
		} else {
			b.WriteString("  subagent: (none configured)\n")
		}
		b.WriteString(fmt.Sprintf("  model:    %s\n", blk.Model))
		b.WriteString(fmt.Sprintf("  task:     %s\n", singleLine(blk.Task)))
		if blk.Note != "" {
			b.WriteString(fmt.Sprintf("  note:     %s\n", blk.Note))
		}
		b.WriteString("\n")
	}
	if strings.TrimSpace(p.Preamble) != "" {
		b.WriteString("Shared context for every block:\n")
		b.WriteString(p.Preamble)
		b.WriteString("\n")
	} else {
		b.WriteString("Shared context: none\n")
	}
	return strings.TrimSpace(b.String())
}

func singleLine(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.Join(strings.Fields(s), " ")
	return s
}
