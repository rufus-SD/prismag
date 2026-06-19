# Integrating PRISMAG into your tool

PRISMAG is a **deterministic routing protocol** any agent or editor can speak.
You don't need a PRISMAG-specific SDK or a Go import — you shell out to the
`prismag` binary and act on its output. This keeps a clean process boundary: your
tool never shares memory, env, or secrets with PRISMAG.

There are two roles you can play:

| You want… | Use | Who executes the models |
|-----------|-----|-------------------------|
| **Route, you execute** | `prismag route --json` | **Your tool** dispatches each block to its own models |
| **Route + execute** | `prismag run --api` | **PRISMAG** calls provider APIs and returns text |

The first is the recommended integration for an agent that already has model
access (subscription models, its own keys, local models): PRISMAG decides *who
runs what*, your tool runs it.

---

## 1. Routing protocol — `prismag route --json`

Pass the `@@`-tagged prompt as arguments (or on stdin). PRISMAG parses it,
resolves each block against the registry, and prints a plan as JSON on stdout.

```bash
prismag route --json "shared context here
@@opus: design the auth flow
@@composer: implement the middleware"
```

### Output schema

```json
{
  "context": "ide",
  "parallel": false,
  "preamble": "shared context here",
  "blocks": [
    {
      "index": 0,
      "alias": "opus",
      "rawAlias": "opus",
      "model": "claude-4.6-opus-high-thinking",
      "provider": "anthropic",
      "agent": "opus-planner",
      "task": "design the auth flow"
    },
    {
      "index": 1,
      "alias": "composer",
      "rawAlias": "composer",
      "model": "composer-2.5-fast",
      "provider": "cursor",
      "agent": "composer-implementer",
      "task": "implement the middleware"
    }
  ]
}
```

| Field | Meaning |
|-------|---------|
| `context` | Always `"ide"` for a plan (route never calls a backend). |
| `parallel` | `true` if blocks are independent (`--parallel`); else run **in order, chaining** each output into the next. |
| `preamble` | Shared context to prepend to **every** block (includes `--diff` / `--file` content if requested). |
| `blocks[]` | One entry per `@@tag`, in prompt order. |
| `blocks[].index` | Position in the prompt (0-based). |
| `blocks[].alias` | Normalized (lowercased) alias. |
| `blocks[].rawAlias` | Alias exactly as typed. |
| `blocks[].model` | Concrete model id to run this block on. |
| `blocks[].provider` | `anthropic` \| `openai` \| `openrouter` \| `cursor` \| `ollama` \| `vllm`. |
| `blocks[].agent` | Suggested subagent name (from the registry); omitted if none configured. |
| `blocks[].task` | The instruction for this block. |
| `blocks[].note` | Present when something needs attention (e.g. no subagent configured). |

### How to act on the plan

```
plan = json(prismag route --json "<tagged prompt>")
ctx  = plan.preamble
for block in plan.blocks:           # serial unless plan.parallel
    out = your_model_client.run(
              model = block.model,   # map to your own model handle
              system = ctx,
              prompt = block.task)
    if not plan.parallel:
        ctx = ctx + "\n\n## Prior block (@@" + block.rawAlias + ")\n" + out
    emit(block.rawAlias, out)
```

- **Serial (default):** run blocks in order; append each output to the context
  for the next block.
- **Parallel:** run blocks concurrently; each sees only `preamble`.
- `block.model` / `block.provider` are PRISMAG's deterministic decision — map
  them onto whatever model access your tool has.

### Flags

| Flag | Effect |
|------|--------|
| `--json` | Emit the plan as JSON (omit for human-readable text). |
| `--parallel` | Mark blocks independent (sets `"parallel": true`). |
| `--diff` | Include the working-tree git diff in `preamble`. |
| `--file <path/glob>` | Include file(s) in `preamble` (repeatable). |
| `--registry <path>` | Use a specific `registry.yaml`. |

The registry is otherwise resolved from `PRISMAG_REGISTRY`, the nearest
`registry.yaml` up from the cwd, or `~/.config/prismag/registry.yaml`.

---

## 2. Execute-for-me — `prismag run --api`

If your tool just wants results (no model access of its own), let PRISMAG execute
the blocks via provider REST APIs using the user's keys, and print sectioned
markdown:

```bash
prismag run --api "@@opus: design auth" "@@fast: summarize"
prismag run --api --parallel "@@opus: a" "@@fast: b"
```

Provider keys are read from the environment (`ANTHROPIC_API_KEY`,
`OPENAI_API_KEY`, `OPENROUTER_API_KEY`), or from maind / `~/.config/prismag/.env`.
Local providers (`ollama`, `vllm`) need no key.

---

## Errors & exit codes

- On success, the plan/result is written to **stdout**; exit code `0`.
- On error (empty prompt, a line starting with `@@` that isn't a valid tag, or an
  unknown alias), a message is written to **stderr** and the exit code is
  non-zero. Unknown-alias errors include a "did you mean?" hint.

Parse stdout only when the exit code is `0`.

---

## Stability

The `route --json` shape above is the integration contract and is kept stable.
The core packages live under `internal/` and may change freely behind this
boundary. (A public Go library facade may be added later for in-process
embedding; until then, the binary + JSON is the supported integration path.)
