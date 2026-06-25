package cli

import (
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/rufus-SD/prismag/adapters"
)

// dispatchMode describes how a given IDE delegates a routed @@block.
type dispatchMode int

const (
	// dispatchSubagent: the IDE can spawn per-task subagents and pick a model
	// per subagent. prismag hands it a delegation plan and it dispatches.
	dispatchSubagent dispatchMode = iota
	// dispatchInline: the IDE has no per-block model primitive, so the agent
	// runs `prismag run --api` itself and shows the sectioned output verbatim.
	dispatchInline
)

// ideAdapter is a single, self-contained IDE integration: where its rule file
// lives, how it dispatches routed blocks, and any embedded subagent templates.
//
// Adding a new IDE is just one entry in the adapters() table below.
type ideAdapter struct {
	id   string // "claude"
	name string // "Claude Code"

	rulePath string // rule file path, relative to the project root
	// shared is true when rulePath is a file the user also edits (CLAUDE.md,
	// CONVENTIONS.md, ...). prismag content is then fenced by markers and
	// upserted in place. When false, rulePath is a dedicated file we own.
	shared      bool
	frontmatter string // optional YAML front-matter prepended to dedicated files

	dispatch dispatchMode

	// Subagent scaffolding (only for dispatchSubagent adapters).
	agentsFS   fs.FS  // embedded templates, nil if none
	agentsSrc  string // source dir inside agentsFS
	agentsDest string // destination dir relative to project root
	// nativeVendor names the model family this IDE's subagents can run
	// (e.g. "Claude"). Blocks routing to other vendors fall back to --api.
	// Empty means the IDE's subagents can run any routed model (e.g. Cursor).
	nativeVendor string

	detect func() bool // project/env heuristic for auto-detection
	notes  []string    // extra lines printed after connecting
}

// ideAdapterTable is the source of truth for every supported IDE, ordered by
// auto-detection priority.
func ideAdapterTable() []ideAdapter {
	return []ideAdapter{
		{
			id:           "cursor",
			name:         "Cursor",
			rulePath:     filepath.Join(".cursor", "rules", "prismag-routing.mdc"),
			frontmatter:  cursorFrontmatter,
			dispatch:     dispatchSubagent,
			agentsFS:     adapters.CursorAgents,
			agentsSrc:    adapters.CursorAgentsDir,
			agentsDest:   filepath.Join(".cursor", "agents"),
			nativeVendor: "", // Cursor's picker exposes every routed model
			detect:       func() bool { return dirExists(".cursor") },
			notes: []string{
				"Allowlist 'prismag' once when prompted, then routing is seamless.",
			},
		},
		{
			id:           "claude",
			name:         "Claude Code",
			rulePath:     "CLAUDE.md",
			shared:       true,
			dispatch:     dispatchSubagent,
			agentsFS:     adapters.ClaudeAgents,
			agentsSrc:    adapters.ClaudeAgentsDir,
			agentsDest:   filepath.Join(".claude", "agents"),
			nativeVendor: "Claude",
			detect:       func() bool { return fileExists("CLAUDE.md") || dirExists(".claude") },
			notes: []string{
				"Claude subagents run Claude models; non-Claude blocks (composer/codex/gpt) auto-fall back to 'prismag run --api'.",
			},
		},
		{
			id:       "windsurf",
			name:     "Windsurf",
			rulePath: filepath.Join(".windsurf", "rules", "prismag-routing.md"),
			dispatch: dispatchInline,
			detect:   func() bool { return dirExists(".windsurf") || fileExists(".windsurfrules") },
		},
		{
			id:       "copilot",
			name:     "GitHub Copilot",
			rulePath: filepath.Join(".github", "copilot-instructions.md"),
			shared:   true,
			dispatch: dispatchInline,
			detect: func() bool {
				return fileExists(filepath.Join(".github", "copilot-instructions.md"))
			},
		},
		{
			id:       "cline",
			name:     "Cline",
			rulePath: filepath.Join(".clinerules", "prismag-routing.md"),
			dispatch: dispatchInline,
			detect:   func() bool { return dirExists(".clinerules") || fileExists(".clinerules") },
		},
		{
			id:       "roo",
			name:     "Roo Code",
			rulePath: filepath.Join(".roo", "rules", "prismag-routing.md"),
			dispatch: dispatchInline,
			detect:   func() bool { return dirExists(".roo") || fileExists(".roomodes") },
		},
		{
			id:       "aider",
			name:     "Aider",
			rulePath: "CONVENTIONS.md",
			shared:   true,
			dispatch: dispatchInline,
			detect:   func() bool { return fileExists("CONVENTIONS.md") },
		},
		{
			id:       "generic",
			name:     "your AI tool",
			rulePath: filepath.Join(".prismag", "rules.md"),
			dispatch: dispatchInline,
			detect:   func() bool { return false },
		},
	}
}

