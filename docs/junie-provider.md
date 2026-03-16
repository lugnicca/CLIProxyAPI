# JetBrains Junie Provider (JetBrains AI Subscription)

Use your JetBrains AI subscription to access GPT-5, Claude 4.6, Grok 4, Gemini 3, and more through CLIProxyAPI.

## Prerequisites

- A JetBrains AI subscription (comes with JetBrains IDE subscriptions)
- CLIProxyAPI installed and running

## Setup (3 steps)

### 1. Login to JetBrains

```bash
cli-proxy-api --junie-login
```

This opens your browser for JetBrains OAuth. After login, tokens are saved to `~/.cli-proxy-api/junie-account.json`.

### 2. Verify it works

```bash
# GPT via OpenAI format
curl http://localhost:8317/v1/chat/completions \
  -H "Authorization: Bearer YOUR_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"model":"gpt4.1","messages":[{"role":"user","content":"hi"}],"max_tokens":10}'

# Claude via Anthropic format (for Claude Code)
curl http://localhost:8317/junie/v1/messages \
  -H "x-api-key: YOUR_API_KEY" \
  -H "anthropic-version: 2023-06-01" \
  -H "Content-Type: application/json" \
  -d '{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":"hi"}],"max_tokens":10}'
```

Replace `YOUR_API_KEY` with one of the keys from your `config.yaml` `api-keys` section.

### 3. Configure your client

#### Claude Code

```bash
ANTHROPIC_BASE_URL=http://YOUR_SERVER:8317/junie \
ANTHROPIC_API_KEY="YOUR_API_KEY" \
claude --dangerously-skip-permissions
```

Claude Code sends native Anthropic format → proxy forwards to JetBrains Ingrazzio → response returned as-is.

#### Hermes Agent

In `~/.hermes/config.yaml`:
```yaml
model:
  default: "gpt4.1"        # or claude-4.6-sonnet, gpt-5, etc.
  provider: "custom"
  base_url: "http://YOUR_SERVER:8317/v1"
```

Then: `hermes chat -m gpt4.1`

#### Any OpenAI-compatible client (aider, continue.dev, etc.)

```bash
export OPENAI_BASE_URL=http://YOUR_SERVER:8317/v1
export OPENAI_API_KEY=YOUR_API_KEY
```

## Available Models

### Via OpenAI endpoint (`/v1/chat/completions`)

| Alias | Real Model | Provider |
|-------|-----------|----------|
| `gpt4.1` | gpt-4.1-2025-04-14 | OpenAI |
| `gpt4.1-mini` | gpt-4.1-mini-2025-04-14 | OpenAI |
| `gpt-5` | gpt-5-2025-08-07 | OpenAI |
| `gpt-5.4` | gpt-5.4-2026-03-05 | OpenAI |
| `o3` | o3-2025-04-16 | OpenAI |
| `o4-mini` | o4-mini-2025-04-16 | OpenAI |
| `claude-4.6-sonnet` | claude-sonnet-4-6 | Anthropic (via Ingrazzio) |
| `claude-4.6-opus` | claude-opus-4-6 | Anthropic (via Ingrazzio) |
| `grok-4` | grok-4 | xAI |
| `grok-4-fast` | grok-4-fast | xAI |

Model aliases are mapped automatically. You can also use the real model names directly.

### Via Anthropic endpoint (`/junie/v1/messages`)

Use real Anthropic model names directly:

- `claude-sonnet-4-6`
- `claude-opus-4-6`
- `claude-sonnet-4-5-20250929`
- `claude-haiku-4-5-20251001`
- `claude-opus-4-1-20250805`

## Endpoints

| Endpoint | Format | Use case |
|----------|--------|----------|
| `POST /v1/chat/completions` | OpenAI | GPT, Claude*, Grok — for OpenAI-compatible clients |
| `POST /junie/v1/messages` | Anthropic Messages | Claude — for Claude Code and Anthropic-native clients |
| `GET /v1/models` | OpenAI | List all available models |
| `GET /junie/v1/models` | Anthropic | List Claude models available via Junie |

*Claude via `/v1/chat/completions` requires OpenAI↔Anthropic translation (automatic).

## How it works

```
Client (Claude Code / Hermes / aider / curl)
  │
  ▼
CLIProxyAPI (your server)
  │
  ├── /v1/chat/completions
  │     ├── model=gpt*,o*  →  Ingrazzio OpenAI endpoint (pass-through)
  │     └── model=claude*  →  Ingrazzio Anthropic endpoint (auto-translated)
  │
  └── /junie/v1/messages
        └── All Claude models →  Ingrazzio Anthropic endpoint (pass-through)
                                    │
                                    ▼
                          JetBrains AI (ingrazzio-cloud-prod.labs.jb.gg)
                                    │
                                    ▼
                          OpenAI / Anthropic / xAI / Google
```

## Token Refresh

The OAuth token expires after ~1 hour. The proxy auto-refreshes it using the refresh token stored in `junie-account.json`. If the refresh token expires (rare), re-run `--junie-login`.

## Troubleshooting

**"Junie authentication not configured"**
→ Run `cli-proxy-api --junie-login`

**"Unsupported model"**
→ Check `/v1/models` for available model names. Use aliases (`gpt4.1`) or real names (`gpt-4.1-2025-04-14`).

**Token expired / 401**
→ The proxy auto-refreshes tokens. If it still fails, re-run `--junie-login`.

**Claude Code says "model not found"**
→ Use `ANTHROPIC_BASE_URL=http://server:8317/junie` (not `/v1`). Claude Code only accepts Anthropic model names like `claude-sonnet-4-6`.
