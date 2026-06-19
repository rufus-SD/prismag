# PRISMAG

**Tag models in one prompt. Route each task to the right model.**

> One prompt enters; PRISMAG splits it into a spectrum, sending each tagged block
> to the right model. Pairs with [maind](https://github.com/rufus-SD/maind) for
> shared, persistent memory across blocks, sessions, and IDEs.

A portable orchestration layer that lets developers write:

```
@@opus: design the authentication flow
@@composer: implement the middleware
```

…and have each block executed by the appropriate model — without switching the IDE model picker or opening multiple chats.

---

## Problem

Today's AI coding tools force a binary choice:

1. Pick **one model** for the whole conversation, or
2. Open **multiple chats** and manually split work.

Neither matches how developers actually think. Planning needs depth (Opus). Implementation needs speed (Composer). Review needs a different lens entirely.

Existing workarounds are inadequate:

| Approach | Limitation |
|----------|------------|
| Model picker / toggle | One model per chat |
| Multiple chats | Manual context splitting, no unified result |
| Cursor subagents | Convention-based, parent model still orchestrates, routing bugs |
| LiteLLM / OpenRouter | Auto-routing by cost/latency — user doesn't choose per block |
| Agent frameworks (LangGraph, CrewAI) | Powerful but code/YAML config, not chat-native UX |

**No tool today ships chat-native, per-block `@model` routing in a single prompt.**
Existing options route by cost/latency (OpenRouter), delegate implicitly (subagents),
or need code/YAML pipelines (LangGraph). This project fills that gap — and pairs it
with shared, persistent memory so context survives across blocks, sessions, and surfaces.

---

## Vision

A small, IDE-agnostic routing core with thin adapters per editor.

```
User prompt with @tags
        ↓
    Parser (shared)
        ↓
  Orchestrator (shared) ←→ ContextStore (in-memory | maind)
        ↓
  Model backends (direct APIs, OpenRouter, Cursor SDK)
        ↓
  Aggregated result → back to IDE
```

The same `@alias: task` syntax works everywhere. Only the entry point changes.

Context and conversation history flow through a pluggable `ContextStore` — an
in-memory store by default, or [maind](https://github.com/rufus-SD/maind) for
persistent, encrypted, cross-session memory shared between the CLI and the IDE
agent.

---

## DSL

### Syntax

```
@@<alias>: <task description>
```

Multiple blocks in one message, executed in order (or parallel when independent):

```
@@opus: review the security implications of this auth module
@@composer: write the unit tests for AuthService.php
@@fast: summarize the diff in 3 bullet points
```

### Rules

- **Trigger is `@@`, not `@`.** A bare `@` collides with the IDE's native
  file/symbol mention menu (Cursor, VS Code). `@@` is a distinct, collision-free
  sigil that travels identically as plain text through every chat surface. (The
  CLI also accepts a bare `@` since there's no collision in a terminal.)
- `@@alias` is case-insensitive (`@@Opus` = `@@opus`)
- Alias maps to a model ID via `registry.yaml` (user-configurable)
- Text before the first `@@tag` is treated as context shared with all tasks (optional)
- **Serial + chained by default:** blocks run in order, and the output of block N
  becomes context for block N+1 (the `@@opus: plan → @@composer: build` case), passed
  through the `ContextStore` under a token budget — not dumped verbatim.
- **`--parallel`:** for *independent* blocks only. They run concurrently and each
  sees only the shared preamble (not each other's output). Chaining and parallelism
  are mutually exclusive for the same blocks.
- On a task error, a chained run **fails fast** (stops, reports completed blocks);
  a `--parallel` run tolerates partial failure.

### Default registry (example)

```yaml
aliases:
  opus:
    model: claude-4.6-opus-high-thinking
    description: Deep reasoning, architecture, security review
  composer:
    model: composer-2.5-fast
    description: Fast implementation, multi-file edits
  fast:
    model: gpt-5.3-codex
    description: Cheap, quick summaries and simple transforms
  codex:
    model: gpt-5.3-codex
    description: Code generation specialist
```

---

## Architecture

### Core (IDE-agnostic)

| Module | Responsibility |
|--------|----------------|
| `parser` | Split prompt into tagged tasks + shared context |
| `registry` | Load alias → model mappings from YAML |
| `orchestrator` | Dispatch tasks, handle parallel/serial, chunk/label/budget context, aggregate results |
| `backends` | Pluggable model providers (direct Anthropic / OpenAI APIs, OpenRouter, Cursor SDK) |
| `context-store` | `ContextStore` interface for inter-task context + conversation history (in-memory default, maind backend) |
| `formatter` | Merge sub-results into a single response |

#### `ContextStore` contract

```go
// ContextStore persists per-task output and returns a budgeted slice for the
// next prompt.
type ContextStore interface {
	// Write persists a task's output, tagged by alias + turn so voices don't blur.
	Write(sessionID, alias string, turn int, output string) error
	// Recall returns the relevant slice for the next prompt, never exceeding
	// budget tokens.
	Recall(sessionID, query string, budget int) (string, error)
}
```

- **`MemoryStore` (default):** keeps outputs for the current run; `Recall`
  filters/truncates under the budget. Zero dependencies. Solves the per-run
  token-cap problem and block→block chaining.
- **`MaindStore` (opt-in):** same interface backed by
  [maind](https://github.com/rufus-SD/maind) — encrypted SQLite + FTS5 recall —
  by shelling out to `maind recall --json --budget`. Persistent, cross-session,
  cross-surface memory. Selected via `--store maind` or auto-detected when the
  binary is present.

The orchestrator owns the *policy* (what to chunk, how to label by alias, how
much to recall); the store owns *persistence and retrieval*. This split keeps
routing logic testable without a live maind binary.

### Adapters (thin, per IDE)

**The primary cross-IDE mechanism is `@@` + rules, not a per-IDE extension.** The
`@@` trigger is just text, so any rules-aware agent can recognize it. `prismag
connect <tool>` writes a rule (exactly like `maind connect`) that teaches the agent:
when it sees `@@alias:`, **shell out to the `prismag` CLI** and show the result
verbatim. Routing happens in the deterministic CLI; the IDE only triggers it.

This sidesteps the fork problem: Cursor/Windsurf are VS Code forks but ship their
own **proprietary chat** and do *not* expose VS Code's Chat Participant API, so a
native extension can't plug into them. Rules + terminal + tool-calling are the APIs
that *do* exist everywhere.

Each IDE is one entry in the `ideAdapterTable()` in `internal/cli/ide_adapters.go`:
a rule file (in that IDE's native path/format), a **dispatch mode**, and optional
embedded subagent templates. Adding a new IDE is a single table row.

Two dispatch tiers, matched to what each IDE can actually do:

- **`subagent`** — the IDE can spawn per-task subagents and pick a model per
  subagent. prismag hands it a delegation plan (`prismag route`) and it dispatches
  each block to its named subagent + model. (Cursor: any routed model; Claude Code:
  Claude models, with auto-fallback to `--api` for non-Claude blocks.)
- **`inline`** — no per-block model primitive, so the agent runs `prismag run --api`
  itself and shows the sectioned output verbatim.

| Adapter | Rule file (native location) | Dispatch | Subagents |
|---------|-----------------------------|----------|-----------|
| **CLI / REPL** | — (`prismag "@@opus: ..."`) | guaranteed, in-process | n/a |
| **Cursor** | `.cursor/rules/prismag-routing.mdc` | `subagent` (any model) | `.cursor/agents/` |
| **Claude Code** | `CLAUDE.md` (managed block) | `subagent` (Claude) + `--api` fallback | `.claude/agents/` |
| **Windsurf** | `.windsurf/rules/prismag-routing.md` | `inline` | — |
| **GitHub Copilot** | `.github/copilot-instructions.md` (managed block) | `inline` | — |
| **Cline** | `.clinerules/prismag-routing.md` | `inline` | — |
| **Roo Code** | `.roo/rules/prismag-routing.md` | `inline` | — |
| **Aider** | `CONVENTIONS.md` (managed block) | `inline` | — |
| **generic** | `.prismag/rules.md` | `inline` | — |

Shared rule files (`CLAUDE.md`, `CONVENTIONS.md`, `copilot-instructions.md`) get a
fenced `<!-- PRISMAG:BEGIN/END -->` block so re-running `connect` is idempotent and
never clobbers the user's own content. `prismag init` auto-detects the right adapter
from project markers/env.

> The VS Code Chat Participant extension is **optional polish**, not the centerpiece
> — it gives a nicer native dropdown where the API exists, but the `@@`-via-rules
> path already covers VS Code (with Copilot) and every fork uniformly.

**"Works everywhere" means:** any agent that has (a) rules / `AGENTS.md`,
(b) terminal command execution. Memory is native everywhere through the maind tool.
Routing is always **deterministic inside the CLI**; what's **best-effort** is whether
the in-IDE agent reliably *invokes* the CLI when it sees `@@` (it depends on the
parent model obeying the rule — see Known limitations). Optionally, where the IDE
has subagents and the user has no API keys, the rule can instead delegate to
subagents using the IDE's own model access (see Execution contexts).

### Project layout (target)

```
prismag/
├── main.go
├── internal/
│   ├── parser/                 # @@alias: task → tasks + shared preamble
│   ├── registry/               # alias → model mappings (registry.yaml)
│   ├── orchestrator/           # dispatch, parallel/serial, chunk/label/budget, aggregate
│   ├── suggest/                # registry → ranked, status-annotated suggestions
│   ├── availability/           # per-alias status: ready | needs-key | needs-sdk
│   ├── contextstore/
│   │   ├── store.go            # ContextStore interface
│   │   ├── memory.go           # default, zero-dep in-memory store
│   │   └── maind.go            # maind-backed (shells out to `maind recall --json`)
│   ├── backends/
│   │   ├── backend.go          # Backend interface
│   │   ├── idesubagent.go      # IDE context: uses the IDE's model access (no keys)
│   │   ├── anthropic.go        # CLI context: direct REST (ANTHROPIC_API_KEY)
│   │   ├── openai.go           # CLI context: direct REST (OPENAI_API_KEY)
│   │   ├── openrouter.go       # optional, single HTTP API
│   │   └── cursorsdk.go        # Composer / Cursor-only (via JS helper; see note)
│   └── cli/
│       ├── root.go
│       ├── run.go              # the default route command
│       ├── connect.go          # writes per-IDE rules (the @@ adapter installer)
│       ├── list.go             # alias listing + availability
│       └── repl.go             # interactive mode with live @@ autocomplete
├── adapters/
│   ├── cursor/
│   │   ├── agents/
│   │   │   ├── opus-planner.md
│   │   │   └── composer-implementer.md
│   │   └── rules/
│   │       └── prismag-routing.mdc  # installed by `prismag connect cursor`
│   └── vscode/
│       └── extension/          # optional Chat Participant (thin JS shim → prismag binary)
├── registry.yaml               # default alias → model map
├── go.mod
└── README.md
```

---

## Execution contexts & credentials

Where the models come from — and therefore what's "available" — depends on **where
PRISMAG runs**. Same `registry.yaml`, same `@@aliases`; the resolver picks a
backend based on context.

| | **In the IDE** (`@@` + rule) | **In the CLI** (standalone) |
|---|---|---|
| Model source | The IDE's own access (your Cursor/Copilot subscription) | **Your API keys** |
| Mechanism | Delegate to **subagents** using the IDE's model picker | Direct provider APIs (Anthropic / OpenAI / OpenRouter) |
| Credentials | **None** — the IDE bills usage | Per-backend keys you supply |
| "Available" means | Models the IDE exposes + subagent supports | Backends you hold a key for |
| Reliability | Best-effort invocation; subagent `model:` can be ignored | Deterministic |

### Availability is credential-driven (CLI)

In the CLI, which aliases route depends on which keys you have:

```
ANTHROPIC_API_KEY only:          + OPENAI_API_KEY:
  @@opus      ✓ ready              @@opus      ✓ ready
  @@fast      ⚠ needs OPENAI_API_KEY   @@fast  ✓ ready
  @@composer  ⚠ needs Cursor SDK    @@composer  ⚠ needs Cursor SDK
```

The `availability` resolver returns `ready | needs-key | needs-sdk` per alias, and
`suggest`/autocomplete surfaces it so the user sees what's usable before tagging.

### Two kinds of backends

- **`ide-subagent`** — used inside the IDE: routes by delegating to the IDE's models;
  no API keys, but best-effort (parent agent must obey, subagent routing is buggy).
- **`anthropic` / `openai` / `openrouter`** — used in the CLI: deterministic, but
  require the user's keys.

### The Composer asymmetry

`@@composer` (a Cursor-only model) is reachable **inside Cursor** via subagent, but
**not** from a plain CLI with only API keys — there is no public provider endpoint
for it. In the CLI it requires the Cursor SDK, so its status is `ready` in Cursor
and `needs-sdk` in the CLI.

### Default when both paths are possible

If the user is inside an IDE **and** has API keys, the rule can either delegate to
subagents (subscription, best-effort) or shell out to the CLI (keys, deterministic).
**Default: prefer the CLI tool when keys exist** (deterministic + verbatim output),
fall back to subagents when they don't (no keys needed, accept best-effort).

---

## Phases

### Phase 1 — CLI + Cursor convention (MVP)

**Goal:** Prove the concept. Usable today without building an extension.

- [ ] Parser: `@@alias: task` → structured task list + shared preamble
- [ ] Registry: YAML alias → model ID
- [ ] CLI: `prismag` command, serial + chained context; `--parallel` for independent blocks
- [ ] Backends: direct Anthropic + OpenAI APIs (no self-hosted gateway); optional OpenRouter
- [ ] Availability resolver: per-alias `ready | needs-key | needs-sdk` from env/credentials
- [x] Discoverability: `prismag list`, shell completion, and REPL with live `@@` autocomplete
- [x] Interactive session: bare `prismag` (or `prismag repl`) opens a `prismag>` loop —
      blocks chain across turns, each session is saved as a markdown transcript under
      `~/.config/prismag/sessions/`, and `:remember <note>` pushes curated memory to maind
- [ ] `ContextStore`: `InMemoryStore` default — `recall(query, budget)` for token-capped chaining
- [ ] Sessions: `--session <id>` (default: hash of cwd + git branch); labeled transcript persisted as JSON
- [ ] Workspace context: gather git diff + named/globbed files once per run
- [ ] `prismag connect cursor`: write the `@@` routing rule + 2 subagents — best-effort invocation
- [ ] Output: sectioned markdown report with per-task results

**Success criteria:** User types a `@@`-tagged prompt in the terminal, each block
runs on the right model, the output of block N is available as context to block
N+1, and a 2-block serial run returns in <30s.

### Phase 2 — VS Code extension

**Goal:** Native in-chat UX for VS Code (and Cursor if Chat API is exposed).

- [ ] VS Code Chat Participant: `@router`
- [ ] Slash commands: `/opus`, `/composer`, `/route`
- [ ] Stream combined response back to chat panel
- [ ] Settings UI for registry editing
- [ ] `MaindStore`: persistent, encrypted, cross-session memory via the same `ContextStore` interface
- [ ] `recall` upgrade: FTS5/semantic retrieval (via maind) instead of plain truncation

**Success criteria:** `@router @@opus: plan this @@composer: build it` works in VS Code chat.

### Phase 3 — Ecosystem

- [ ] Watch mode: monitor clipboard or file for tagged prompts
- [ ] Cost tracking per alias per session
- [ ] Smart context chunking/summarization on `write` (better recall quality)
- [ ] Publish to npm + VS Code Marketplace

---

## Tech stack (proposed)

| Layer | Choice | Why |
|-------|--------|-----|
| Language | **Go** | Single static binary, fast startup (matters for an agent that auto-runs it per block), minimal deps — and the **same stack as maind** |
| CLI framework | `spf13/cobra` | Same as maind; the standard Go CLI toolkit |
| Model backends | Direct provider REST via `net/http` (Anthropic, OpenAI) + optional OpenRouter | Smallest attack surface; no vendor SDK; keys go straight to vendors; no self-hosted gateway |
| Memory / context | `ContextStore` interface — in-memory default, [maind](https://github.com/rufus-SD/maind) backend (shell out to `maind recall --json`) | Local, encrypted (AES-256-GCM), no network service; shared between CLI and IDE agent |
| Config | YAML (`registry.yaml`) via `gopkg.in/yaml.v3` | Human-readable, git-friendly |
| VS Code extension | thin **JS shim** (Chat Participant) that shells out to the `prismag` binary | The only JS needed; the routing core stays in Go |
| Cross-IDE trigger | `@@` text sigil + rules, installed by `prismag connect` | No extension API for Agent chat; rules + tool calls are the available mechanism, and `@@` avoids the native `@` mention collision |
| Testing | Go `testing` (table-driven) | Stdlib, fast, no extra deps |

> **Why Go (not TypeScript).** PRISMAG ships as a CLI that agents auto-invoke per
> `@@` block, so single-binary distribution and near-instant startup matter. Go
> also keeps the dependency/supply-chain surface tiny (provider APIs are plain
> REST — no SDK), and it matches **maind**, so both tools share one language,
> toolchain, and mental model. The only thing that *must* be JS — the optional VS
> Code Chat Participant extension and the Cursor SDK path for `@@composer` — is a
> thin shim that calls the Go binary; the routing core never leaves Go.

> **No LiteLLM.** A self-hosted gateway adds a second routing layer plus a Python
> proxy, DB, and admin UI to trust and patch — and LiteLLM had a March 2026 PyPI
> supply-chain compromise that harvested credentials. PRISMAG already *is* the
> router, so we call provider APIs directly and keep the dependency surface minimal.

---

## Non-goals (v1)

- Replacing the IDE's agent loop entirely
- Auto-routing without user-specified `@@tags` (that's OpenRouter's job)
- Training a classifier model to pick aliases
- Supporting every IDE on day one
- Real-time streaming merge of multiple model outputs in CLI (Phase 2+)

---

## Known limitations

**Two distinct gaps — only one is solvable in-IDE today:**

| Capability | IDE exposes it? | Where it works |
|------------|-----------------|----------------|
| Agent calls an external **tool** (maind for memory) | **Yes** (tool/MCP, `connect`) | Memory is **native everywhere** |
| Code **routes blocks to different models** | **No** | Routing stays **CLI-guaranteed**, IDE best-effort |

1. **In-IDE invocation is rule-driven (empirically reliable).** The `@@` trigger
   relies on the installed rule prompting the agent to shell out to the CLI. In
   practice this works well — validated across maind and two other rule-triggered
   tools, where the agent reliably runs the command. The CLI's routing is then 100%
   deterministic. The two residual soft spots are narrow: (a) the agent occasionally
   paraphrasing tool output instead of showing it verbatim (mitigated by an explicit
   "show verbatim" instruction in the rule), and (b) subagent `model:` routing bugs
   when delegating instead of using the CLI (see #2). The **CLI run is the
   deterministic core; rule-driven invocation has proven dependable.**
2. **Subagent `model:` routing** in Cursor has known bugs (frontmatter sometimes
   ignored), so a block may run on the wrong model without signaling it. Low stakes
   for memory recall; a correctness issue for routing — another reason the CLI is
   the reliable path.
3. **Cursor-only models** (Composer) may only be reachable via the Cursor SDK, not
   direct provider APIs or OpenRouter.
4. **Memory/context is solved**, not manual: the `ContextStore` (in-memory default,
   maind backend) handles inter-block context and conversation history. With maind
   wired in via `connect`, the IDE's native agent reads/writes the *same* memory the
   CLI uses — so context persists across blocks, runs, sessions, and surfaces.
5. **Auto-running commands is a trust grant.** `connect` allowlists the agent
   to run `maind`/`prismag` without prompting. Keep that surface narrow and
   read-mostly (`maind recall` yes, arbitrary shell no), pin dependencies, and use
   signed releases — an allowlisted, auto-running binary is high-impact if compromised
   (cf. the LiteLLM supply-chain incident).
6. **No API to enumerate IDE models.** Inside an IDE there's no public way to list
   which models are actually available, so IDE-context autocomplete falls back to the
   configured registry (and the rule listing aliases), not a live IDE model list. Live
   `@@` autocomplete is fully solved only in the CLI REPL and a native VS Code extension.

---

## Differentiation

| vs LiteLLM / OpenRouter | vs Cursor subagents | vs LangGraph / CrewAI |
|-------------------------|---------------------|----------------------|
| User picks model per block, not auto | Explicit `@alias` syntax, not implicit delegation | Chat-native DSL, not Python/YAML pipelines |
| Routes in the client, not a self-hosted proxy | Portable across IDEs | Minutes to set up, not hours |
| Direct provider APIs, minimal attack surface | Shared persistent memory (maind) across surfaces | Implicit context by message order, not an explicit DAG |

---

## Open questions

- [ ] Default `recall` token budget: fixed value, or per-model from the registry?
- [ ] History default: shared-but-alias-labeled transcript (leaning yes) vs strictly per-alias threads?
- [ ] How aggressively should the orchestrator chunk a task's output on `write` (full blob vs structured decision/constraint/snippet) — better recall vs more model calls?
- [ ] Cursor SDK: local runtime only, or cloud agents too?
- [ ] License: MIT (max adoption) or AGPL (prevent SaaS wrappers)?

**Resolved**

- ~~Should results be merged into one response or shown as separate sections?~~ →
  Sectioned markdown by alias in Phase 1 (clearer for CLI + debugging).
- ~~How to pass workspace context to each routed task?~~ → CLI gathers git diff +
  named/globbed files once per run as shared context; inter-task context flows
  through the `ContextStore` (`recall(query, budget)`).
- ~~Model gateway choice?~~ → No LiteLLM; direct provider APIs + optional OpenRouter.
- ~~Name?~~ → **PRISMAG** (one prompt → a spectrum of models; pairs with maind).
- ~~Trigger collision with the IDE's `@`?~~ → Use `@@` everywhere; CLI also accepts bare `@`.

---

## One-liner

> **PRISMAG** — the missing `@@model` layer between your IDE and your LLMs.
