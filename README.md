<p align="center"><img src="internal/web/static/logo.svg" alt="RouteCat" width="160"></p>

# RouteCat

**Open-source AI inference gateway** — routes user requests to community GPU nodes, meters tokens transparently, pays providers via Lightning Network.

[route.cat](https://route.cat) · [API Docs](https://route.cat/docs/) · [Telegram](https://t.me/routecat) · [Run a Node](#run-a-node) · [Self-host](#self-hosting)

> **Status: v0.2 beta** — The gateway is live and serving requests at [route.cat](https://route.cat).
> Available in 8 languages: English, Catalan, Spanish, Galician, Basque, French, German, Italian.

---

## Why RouteCat?

Most AI inference gateways are black boxes: closed-source billing, opaque routing, unverifiable payouts. RouteCat is different:

- **Fully open source** — Every line of code is public. Audit the billing, routing, and payment logic yourself.
- **Privacy first** — We don't log prompts or responses. Provider nodes never see your identity. No emails, no tracking, no cookies.
- **Transparent billing** — Per-token pricing with a flat 5% gateway fee. Every job is logged and publicly auditable via `/v1/audit`.
- **Lightning payments** — Providers earn sats automatically. Users top up with Lightning — no banks, no credit cards.
- **OpenAI compatible** — Drop-in replacement. Change your `base_url` and it works with any OpenAI SDK.
- **Community powered** — Anyone with a GPU can join as a provider node and earn Bitcoin from idle compute.

## Quick start

### As a user (send requests)

```bash
# 1. Get an API key (10 free playground requests/day)
curl -X POST https://route.cat/v1/auth/register \
  -d '{"name":"my app"}'

# 2. Top up your balance with Lightning
curl -X POST https://route.cat/v1/auth/topup \
  -H "Authorization: Bearer rc_YOUR_KEY" \
  -d '{"amount_sats":1000}'
# Returns a Lightning invoice — pay it with any wallet

# 3. Send a chat completion
curl https://route.cat/v1/chat/completions \
  -H "Authorization: Bearer rc_YOUR_KEY" \
  -H "Content-Type: application/json" \
  -d '{"model":"qwen2.5:7b","messages":[{"role":"user","content":"Hello!"}],"stream":true}'
```

Or with the OpenAI Python SDK:

```python
from openai import OpenAI

client = OpenAI(base_url="https://route.cat/v1", api_key="rc_YOUR_KEY")
response = client.chat.completions.create(
    model="qwen2.5:7b",
    messages=[{"role": "user", "content": "Hello!"}],
    stream=True,
)
for chunk in response:
    print(chunk.choices[0].delta.content or "", end="")
```

### Use with your tools

RouteCat works with any OpenAI-compatible client. See the full [integration guides](https://route.cat/docs/#integrations) for step-by-step setup:

| Tool | Config |
|------|--------|
| **VS Code (Continue)** | `apiBase: https://route.cat/v1` in `config.yaml` |
| **Cursor** | Settings → Models → OpenAI-compatible |
| **Open WebUI** | Settings → Connections → add URL + key |
| **ChatBox** | Settings → OpenAI API Compatible |
| **OpenCode** | `opencode.json` with `@ai-sdk/openai-compatible` provider |

### As a provider (earn Bitcoin)

Use our [Owlrun fork](https://github.com/routecat/owlrun) (multi-language dashboard, modular UI) or any compatible provider client:

```ini
# ~/.owlrun/owlrun.conf
[marketplace]
gateway = https://route.cat

[account]
lightning_address = you@walletofsatoshi.com
```

Restart the client. Your node will register, appear on the network, and start receiving inference jobs.

## API

Full documentation at **[route.cat/docs](https://route.cat/docs/)** — includes request/response schemas, streaming, billing details, error codes, and integration guides.

| Method | Endpoint | Auth | Description |
|--------|----------|------|-------------|
| `POST` | `/v1/chat/completions` | Bearer | Chat completion (streaming SSE) |
| `GET`  | `/v1/models` | — | List available models with pricing |
| `POST` | `/v1/auth/register` | — | Create a user API key |
| `POST` | `/v1/auth/topup` | Bearer | Generate Lightning invoice to top up balance |
| `GET`  | `/v1/auth/balance` | Bearer | Check balance and remaining free requests |
| `GET`  | `/v1/stats` | — | Gateway stats: nodes, jobs, version, commit |
| `GET`  | `/v1/audit` | — | Public job log (anonymised billing data) |

### Authentication

```
Authorization: Bearer rc_YOUR_API_KEY
```

API keys start with `rc_` and are shown only once at creation. The free tier (10 requests/day) works only from the web playground. Direct API access requires a positive sats balance.

## Privacy

RouteCat acts as a privacy proxy between users and inference nodes:

- **No prompt logging** — requests are forwarded in real time and never stored
- **No user tracking** — no emails, no cookies, no analytics
- **Anonymous to providers** — nodes receive only the raw model request, never your identity
- **API key = identity** — generated with `crypto/rand`, no personal data attached
- **Open source** — verify these claims by reading the code

## Self-hosting

Run your own RouteCat gateway on your own infrastructure.

### Prerequisites

- Go 1.21+ (for building from source)
- SQLite (embedded, no external dependency)
- LND node (optional — payouts work without it, they just queue)
- A domain + reverse proxy (Caddy recommended for auto-HTTPS)

### Build from source

```bash
git clone https://github.com/routecat/routecat.git
cd routecat

# Build with version info
COMMIT=$(git rev-parse --short HEAD)
go build -ldflags "-X main.version=0.2.0 -X main.commit=$COMMIT" \
  -o routecat ./cmd/routecat

# Generate checksum for verification
sha256sum routecat > routecat.sha256
```

### Cross-compile for Linux (from Windows/macOS)

```bash
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 \
  go build -ldflags "-X main.version=0.2.0 -X main.commit=$(git rev-parse --short HEAD)" \
  -o routecat ./cmd/routecat
```

### Run

```bash
# Minimal (no Lightning — users can't pay, but inference works)
./routecat -addr :8080 -db data/routecat.db -fee 5.0

# Full (with LND payouts via Tailscale)
./routecat -addr :8080 -db data/routecat.db -fee 5.0 \
  -lnd-addr 100.x.x.x:8080 \
  -lnd-macaroon /path/to/admin.macaroon \
  -lnd-tls /path/to/tls.cert
```

### CLI flags

| Flag | Default | Description |
|------|---------|-------------|
| `-addr` | `:8080` | Listen address |
| `-db` | `routecat.db` | SQLite database path |
| `-fee` | `5.0` | Gateway fee percentage |
| `-lnd-addr` | *(none)* | LND REST API address |
| `-lnd-macaroon` | *(none)* | Path to LND macaroon |
| `-lnd-tls` | *(none)* | Path to LND TLS cert |
| `-version` | | Print version and exit |

### Deployment

See [`deploy/`](deploy/) for production setup:
- `routecat.service` — systemd unit file with security hardening
- `Caddyfile` — reverse proxy with auto-HTTPS and WebSocket support
- `setup.sh` — VPS setup script (Ubuntu, Caddy, Tailscale)

### Verify your build

Every running RouteCat instance exposes its git commit hash:

```bash
curl -s https://route.cat/v1/stats | jq '.commit'
```

To verify the binary matches the source:

```bash
# 1. Check the commit hash of the running instance
COMMIT=$(curl -s https://route.cat/v1/stats | jq -r '.commit')

# 2. Build from that exact commit
git checkout $COMMIT
go build -ldflags "-X main.version=0.2.0 -X main.commit=$COMMIT" -o routecat-verify ./cmd/routecat

# 3. Compare checksums
sha256sum routecat routecat-verify
```

If the hashes match, the running binary was built from the public source code.

## Security

### Network & transport
- Rate limiting: 60 requests/min per IP (mutation endpoints only)
- Request body size limit: 1 MB
- User registration: 3 keys/hour per IP
- Constant-time API key comparison (timing attack resistant)
- systemd hardening: `NoNewPrivileges`, `ProtectSystem=strict`

### Anti-drain protections
- **Per-job token cap**: max 100K tokens per field, 200K total per job
- **Per-job earnings cap**: max 200 sats per single job
- **LND spending cap**: 10,000 sats/hour (configurable)
- **Minimum payout threshold**: 100 sats (prevents micro-payout spam)
- **Concurrent request limit**: max 3 simultaneous requests per user key
- **Payout requires valid payment hash**: no phantom payouts recorded

### Job & billing integrity
- WebSocket sender validation: nodes can only complete their own jobs
- Job proxy endpoint authenticated: only the assigned node can fetch the buyer's request
- Atomic balance deduction (SQLite transactions)
- Stale job cleanup: 10-minute timeout with partial compensation
- Invoice expiry: 10 minutes, idempotent crediting (no double-credit)

## Architecture

```
Users (buyers)              RouteCat Gateway              Nodes (providers)
──────────────           ─────────────────────          ──────────────────
                         ┌───────────────────┐
POST /v1/chat/       →   │   Public API      │
completions               │  (OpenAI compat)  │
                         ├───────────────────┤
                         │   Router          │  ← model match, VRAM,
                         │                   │    queue depth
                         ├───────────────────┤
                         │  WebSocket Hub    │  ←→  provider nodes
                         │  heartbeat 30s    │      (register, heartbeat,
                         │  job assignment   │       accept/reject,
                         │  proxy streaming  │       proxy chunks)
                         ├───────────────────┤
                         │  Billing Engine   │  ← token metering,
                         │  5% flat fee      │    per-job accounting
                         ├───────────────────┤
                         │  Lightning Payout │  ← LND via Tailscale,
                         │  threshold-based  │    auto-pay to providers
                         ├───────────────────┤
                         │  Invoice Watcher  │  ← user top-ups,
                         │                   │    balance crediting
                         ├───────────────────┤
                         │  Frontend + Docs  │  ← embedded static files,
                         │  8 languages      │    i18n, API docs
                         └───────────────────┘
```

### Packages

| Package | Description |
|---------|-------------|
| `cmd/routecat` | Entry point, CLI flags, service wiring |
| `internal/gateway` | WebSocket hub, node lifecycle, proxy streaming, HTTP server, security middleware |
| `internal/router` | Job routing: model match → lowest queue depth |
| `internal/billing` | Token metering, USD→msats conversion, BTC price feed (CoinGecko) |
| `internal/lightning` | LND REST client, LNURL resolution, payout engine, invoice watcher |
| `internal/store` | SQLite: nodes, jobs, payouts, user API keys, invoices, balances |
| `internal/api` | Public API, user registration, top-up, balance, audit log |
| `internal/web` | Embedded frontend: landing, docs, pricing, playground, i18n (8 locales) |

## Community

- **Telegram channel**: [t.me/routecat](https://t.me/routecat) — announcements and updates
- **Telegram chat**: [t.me/routecatchat](https://t.me/routecatchat) — questions, ideas, help
- **GitHub Issues**: [Report bugs](https://github.com/routecat/routecat/issues) or submit pull requests

## License

MIT — see [LICENSE](LICENSE).
