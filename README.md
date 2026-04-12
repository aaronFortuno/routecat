# 🐱 RouteCat

**Open-source AI inference gateway** — routes user requests to community GPU nodes, meters tokens transparently, pays providers via Lightning Network.

[route.cat](https://route.cat) · [API Docs](#api) · [Run a Node](#run-a-node) · [Architecture](#architecture)

> **Status: v0.1 beta** — The gateway is functional and serving requests. Payment system, additional security hardening, and documentation are in active development.

---

## Why RouteCat?

Most AI inference gateways are black boxes: closed-source billing, opaque routing, unverifiable payouts. RouteCat is different:

- **Fully open source** — Every line of code is public. Audit the billing, routing, and payment logic yourself.
- **Transparent billing** — Per-token pricing with a flat 5% gateway fee. Every job is logged with token counts, earnings, and fees in SQLite.
- **Lightning payments** — Providers earn Bitcoin automatically when their balance exceeds their configured threshold. No bank accounts, no invoices, no delays.
- **OpenAI compatible** — Drop-in replacement. Change your `base_url` and it works with any OpenAI SDK.
- **Community powered** — Anyone with a GPU can join as a provider node and earn sats from idle compute.

## Quick start

### As a user (send requests)

```bash
# 1. Get an API key (free: 100 requests/day)
curl -X POST https://route.cat/v1/auth/register \
  -d '{"name":"my app"}'

# 2. Send a chat completion
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

### As a provider (earn Bitcoin)

If you run [Owlrun](https://github.com/fabgoodvibes/owlrun) or a compatible provider client, point it at RouteCat:

```ini
# ~/.owlrun/owlrun.conf
[marketplace]
gateway = https://route.cat
```

Restart the client. Your node will register, appear on the network, and start receiving inference jobs.

## API

All endpoints are OpenAI-compatible.

| Method | Endpoint | Description |
|--------|----------|-------------|
| `POST` | `/v1/chat/completions` | Chat completion (streaming SSE) |
| `GET`  | `/v1/models` | List available models with pricing |
| `POST` | `/v1/auth/register` | Create a user API key |

### Authentication

```
Authorization: Bearer rc_YOUR_API_KEY
```

### Models & pricing

Pricing is per million tokens. Providers keep 95%, the gateway takes a 5% flat fee.

```bash
curl https://route.cat/v1/models
```

Live pricing and a playground are available at [route.cat](https://route.cat).

## Architecture

```
Users (buyers)              RouteCat Gateway              Nodes (providers)
──────────────           ─────────────────────          ──────────────────
                         ┌───────────────────┐
POST /v1/chat/       →   │   Public API      │
completions               │  (OpenAI compat)  │
                         ├───────────────────┤
                         │   Router          │  ← model match, VRAM,
                         │                   │    queue depth, region
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
                         └───────────────────┘
```

### Components

| Package | Description |
|---------|-------------|
| `cmd/routecat` | Entry point, CLI flags, service wiring |
| `internal/gateway` | WebSocket hub, node lifecycle, proxy streaming, HTTP server, rate limiting |
| `internal/router` | Job routing: model match → lowest queue depth |
| `internal/billing` | Token metering, USD→msats conversion, BTC price feed (CoinGecko) |
| `internal/lightning` | LND REST client, LNURL resolution, payout engine with spending cap |
| `internal/store` | SQLite persistence: nodes, jobs, payouts, user API keys |
| `internal/api` | Public OpenAI-compatible API, user registration |
| `internal/web` | Embedded static frontend (landing, docs, playground) |

### Protocol

RouteCat implements the same WebSocket protocol as the Owlrun gateway, making it compatible with existing Owlrun provider nodes:

- **Registration**: `POST /v1/gateway/register` with node ID, GPU info, models, Lightning address
- **Control channel**: `WS /v1/gateway/ws?api_key=X` — heartbeat, job assignment, proxy streaming
- **Job flow**: gateway assigns job → node accepts → node fetches buyer request → streams Ollama response back → gateway meters tokens and bills

## Run a Node

### Requirements

- A GPU (NVIDIA recommended, 8GB+ VRAM)
- [Ollama](https://ollama.com) installed with at least one model
- [Owlrun](https://github.com/fabgoodvibes/owlrun) provider client (or compatible)

### Setup

1. Install Ollama and pull a model: `ollama pull qwen2.5:7b`
2. Install and run Owlrun
3. Edit `~/.owlrun/owlrun.conf`:
   ```ini
   [marketplace]
   gateway = https://route.cat

   [account]
   lightning_address = you@walletofsatoshi.com
   ```
4. Restart Owlrun — your node connects and starts earning

## Self-hosting

RouteCat is designed to be self-hosted. Run your own gateway:

```bash
# Build
go build -o routecat ./cmd/routecat

# Run (minimal, no Lightning)
./routecat -addr :8080 -db data/routecat.db -fee 5.0

# Run (with LND payouts)
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

### Deployment

See [`deploy/`](deploy/) for:
- `routecat.service` — systemd unit file
- `Caddyfile` — reverse proxy with auto-HTTPS
- `setup.sh` — VPS setup script (Ubuntu, Caddy, Tailscale)

## Security

- Rate limiting: 60 requests/min per IP
- Request body size limit: 1MB
- Constant-time API key comparison
- WebSocket message sender validation (nodes can only complete their own jobs)
- Stale job cleanup (2-minute timeout)
- LND spending cap: 10,000 sats/hour
- systemd hardening: `NoNewPrivileges`, `ProtectSystem=strict`

## Tech stack

- **Go** — gateway, routing, billing, API
- **SQLite** — persistence (nodes, jobs, payouts, API keys)
- **LND** — Lightning payments via REST API
- **Caddy** — reverse proxy, auto-HTTPS
- **Tailscale** — secure tunnel to LND node

Zero external dependencies beyond the Go standard library, SQLite, and the WebSocket library.

## Contributing

Contributions welcome. The project is MIT licensed.

```bash
git clone https://github.com/aaronFortuno/routecat.git
cd routecat
go build ./cmd/routecat
./routecat -addr :8080 -db test.db
```

## License

MIT — see [LICENSE](LICENSE).