func adapterByID(id string) (ideAdapter, bool) {
	for _, a := range ideAdapterTable() {
		if a.id == id {
			return a, true
		}
	}
	return ideAdapter{}, false
}

func adapterIDs() []string {
	all := ideAdapterTable()
	ids := make([]string, len(all))
	for i, a := range all {
		ids[i] = a.id
	}
	return ids
}

// detectAdapter returns the best-guess IDE for the current project, falling
// back to "generic".
func detectAdapter() ideAdapter {
	for _, a := range ideAdapterTable() {
		if a.id == "generic" {
			continue
		}
		if a.detect != nil && a.detect() {
			return a
		}
	}
	if isIDEEnv() {
		if a, ok := adapterByID("cursor"); ok {
			return a
		}
	}
	g, _ := adapterByID("generic")
	return g
}

// connect wires this adapter into the project: writes/updates the rule file and
// installs any subagent templates.
func (a ideAdapter) connect() error {
	body := a.ruleBody()

	if a.shared {
		if err := upsertManagedBlock(a.rulePath, body); err != nil {
			return err
		}
	} else {
		content := body
		if a.frontmatter != "" {
			content = a.frontmatter + body
		}
		if err := writeRuleFile(a.rulePath, content); err != nil {
			return err
		}
	}

	copied := 0
	if a.dispatch == dispatchSubagent && a.agentsFS != nil {
		if err := os.MkdirAll(a.agentsDest, 0755); err != nil {
			return fmt.Errorf("create agents directory: %w", err)
		}
		n, err := installEmbeddedAgents(a.agentsFS, a.agentsSrc, a.agentsDest)
		if err != nil {
			return err
		}
		copied = n
	}

	a.printConnected(copied)
	return nil
}

// ruleBody renders the integration rule, tailored to the adapter's dispatch mode.
func (a ideAdapter) ruleBody() string {
	var b strings.Builder
	b.WriteString(ruleHeader)
	b.WriteString(ruleTrigger)
	switch a.dispatch {
	case dispatchSubagent:
		b.WriteString(a.subagentSection())
	default:
		b.WriteString(inlineSection)
	}
	b.WriteString(ruleAvailability)
	b.WriteString(ruleDoNot)
	return b.String()
}

