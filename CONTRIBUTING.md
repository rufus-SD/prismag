# Contributing to PRISMAG

Thanks for your interest in contributing.

## Getting started

```bash
git clone https://github.com/rufus-SD/prismag.git
cd prismag
make build
make test
```

Requires **Go 1.26+**. Dependencies are minimal — `spf13/cobra`, `golang.org/x/term`,
and `gopkg.in/yaml.v3`. There are no provider SDKs: backends call REST APIs directly.

## Development workflow

1. Fork the repo and create a branch from `main`
2. Make your changes
3. Add or update tests as needed
4. Run `make test` and `make lint` — both must pass
5. Open a pull request with a clear description

## Code style

- Follow standard Go conventions (`gofmt`, `go vet`)
- Keep functions focused and small
- Error messages should be lowercase, no trailing punctuation
- CLI output: user-facing messages to `stderr`, machine-parseable data (plans,
  JSON, IDs) to `stdout`

## Project structure

```
internal/
  parser/         @@alias: task → tasks + shared preamble
  registry/       alias → model mappings (registry.yaml)
  orchestrator/   dispatch, parallel/serial, context budget, aggregate, streaming
  availability/   execution context + per-alias status
  discovery/      model discovery (provider APIs / IDE cache)
  backend/        pluggable providers (anthropic, openai, openrouter) + Streamer
  contextstore/   ContextStore interface (in-memory default, maind backend)
  secrets/        API key loading/storage (env, .env, maind)
  workspace/      git diff + file globs as shared context
  cli/            Cobra commands (run, route, connect, setup, repl, ...)
adapters/         embedded IDE templates (cursor/, claude/ subagents)
```

## Adding an IDE integration

Each editor is one entry in `ideAdapterTable()` in `internal/cli/ide_adapters.go`:
its rule path, dispatch mode (`subagent` or `inline`), and optional embedded
subagent templates under `adapters/<tool>/`. Add the row, add a test, done.

## Testing

```bash
make test        # go test ./...
make lint        # go vet ./...
```

Tests use `t.TempDir()` for isolation — no cleanup needed.

## What to avoid

- Adding a self-hosted gateway or provider SDKs — PRISMAG calls REST APIs directly
- Network/cloud dependencies — it's a local-first CLI
- Large refactors without an issue/discussion first
