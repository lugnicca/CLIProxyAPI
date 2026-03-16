# JetBrains Junie Provider (JetBrains AI Subscription)

Use your JetBrains AI subscription to access GPT-5, Claude 4.6, Grok 4, Gemini 3, and more through CLIProxyAPI.

## Prerequisites

- A JetBrains AI subscription (comes with JetBrains IDE subscriptions)
- Go 1.22+ (to build from source)

## Install

The Junie provider is not yet in the official CLIProxyAPI release.
Build from this fork:

```bash
git clone https://github.com/lugnicca/CLIProxyAPI.git
cd CLIProxyAPI
git checkout feat/junie-provider
go build -o cli-proxy-api ./cmd/server/
```

## Setup

### 1. First-time config

If you don't have CLIProxyAPI configured yet:

```bash
# Create config directory
mkdir -p ~/.cli-proxy-api

# Create minimal config
cat > config.yaml << 'EOF'
host: ""
port: 8317
auth-dir: "~/.cli-proxy-api"
api-keys:
  - "sk-change-me-to-something-random"
EOF
```

Generate a random API key: `openssl rand -hex 24` and replace `sk-change-me-to-something-random`.

### 2. Login to JetBrains

```bash
./cli-proxy-api --junie-login
```

This opens your browser for JetBrains OAuth authentication.
After login, tokens are saved to `~/.cli-proxy-api/junie-account.json`.

### 3. Start the server

```bash
./cli-proxy-api
```

The server starts on port 8317 (or whatever you set in config.yaml).

### 4. Verify

```bash
API_KEY="sk-change-me-to-something-random"  # your key from config.yaml

# Test GPT via OpenAI format
curl http://localhost:8317/v1/chat/completions \
  -H "Authorization: Bearer $API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"model":"gpt4.1","messages":[{"role":"user","content":"hello"}],"max_tokens":10}'

# Test Claude via Anthropic format
curl http://localhost:8317/junie/v1/messages \
  -H "x-api-key: $API_KEY" \
  -H "anthropic-version: 2023-06-01" \
  -H "Content-Type: application/json" \
  -d '{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":"hello"}],"max_tokens":10}'
```

## Client Configuration

### Claude Code

```bash
ANTHROPIC_BASE_URL=http://YOUR_SERVER:8317/junie \
ANTHROPIC_API_KEY="YOUR_API_KEY" \
claude
```

Or as a shell alias:
```bash
alias cj='ANTHROPIC_BASE_URL=http://YOUR_SERVER:8317/junie ANTHROPIC_API_KEY="YOUR_API_KEY" claude --dangerously-skip-permissions'
```

### Hermes Agent

In `~/.hermes/config.yaml`:
```yaml
model:
  default: "gpt4.1"
  provider: "custom"
  base_url: "http://YOUR_SERVER:8317/v1"
```

Set the API key in `~/.hermes/.env`:
```
OPENAI_API_KEY=YOUR_API_KEY
```

Then: `hermes chat -m gpt4.1` or `hermes chat -m gpt-5`

### Any OpenAI-compatible client (aider, continue.dev, Cursor, etc.)

```bash
export OPENAI_BASE_URL=http://YOUR_SERVER:8317/v1
export OPENAI_API_KEY=YOUR_API_KEY
```

## Available Models

### OpenAI endpoint (`/v1/chat/completions`)

| Alias | Real Name | Provider |
|-------|-----------|----------|
| `gpt4.1` | gpt-4.1-2025-04-14 | OpenAI |
| `gpt4.1-mini` | gpt-4.1-mini-2025-04-14 | OpenAI |
| `gpt-5` | gpt-5-2025-08-07 | OpenAI |
| `gpt-5.4` | gpt-5.4-2026-03-05 | OpenAI |
| `o3` | o3-2025-04-16 | OpenAI |
| `o4-mini` | o4-mini-2025-04-16 | OpenAI |
| `claude-4.6-sonnet` | claude-sonnet-4-6 | Anthropic |
| `claude-4.6-opus` | claude-opus-4-6 | Anthropic |
| `grok-4` | grok-4 | xAI |
| `grok-4-fast` | grok-4-fast | xAI |

Aliases are mapped automatically. You can also use the real names directly.

### Anthropic endpoint (`/junie/v1/messages`)

Use real Anthropic model names:

- `claude-sonnet-4-6`
- `claude-opus-4-6`
- `claude-sonnet-4-5-20250929`
- `claude-haiku-4-5-20251001`

## Architecture

```
Client (Claude Code / Hermes / aider / curl)
  │
  ▼
CLIProxyAPI (your server:8317)
  │
  ├── /v1/chat/completions  (OpenAI format)
  │     ├── gpt*, o*    →  Ingrazzio OpenAI endpoint (pass-through)
  │     └── claude*     →  Ingrazzio Anthropic endpoint (auto-translated)
  │
  └── /junie/v1/messages  (Anthropic format)
        └── claude*     →  Ingrazzio Anthropic endpoint (pass-through)
                              │
                              ▼
                    JetBrains Ingrazzio backend
                              │
                              ▼
                    OpenAI / Anthropic / xAI / Google
```

## Running as a service (Linux)

```bash
# Copy binary
sudo cp cli-proxy-api /usr/local/bin/

# Create systemd service
cat > ~/.config/systemd/user/cliproxyapi.service << 'EOF'
[Unit]
Description=CLIProxyAPI Service
After=network.target

[Service]
Type=simple
WorkingDirectory=/path/to/your/config/directory
ExecStart=/usr/local/bin/cli-proxy-api
Restart=always
RestartSec=10

[Install]
WantedBy=default.target
EOF

systemctl --user daemon-reload
systemctl --user enable --now cliproxyapi
```

## Token Refresh

The OAuth token expires after ~1 hour. The proxy auto-refreshes using the refresh token in `junie-account.json`. If the refresh token expires (rare), re-run `--junie-login`.

## Troubleshooting

| Error | Fix |
|-------|-----|
| "Junie authentication not configured" | Run `cli-proxy-api --junie-login` |
| "Unsupported model" | Check `GET /v1/models` for available names |
| 401 / token expired | Usually auto-refreshes. If not, re-run `--junie-login` |
| Claude Code "model not found" | Use `ANTHROPIC_BASE_URL=http://server:8317/junie` (not `/v1`) |
| `max_tokens` error on GPT-5 | Use `max_completion_tokens` instead (OpenAI API change) |