func (a ideAdapter) subagentSection() string {
	var b strings.Builder
	fmt.Fprintf(&b, "## How to route (delegation — default in %s)\n\n", a.name)
	b.WriteString("1. Extract the full tagged prompt (shared preamble + all @@ blocks).\n")
	b.WriteString("2. Ask prismag for the deterministic delegation plan:\n\n")
	b.WriteString("```bash\nprismag route \"$(cat <<'PRISMAG'\nshared context here\n@@opus: first block\n@@composer: second block\nPRISMAG\n)\"\n```\n\n")
	b.WriteString("3. The plan lists, per block: the **subagent**, the **model**, and the **task**.\n")
	fmt.Fprintf(&b, "   Dispatch each block to its named subagent (templates in `%s/`) with that model.\n", a.agentsDest)
	b.WriteString("   - **Independent blocks** (\"parallel\" in the plan): launch concurrently.\n")
	b.WriteString("   - **Serial chain** (default): run in order, feeding each output to the next.\n")
	b.WriteString("   - Always include the plan's \"Shared context\" with every block.\n")
	if a.nativeVendor != "" {
		fmt.Fprintf(&b, "4. If a block's model is **not a %s model**, this tool can't run it as a subagent —\n", a.nativeVendor)
		b.WriteString("   execute that block with `prismag run --api \"<block>\"` and show the output verbatim.\n")
		b.WriteString("5. If a block shows `subagent: (none configured)`, add an `agent:` mapping in\n")
		b.WriteString("   registry.yaml, or run it with `--api` + the provider key.\n")
		b.WriteString("6. Present results as sectioned markdown, one section per `@@alias → model`.\n\n")
	} else {
		b.WriteString("4. If a block shows `subagent: (none configured)`, add an `agent:` mapping in\n")
		b.WriteString("   registry.yaml, or run it with `--api` + the provider key.\n")
		b.WriteString("5. Present results as sectioned markdown, one section per `@@alias → model`.\n\n")
	}
	b.WriteString("**Headless fallback.** In a plain terminal (or with `--api`), prismag executes the\n")
	b.WriteString("blocks itself via provider APIs — run `prismag run \"@@...\"` and show output verbatim.\n\n")
	return b.String()
}

func installEmbeddedAgents(fsys fs.FS, srcDir, destDir string) (int, error) {
	entries, err := fs.ReadDir(fsys, srcDir)
	if err != nil {
		return 0, err
	}
	count := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		// embed.FS / io/fs paths are always forward-slash, regardless of OS, so
		// use path.Join here (not filepath.Join, which breaks on Windows).
		data, err := fs.ReadFile(fsys, path.Join(srcDir, e.Name()))
		if err != nil {
			return count, err
		}
		dest := filepath.Join(destDir, e.Name())
		if _, err := os.Stat(dest); err == nil {
			continue // don't overwrite user edits
		}
		if err := os.WriteFile(dest, data, 0644); err != nil {
			return count, err
		}
		count++
	}
	return count, nil
}

func writeRuleFile(path, content string) error {
	dir := filepath.Dir(path)
	if dir != "." {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("create directory %s: %w", dir, err)
		}
	}
	return os.WriteFile(path, []byte(content), 0644)
}

const (
	managedBegin = "<!-- PRISMAG:BEGIN (managed by `prismag connect` — edit registry.yaml, not here) -->"
	managedEnd   = "<!-- PRISMAG:END -->"
)

// upsertManagedBlock writes body into path between PRISMAG markers, replacing an
// existing managed block if present (idempotent) or appending one otherwise. It
// preserves all user content outside the markers.
func upsertManagedBlock(path, body string) error {
	dir := filepath.Dir(path)
	if dir != "." {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("create directory %s: %w", dir, err)
		}
	}

	existing := ""
	if data, err := os.ReadFile(path); err == nil {
		existing = string(data)
	}

	block := managedBegin + "\n" + body + managedEnd + "\n"

	start := strings.Index(existing, managedBegin)
	if start >= 0 {
		end := strings.Index(existing[start:], managedEnd)
		if end >= 0 {
			end = start + end + len(managedEnd)
			// swallow a trailing newline after the end marker, if any
			if end < len(existing) && existing[end] == '\n' {
				end++
			}
			out := existing[:start] + block + existing[end:]
			return os.WriteFile(path, []byte(out), 0644)
		}
	}

	out := existing
	if out != "" && !strings.HasSuffix(out, "\n") {
		out += "\n"
	}
	if out != "" {
		out += "\n"
	}
	out += block
	return os.WriteFile(path, []byte(out), 0644)
}

