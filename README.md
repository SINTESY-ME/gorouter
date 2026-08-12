<div align="center">

# gorouter

**The fastest AI gateway. One binary. Zero dependencies. Imperceptible overhead.**

[![Go](https://img.shields.io/badge/Go-1.25-00ADD8?logo=go&logoColor=white)](https://go.dev)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
![Docker](https://img.shields.io/badge/Docker-ready-2496ED?logo=docker&logoColor=white)
![Single Binary](https://img.shields.io/badge/Single%20Binary-no%20runtime-success)

A high-performance AI gateway written in Go that unifies access to every LLM provider through a single OpenAI-compatible API. Automatic failover, load balancing, semantic caching, MCP tool gateway, and an embedded dashboard — all in one static binary with no runtime, no VM, no interpreter.

</div>

---

## Quick Start

**Go from zero to production-ready AI gateway in under a minute.**

**Step 1:** Start gorouter

```bash
# Docker
docker run -p 20128:20128 ghcr.io/sintesy-me/gorouter:latest

# Or build from source
docker build -t gorouter . && docker run -p 20128:20128 gorouter
```

**Step 2:** Open the dashboard

```
open http://localhost:20128
```

**Step 3:** Make your first API call

```bash
curl -X POST http://localhost:20128/v1/chat/completions \
  -H "Authorization: Bearer <your-api-key>" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "openai/gpt-4o",
    "messages": [{"role": "user", "content": "Hello, gorouter!"}]
  }'
```

**That's it.** Your AI gateway is running with a web interface for visual configuration, real-time monitoring, and analytics.

---

## Why gorouter

| | gorouter | Bifrost | Portkey | LiteLLM |
|---|---|---|---|---|
| **Runtime** | Go (static binary) | Go (Docker) | Node (Docker) | Python |
| **Avg latency (c=50)** | **6.3 ms** | 11.6 ms | 209 ms | 1.15 s |
| **RPS (c=50)** | **~7.9k** | ~4.3k | ~238 | ~43 |
| **Overhead vs mock** | **~4.85 ms** | ~10.15 ms | ~207 ms | ~1.15 s |
| **Single binary** | ✅ | ❌ | ❌ | ❌ |
| **Embedded dashboard** | ✅ | ✅ | ✅ | ❌ |

> Benchmark methodology and full results: **[docs/BENCHMARK.md](docs/BENCHMARK.md)**

---

## Key Features

### Core Infrastructure

- **[Unified OpenAI-Compatible API](#api)** — Single `/v1/*` interface for every provider. Talk to Anthropic, Gemini, or any provider using the same OpenAI client SDK — gorouter translates transparently.
- **[Multi-Format Translation](#multi-format-translation)** — Speaks OpenAI Chat, Anthropic Messages, Gemini generateContent, and OpenAI Responses API. Route between formats without changing client code.
- **[Automatic Fallbacks](#fallback-strategies)** — Seamless failover between providers and models with zero downtime. Network errors, 5xx, 429s — all handled automatically.
- **[Load Balancing](#fallback-strategies)** — Distribute requests across multiple API keys and providers with round-robin or weighted strategies.

### Smart Routing

- **[Combos (Virtual Models)](#combos)** — Group multiple models under a single name with intelligent routing strategies: ordered fallback, round-robin, velocity-based, intelligence-classified, and weighted.
- **[Health Tracking](#health-tracking)** — Models that fail are marked unhealthy and skipped automatically. Background probes detect recovery without manual intervention.
- **[Context-Window Aware Routing](#context-window-aware-routing)** — Estimates prompt tokens and skips models whose context window can't fit the request — no wasted fallbacks on 400 errors.
- **[Connection-Level Fallback](#connection-level-fallback)** — Multiple API keys per provider are tried in rotation. Rate-limited connections are paused respecting `Retry-After` headers.

### Cost & Performance

- **[Response Cache](#response-cache)** — Deterministic-hash caching with LRU + TTL. Identical requests get cached responses with zero upstream latency and zero token cost. Per-request bypass via headers.
- **[Semantic Cache](#semantic-cache)** — Vector-similarity caching for near-duplicate prompts. Catches rephrased questions, paraphrased requests, and semantically equivalent queries.
- **[RTK Token Compression](#rtk-token-compression)** — 11 automatic filters compress request bodies (git diffs, logs, search results) to reduce input tokens before they reach the provider.
- **[Automatic Pricing](#automatic-pricing)** — Resolves per-model pricing from LiteLLM, models.dev, and OpenRouter in cascade with fuzzy matching. Cost tracking with zero hot-path overhead.
- **[Savings Tracker](#savings-tracker)** — Real-time metrics on tokens saved by cache hits and bytes saved by RTK compression.

### MCP Gateway

- **[MCP Tool Injection](#mcp-gateway)** — Connect external MCP servers (filesystem, web search, databases, GitHub) and inject their tools into combo requests. Each combo declares which MCP servers it uses.
- **[Aggregated `/mcp` Endpoint](#mcp-gateway)** — Agents like Codex and Claude CLI connect to gorouter as their MCP server. gorouter proxies `tools/call` to the owning upstream client.
- **[Server-Side Agent Loop](#mcp-gateway)** — For non-streaming OpenAI chat, gorouter executes tool calls automatically and re-dispatches until the model stops calling tools (max depth 5).

### Security & Governance

- **[Multi-User RBAC](#multi-user-rbac)** — Admin and member roles with per-user data isolation. Members see only their own providers, combos, and keys.
- **[API Key Management](#api-key-management)** — Keys are hashed at rest (SHA-256). Per-key rate limits (token bucket) and budget caps (spend limits). Per-key model access restrictions.
- **[OAuth Integration](#oauth-integration)** — PKCE OAuth flows for Codex and Gemini CLI providers with automatic token refresh.
- **[Hook Pipeline](#hook-pipeline)** — PreCall/PostCall/PostCallFailure hooks for moderation, prompt injection detection, request logging, Prometheus metrics, and webhook observability — all live-toggleable, zero-cost when disabled.

### Developer Experience

- **[Embedded Dashboard](#embedded-dashboard)** — React + Vite + Tailwind + HeroUI compiled into the binary via `go:embed`. No separate frontend to deploy.
- **[42-Language i18n](#embedded-dashboard)** — Full internationalization with 42 locales bundled at build time.
- **[Provider Catalog & Store](#provider-catalog)** — Browse and install provider templates from the dashboard. One-click setup for 20+ providers.
- **[42-Locale Dashboard](#embedded-dashboard)** — Full i18n support with 42 bundled locales, native language names, and RTL support.
- **[Zero-Config Startup](#quick-start)** — Start immediately with dynamic provider configuration via the web UI.

---

## Multi-Format Translation

Talk to any provider using the same OpenAI client. gorouter detects the upstream format and translates transparently.

```
You:        POST /v1/chat/completions     (OpenAI)
OpenAI:     POST /v1/chat/completions     ← no translation
Anthropic:  POST /v1/messages             ← translated
Gemini:     POST /v1beta/models/{m}:generateContent  ← translated
Responses:  POST /v1/responses            ← translated
```

All four client formats (`/v1/chat/completions`, `/v1/messages`, `/v1/responses`, Gemini) are accepted on every endpoint and translated to whatever the upstream provider speaks. Responses are translated back to the client's format.

---

## Combos

Combos are **virtual models** that group multiple real models under a single name. When you send a request using the combo name, gorouter routes automatically between the member models using the configured strategy.

```json
{
  "name": "smart",
  "models": ["openai/gpt-4o", "anthropic/claude-sonnet-5", "google/gemini-2.5-pro"],
  "strategy": "ordered_fallback",
  "mcp_clients": ["github", "filesystem"]
}
```

Then use `"model": "smart"` in any request — gorouter handles the rest. Combos appear in `/v1/models` with `owned_by: "combo"`.

### Fallback Strategies

| Strategy | Description |
|---|---|
| **`ordered_fallback`** | Tries models in configured order. If the first fails, tries the next. Ideal for cascata fallbacks. |
| **`round-robin`** | Rotates the starting model per request, distributing load. Unhealthy models are skipped automatically. |
| **`velocity`** | Ranks models by measured tokens-per-second. Fastest model goes first. Auto-probes models with no usage data. |
| **`intelligence`** | A classifier model evaluates the prompt and picks the best member. Requires per-model descriptions. |
| **`weighted`** | Distributes requests by configured weights (1-100 per model). Higher weight = more traffic. |

### How Fallback Works

| Condition | Fallback? | Reason |
|---|---|---|
| Network error / timeout | ✅ Yes | Transient infrastructure failure |
| 5xx (500-599) | ✅ Yes | Transient upstream error |
| 429 (Too Many Requests) | ✅ Yes | Rate limited |
| 408 (Request Timeout) | ✅ Yes | Timeout / unavailable |
| 401 (Unauthorized) | ✅ Yes | Try another account |
| 403 (Forbidden) | ✅ Yes | Try another account |
| 404 (Not Found) | ✅ Yes | Model removed/deprecated; next one might have it |
| 400, 422, 415 | ❌ No | Client error — will fail on all providers |

---

## Health Tracking

Models that fail are marked **unhealthy** and skipped on subsequent requests. Background probes run in parallel (20s timeout, minimal request) to detect when they recover — no manual downtime.

If all models in a combo are unhealthy, gorouter tries them all inline again (last-resort pass). If any succeeds, it's marked healthy immediately.

---

## Context-Window Aware Routing

gorouter **estimates prompt tokens** and **skips combo models whose context window can't fit** the request — a 10k-token prompt won't try a 4k-window model (which would 400 and burn a fallback). Requires context-window data from catalog sync; without data, nothing is filtered (fail-open).

---

## Connection-Level Fallback

Within each model, multiple connections (accounts) for the same provider are tried in round-robin. Connections that fail with 429/5xx are temporarily paused (respecting `Retry-After` or 5s default).

---

## Response Cache

Deterministic-hash caching. Identical requests receive the cached response **without calling the provider** — zero upstream latency, zero token cost.

- **LRU + TTL**: Configurable entry limit (default 10,000) with LRU eviction + per-entry TTL (default 5min) + background sweep
- **Deterministic normalization**: Ephemeral fields (`user`, `request_id`) are stripped and JSON keys sorted before hashing — same request with different field order = cache hit
- **Stream + non-stream**: Both supported; streams are accumulated and replayed verbatim
- **Per-request bypass**: `x-gr-cache: off` disables cache for a specific request
- **Per-request TTL**: `x-gr-cache-ttl: <seconds>` sets a shorter TTL for the stored entry
- **Caching groups**: Interchangeable models share the same cache entry
- **Observability**: `x-gr-cache-hit: true` response header; `GET /api/cache/stats` shows entries/hits/misses

```bash
# Enable via env
GOROUTER_CACHE_ENABLED=true
GOROUTER_CACHE_TTL=5m
GOROUTER_CACHE_MAX_ENTRIES=10000

# Bypass per-request
curl -H "x-gr-cache: off" -d '{"model":"...","messages":[...]}' http://localhost:20128/v1/chat/completions
```

Benchmark: cache hit is **~3× faster** than miss (14.7k vs 5.3k RPS at c=10, local mock).

---

## Semantic Cache

Vector-similarity caching for near-duplicate prompts. Catches rephrased questions and semantically equivalent queries that a deterministic hash would miss. Requires an embedding model; powered by cosine similarity with configurable threshold.

```bash
# Enable via env or dashboard
GOROUTER_SEMANTIC_CACHE_ENABLED=true
GOROUTER_SEMANTIC_CACHE_MODEL=openai/text-embedding-3-small
GOROUTER_SEMANTIC_CACHE_THRESHOLD=0.95
```

---

## RTK Token Compression

11 automatic filters compress request bodies to reduce input tokens before they reach the provider. Auto-detected by the first 1KB of content.

| Filter | What it compresses |
|---|---|
| `gitDiff` | Unified diffs — removes file headers, hunk metadata |
| `gitLog` | Git log output — trims hashes and dates |
| `grep` | Grep output — removes filenames on every line |
| `find` | Find output — collapses redundant path prefixes |
| `ls` | Directory listings — removes permissions, dates |
| `tree` | Tree output — strips indentation art |
| `buildOutput` | Build logs — deduplicates repeated lines |
| `dedupLog` | General logs — collapses repeated entries |
| `readNumbered` | Numbered file content — removes line numbers |
| `smartTruncate` | Long text — truncates at a sensible boundary |
| `searchList` | Search results — collapses redundant metadata |

Fail-open: if anything fails, the original request goes through intact.

---

## MCP Gateway

Connect external MCP servers and expose their tools to your models — either through tool injection into combo requests or through the aggregated `/mcp` endpoint for agents like Codex and Claude CLI.

### How it works

1. **Register MCP servers** in the dashboard (HTTP, SSE, or stdio transports; bearer auth supported)
2. **Attach MCP servers to combos** — each combo declares which MCP servers it uses via `mcp_clients`
3. **Tool injection** — when a request routes through a combo with MCPs, gorouter merges those servers' tools into the request body (format-aware: OpenAI, Anthropic, Responses)
4. **Agent loop** — for non-streaming OpenAI chat, gorouter executes tool calls server-side and re-dispatches until the model stops calling tools (max depth 5)
5. **Aggregated `/mcp` endpoint** — agents connect to gorouter as their MCP server; gorouter proxies `tools/call` to the owning upstream client

```bash
# Agents connect to gorouter as their MCP server
# Codex / Claude CLI / any MCP client:
POST /mcp  (JSON-RPC: initialize, tools/list, tools/call)
# Auth: same API key as /v1/*
```

Tools are only injected for **combos that declare MCP clients** — direct model requests and combos without MCPs are never touched.

---

## Automatic Pricing

gorouter resolves per-model pricing automatically during catalog sync, in cascade: **LiteLLM → models.dev → OpenRouter**, with **fuzzy matching** as fallback:

- **Exact match** (provider + model): direct registry lookup
- **Name match**: model without provider prefix
- **Fuzzy matching**: safe suffix stripping, containment, and Levenshtein distance for typos/variants
- **Free models ($0)**: models with cost=0 from any source are accepted as valid pricing
- **Zero hot-path overhead**: pricing is resolved once at sync time and cached in memory; the hot path does only `RLock + map[string]` lookup (nanoseconds)

```bash
# Manual price override (dashboard or API)
POST /api/model-pricing
```

---

## Savings Tracker

Real-time metrics on cost savings from cache and compression:

```bash
GET /api/savings
# {
#   "cache_hits": 1542,
#   "cache_tokens_saved": 8200000,
#   "cache_cost_saved": 184.50,
#   "rtk_compressions": 320,
#   "rtk_bytes_saved": 1500000,
#   "rtk_tokens_saved": 375000,
#   "semantic_hits": 87,
#   "semantic_tokens_saved": 410000
# }
```

---

## Multi-User RBAC

Admin and member roles with per-user data isolation:

- **Admins** see and manage everything (providers, combos, keys, models, users, settings)
- **Members** see only their own providers, combos, and keys — granted via per-user access lists
- **Session-based auth** with bcrypt password hashing
- **Setup wizard** on first launch: create the admin account via the dashboard

---

## API Key Management

- Keys are **hashed at rest** (SHA-256). Plaintext is returned exactly once at creation time.
- **Rate limits**: per-key token bucket (requests per rolling window)
- **Budget caps**: per-key spending limits (max USD per rolling window)
- **Model restrictions**: each key can be restricted to specific models/combos (`allowed_models`)
- **HMAC CRC**: fast in-memory rejection of fabricated keys before any DB lookup

---

## OAuth Integration

PKCE OAuth flows for providers that require browser-based authentication:

- **Codex** (ChatGPT backend)
- **Gemini CLI** (Google Cloud Code)
- **Antigravity** (Google)

Automatic token refresh before upstream calls. Paste-code flow in the dashboard.

---

## Hook Pipeline

PreCall / PostCall / PostCallFailure hooks — inspired by LiteLLM's `CustomLogger`. Moderation, logging, and metrics are plugins, never touching the router core. Hooks are live-toggleable via the dashboard with **zero cost when disabled** (`Router.Hooks` stays nil; every hook point is a single branch).

```bash
curl -X PUT localhost:20128/api/settings \
  -H 'Content-Type: application/json' \
  -d '{"hooks_enabled":["keyword_moderation","prompt_injection_heuristic","request_logging","prometheus"]}'
```

| Hook | Point | Effect |
|---|---|---|
| `keyword_moderation` | PreCall | Rejects (400) messages matching `GOROUTER_HOOK_MODERATION_PATTERNS` |
| `prompt_injection_heuristic` | PreCall | Rejects (400) common prompt injection patterns |
| `request_logging` | PostCall / Failure | Structured slog of success/failure per request |
| `prometheus` | PostCall / Failure | Feeds metrics to `/metrics` |
| `webhook_logging` | PostCall / Failure | Sends request events to an HTTP URL (Slack, Datadog, custom) |

### Webhook Observability

With `webhook_logging` enabled and `GOROUTER_HOOK_WEBHOOK_URL` set, each request generates an async POST with the event (fail-open — webhook downtime never affects the request):

```json
{
  "event": "request.completed",
  "request_id": "...",
  "model": "gpt-4o",
  "provider": "openai",
  "status": 200,
  "prompt_tokens": 10,
  "completion_tokens": 5,
  "cost": 0.0012,
  "latency_ms": 500
}
```

### Prometheus

`GET /metrics` exposes Prometheus-format metrics with zero dependencies:

- **Request path**: `gorouter_requests_total`, `gorouter_failed_requests_total`, `gorouter_request_duration_seconds`, `gorouter_request_ttft_seconds`, `gorouter_tokens_input_total`, `gorouter_tokens_output_total`, `gorouter_spend_usd_total` — labeled by model/endpoint/status
- **State gauges**: `gorouter_health_*`, `gorouter_cache_*`, `gorouter_semantic_cache_*`, `gorouter_savings_*`, `gorouter_uptime_seconds`

---

## Embedded Dashboard

React + Vite + Tailwind + HeroUI compiled into the binary via `go:embed`. Manage providers, combos, keys, models, MCP servers, and visualize usage analytics in real time — no separate frontend to deploy.

**Pages:**
- **Dashboard** — usage stats, cost charts, savings (cache + RTK + semantic), pie charts by provider/model
- **Providers** — connections, provider catalog & store, OAuth flows, model sync
- **Combos** — combo editor with drag-and-drop model ordering, strategy selection, MCP server attachment
- **Models** — model catalog with pricing, kind, stats, activate/deactivate, manual price override
- **MCP Gateway** — MCP server management (add/edit/remove, connection status, discovered tools)
- **Playground** — live chat testing with streaming
- **Keys** — API key management with rate limits, budget caps, model restrictions
- **Logs** — request history with prompt/completion tokens, cache badges, cost
- **Performance** — live toggles for RTK, cache, semantic cache; cache stats; flush buttons
- **Settings** — hooks, webhook, caching groups, semantic cache configuration
- **Users** — admin-only user management with role-based access control

**42 locales** bundled at build time with native language names and RTL support.

---

## Provider Catalog

Browse and install provider templates from the dashboard. One-click setup for 20+ providers including OpenAI, Anthropic, Google, Groq, DeepSeek, Mistral, Together, Ollama, OpenRouter, and more. The catalog syncs from the [gorouter providers repository](https://github.com/SINTESY-ME/gorouter).

---

## Multimodal Support

gorouter routes **all model types** via combos with fallback:

- **LLM** — chat completions, streaming, vision, reasoning
- **Embeddings** — vectors for RAG, semantic search
- **Images** — image generation (DALL-E, Stable Diffusion, Midjourney)
- **Audio** — TTS (text-to-speech) and STT (speech-to-text, Whisper)
- **Rerank** — document reordering
- **OCR** — text extraction from images
- **Video** — video generation and processing

Each type has its own endpoint (`/v1/chat/completions`, `/v1/embeddings`, `/v1/images/generations`, `/v1/audio/speech`, etc.) and all work with combos.

---

## API

### `/v1/*` — OpenAI-Compatible API

| Endpoint | Description |
|---|---|
| `GET /v1/models` | List available models (includes combos) |
| `POST /v1/chat/completions` | Chat completion (streaming or non-streaming) |
| `POST /v1/completions` | Completion (alias) |
| `POST /v1/messages` | Anthropic-style messages |
| `POST /v1/responses` | OpenAI Responses API |
| `POST /v1/embeddings` | Embeddings |
| `POST /v1/images/generations` | Image generation |
| `POST /v1/audio/speech` | TTS |
| `POST /v1/audio/transcriptions` | STT |
| `POST /v1/mcp/tool/execute` | Execute an MCP tool (`?format=chat\|responses`) |
| `POST /mcp` | Aggregated MCP gateway (JSON-RPC) |

### Health Probes (public, no auth)

| Endpoint | Description |
|---|---|
| `GET /health` | 200 + `{status, uptime_seconds}` |
| `GET /health/liveliness` | 200 if process is alive (K8s liveness) |
| `GET /health/readiness` | 200 if ready (DB ok + provider configured); 503 otherwise |
| `GET /metrics` | Prometheus metrics |

### Control & Debug Headers

| Header | Direction | Description |
|---|---|---|
| `x-gr-cache: off` | request | Bypass cache for this request |
| `x-gr-cache-ttl: <sec>` | request | Override TTL for the cached entry |
| `x-gr-timeout: <sec>` | request | Override upstream timeout (non-streaming) |
| `x-gr-cache-hit: true` | response | Response came from cache |
| `x-gr-model` | response | Real model that served the request (useful in combos) |
| `x-gr-attempted-retries` | response | How many fallbacks/retries occurred |

### Usage

```bash
# Create an API key in the dashboard, then:
curl http://localhost:20128/v1/chat/completions \
  -H "Authorization: Bearer <your-api-key>" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "smart",
    "messages": [{"role": "user", "content": "Hello!"}],
    "stream": true
  }'
```

### Dashboard API (`/api/*`)

Protected by dashboard auth. Full CRUD for providers, connections, combos, keys, models, MCP clients, users, plus usage analytics and settings.

| Endpoint | Description |
|---|---|
| `GET /api/mcp/clients` | List MCP clients with live status |
| `POST /api/mcp/clients` | Create and dial an MCP client |
| `GET /api/mcp/tools` | List all exposed MCP tools |
| `GET /api/savings` | Savings (cache + RTK + semantic) |
| `GET /api/cache/stats` | Cache stats (entries, hits, misses) |
| `POST /api/cache/flush` | Flush cache |
| `GET /api/settings` | Settings (RTK, cache, hooks, semantic cache) |
| `PUT /api/settings` | Update settings (live toggle) |
| `POST /api/model-pricing` | Manual price override |

---

## Architecture

```
┌──────────────────────────────────────────────────────────────────┐
│                        interfaces/http                            │
│   chi router  │  /v1/* handlers  │  /api/* handlers  │  /mcp     │
└───────────────────────────┬──────────────────────────────────────┘
                            │
┌───────────────────────────▼──────────────────────────────────────┐
│                          application                              │
│  RouterService  │  ComboService  │  MCPService  │  UsageService  │
│  AuthService    │  HookPipeline  │  CacheService                 │
└───────────────────────────┬──────────────────────────────────────┘
                            │  (ports / interfaces)
┌───────────────────────────▼──────────────────────────────────────┐
│                         infrastructure                            │
│  executor  │  translator  │  sse  │  mcp gateway  │  GORM repos  │
│  responsecache  │  semanticcache  │  rtk  │  metrics  │  redis     │
└──────────────────────────────────────────────────────────────────┘
                            │
               ┌────────────┼────────────┐
               │            │            │
          OpenAI      Anthropic       Gemini
          (any /v1)   (Messages)    (generateContent)
```

**Layering:** `interfaces/http` (transport) → `app` (services, no I/O) → `domain` (ports/entities) ← `infra/*` (adapters). Everything is constructed in `cmd/gorouter/main.go`.

---

## Configuration

All environment variables are optional:

| Variable | Default | Description |
|---|---|---|
| `GOROUTER_PORT` | `20128` | HTTP port |
| `GOROUTER_HOME` | `~/.gorouter` | Data directory |
| `GOROUTER_DB` | `<home>/gorouter.db` | SQLite path |
| `GOROUTER_DB_DRIVER` | `sqlite` | `sqlite` or `postgres` |
| `GOROUTER_DB_DSN` | — | Postgres connection string |
| `GOROUTER_KEY_SECRET` | (auto-generated) | HMAC secret for API key CRC |
| `GOROUTER_REQUIRE_KEY` | `true` | Require API key on `/v1/*` |
| `GOROUTER_DASHBOARD_TOKEN` | — | Fixed dashboard password (env-only) |
| `GOROUTER_UPSTREAM_TIMEOUT` | `600` | Non-streaming request timeout (seconds) |
| `GOROUTER_CACHE_ENABLED` | `false` | Enable response cache (LRU + TTL) |
| `GOROUTER_CACHE_TTL` | `5m` | Per-entry cache TTL |
| `GOROUTER_CACHE_MAX_ENTRIES` | `10000` | Cache entry limit (LRU eviction) |
| `GOROUTER_CACHE_MAX_HISTORY` | `0` | Skip caching conversations with more messages than this (0 = off) |
| `GOROUTER_RTK_ENABLED` | `false` | Enable RTK request compression |
| `GOROUTER_SEMANTIC_CACHE_ENABLED` | `false` | Enable semantic (vector) cache |
| `GOROUTER_SEMANTIC_CACHE_MODEL` | — | Embedding model for semantic cache |
| `GOROUTER_SEMANTIC_CACHE_THRESHOLD` | `0.95` | Cosine similarity threshold |
| `GOROUTER_REDIS_URL` | — | Redis URL for multi-instance (shared cache + health) |
| `GOROUTER_HOOK_MODERATION_PATTERNS` | — | Regex (CSV) for `keyword_moderation` hook |
| `GOROUTER_HOOK_WEBHOOK_URL` | — | Webhook URL for `webhook_logging` hook |

---

## Deployment

### Docker

```bash
docker run -p 20128:20128 ghcr.io/sintesy-me/gorouter:latest
```

### Docker Compose (with Postgres)

```bash
docker compose up -d
```

### Kubernetes

The image is standard — use a Deployment + Service + Secret. Manage Postgres with a StatefulSet or operator.

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: gorouter
spec:
  replicas: 1
  selector:
    matchLabels: { app: gorouter }
  template:
    metadata:
      labels: { app: gorouter }
    spec:
      containers:
        - name: gorouter
          image: ghcr.io/sintesy-me/gorouter:latest
          ports: [{ containerPort: 20128 }]
          env:
            - { name: GOROUTER_DB_DRIVER, value: postgres }
            - { name: GOROUTER_DB_DSN, valueFrom: { secretKeyRef: { name: gorouter-db, key: dsn } } }
```

### Bare Metal

```bash
# systemd service
[Unit]
Description=gorouter
After=network.target postgresql.service

[Service]
ExecStart=/usr/local/bin/gorouter
Environment=GOROUTER_DB_DRIVER=postgres
Environment=GOROUTER_DB_DSN=postgres://gorouter:secret@localhost:5432/gorouter
Restart=always
User=gorouter

[Install]
WantedBy=multi-user.target
```

---

## Development

```bash
# Terminal 1 — API
go run ./cmd/gorouter

# Terminal 2 — Frontend (hot reload, proxies to :20128)
cd internal/web && npm install && npm run dev
```

### Build with embedded frontend

```bash
cd internal/web && npm run build && cd ../..
go build -tags embed -o gorouter ./cmd/gorouter
```

### Tests

```bash
go test ./...
```

---

## Repository Structure

```text
gorouter/
├── cmd/gorouter/          # Composition root: wires adapters into services
├── internal/
│   ├── domain/            # Entities + ports (no I/O dependencies)
│   ├── app/               # Application services (router, combos, MCP, cache, hooks)
│   ├── infra/
│   │   ├── db/            # GORM repos (SQLite + Postgres)
│   │   ├── executor/      # HTTP reverse-proxy executor
│   │   ├── translator/    # Format translators (OpenAI ↔ Anthropic ↔ Gemini ↔ Responses)
│   │   ├── mcp/           # MCP gateway (dial, registry, sync, aggregated server)
│   │   ├── sse/           # SSE passthrough helpers
│   │   ├── rtk/           # Request token compression
│   │   ├── responsecache/ # Deterministic-hash response cache
│   │   ├── semanticcache/ # Vector-similarity cache
│   │   ├── redis/         # Multi-instance shared state
│   │   ├── apikey/        # API key generation/verification
│   │   └── metrics/       # Prometheus metrics
│   ├── interfaces/http/   # chi router + handlers (16 files)
│   ├── providers/         # Provider catalog/store (YAML presets) + OAuth + executors
│   └── web/               # Embedded React frontend (Vite + Tailwind + HeroUI)
├── providers/             # YAML provider templates (synced from GitHub)
├── docs/                  # Benchmark documentation
└── deploy.sh              # Internal deployment script
```

---

## Tech Stack

**Go core**
- [chi](https://github.com/go-chi/chi) — HTTP router
- [GORM](https://gorm.io) — ORM (SQLite + Postgres)
- [glebarez/sqlite](https://github.com/glebarez/sqlite) — Pure-Go SQLite driver
- [mark3labs/mcp-go](https://github.com/mark3labs/mcp-go) — MCP protocol client/server
- [google/uuid](https://github.com/google/uuid) — UUID generation
- [prometheus/client_golang](https://github.com/prometheus/client_golang) — Metrics

**Frontend**
- React 19 + TypeScript
- Vite
- Tailwind CSS v4
- HeroUI v3
- react-i18next (42 locales)
- @dnd-kit (drag-and-drop)

---

## License

MIT

---

## Inspired by

[9router](https://github.com/decolua/9router)