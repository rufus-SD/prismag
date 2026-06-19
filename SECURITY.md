# Security Policy

## Credential model

PRISMAG calls provider APIs **directly** — your keys go straight to the vendor
(Anthropic, OpenAI, OpenRouter). There is **no gateway, proxy, or telemetry**, and
no PRISMAG server ever sees your keys or prompts.

Keys are resolved in this order:

1. **Environment** — `ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `OPENROUTER_API_KEY`
2. **[maind](https://github.com/rufus-SD/maind)** — when present and unlocked,
   keys are stored encrypted (AES-256-GCM) instead of in plaintext
3. **`~/.config/prismag/.env`** — fallback plaintext file (`0600`)

Inside an IDE that dispatches subagents, blocks route through your editor
subscription and **no API keys are used at all**.

> Treat `~/.config/prismag/.env` as a secret. It is `0600` by design and should
> never be committed — the project `.gitignore` excludes `.env` files.

## Auto-run is a trust grant

`prismag connect` writes a rule that allowlists your AI agent to run the `prismag`
binary without prompting. An allowlisted, auto-running binary is high-impact if
compromised, so PRISMAG deliberately:

- ships as a **single static Go binary** with a tiny dependency set (no provider
  SDKs, no self-hosted services),
- calls provider REST APIs directly (smallest attack surface),
- recommends pinned dependencies and signed releases.

This stance is a direct response to gateway supply-chain risk (cf. the March 2026
LiteLLM PyPI compromise that harvested credentials).

## Reporting a vulnerability

If you discover a security issue, please report it responsibly:

1. **Do not** open a public GitHub issue
2. Email **[TODO: add contact email]** with details
3. Include steps to reproduce if possible

You will receive a response within 48 hours. We will coordinate a fix and
disclosure timeline with you.

## Scope

In scope:

- Credential leakage (env, `.env`, maind handoff)
- Command/argument injection in routing or `connect`
- Rule-file injection that could trigger unintended agent actions
- Dependency/supply-chain concerns

Out of scope:

- Attacks requiring physical access to an unlocked machine
- Social engineering
- Denial of service against the local CLI
- Provider-side model behavior (prompt injection in model output)