func (a ideAdapter) printConnected(agentsCopied int) {
	abs, _ := filepath.Abs(a.rulePath)
	fmt.Fprintln(os.Stderr)
	fmt.Fprintf(os.Stderr, "  PRISMAG connected to %s.\n\n", a.name)
	fmt.Fprintf(os.Stderr, "    Rule: %s\n", abs)
	if a.dispatch == dispatchSubagent && a.agentsFS != nil {
		agAbs, _ := filepath.Abs(a.agentsDest)
		fmt.Fprintf(os.Stderr, "    Subagents: %d template(s) in %s\n", agentsCopied, agAbs)
	}
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "  When the user sends a message with @@alias: tags, the AI will:")
	if a.dispatch == dispatchSubagent {
		fmt.Fprintln(os.Stderr, "    - Run 'prismag route' for the delegation plan, then dispatch")
		fmt.Fprintln(os.Stderr, "      each block to its subagent + model")
		if a.nativeVendor != "" {
			fmt.Fprintf(os.Stderr, "    - Fall back to 'prismag run --api' for non-%s models\n", a.nativeVendor)
		}
	} else {
		fmt.Fprintln(os.Stderr, "    - Run 'prismag run' (provider APIs) and show the output verbatim")
	}
	fmt.Fprintln(os.Stderr, "    - Use 'prismag list' to check alias availability")
	for _, n := range a.notes {
		fmt.Fprintf(os.Stderr, "\n  %s\n", n)
	}
	fmt.Fprintln(os.Stderr)
}

// ---- shared rule sections ------------------------------------------------

const cursorFrontmatter = `---
description: "PRISMAG — per-block model routing via @@alias tags"
globs: "**/*"
alwaysApply: true
---
`

const ruleHeader = `# PRISMAG — Per-block model routing

PRISMAG routes each **@@alias:** block in a prompt to the right model. The routing
decision is **deterministic** and owned by the prismag CLI + registry.yaml — never
improvise it.

`

const ruleTrigger = "## Trigger\n\n" +
	"When the user's message contains one or more lines like:\n\n" +
	"```\n@@opus: design the authentication flow\n@@composer: implement the middleware\n```\n\n" +
	"…you MUST route it through PRISMAG before answering yourself.\n\n" +
	"The trigger is **@@** (double at-sign), not bare @ — bare @ is the IDE mention menu.\n\n"

const inlineSection = "## How to route (this tool runs blocks via prismag)\n\n" +
	"This tool doesn't dispatch per-block subagents, so prismag executes each block\n" +
	"through provider APIs and returns sectioned output. Run it and show the output\n" +
	"**verbatim** — do not paraphrase or re-route through yourself:\n\n" +
	"```bash\nprismag run \"@@opus: ...\" \"@@composer: ...\"      # serial + chained (default)\nprismag run --parallel \"@@opus: a\" \"@@fast: b\"   # independent blocks\n```\n\n" +
	"For multi-line blocks, pass the tagged text via a heredoc:\n\n" +
	"```bash\nprismag run \"$(cat <<'PRISMAG'\nshared context\n@@opus: first block\n@@composer: second block\nPRISMAG\n)\"\n```\n\n" +
	"If a run reports \"not ready\", run `prismag list` and tell the user which API key\nthat alias needs (ANTHROPIC_API_KEY, OPENAI_API_KEY, OPENROUTER_API_KEY).\n\n"

const ruleAvailability = "## Availability & model discovery\n\n" +
	"- `prismag models` lists the models available right now.\n" +
	"- `prismag list` shows each alias with an **AVAIL** mark (✓/✗/?). Use it before routing.\n\n" +
	"Default aliases (override in registry.yaml): **@@opus**, **@@opus4.8**, **@@composer**,\n" +
	"**@@composer2.5**, **@@fast**, **@@codex**, **@@gpt5.5**.\n\n"

const ruleDoNot = "## Do NOT\n\n" +
	"- Answer @@tagged blocks yourself without routing through prismag\n" +
	"- Invent the alias → model/subagent mapping — prismag + registry.yaml is the source of truth\n" +
	"- Strip or rewrite a run's sectioned output\n"
