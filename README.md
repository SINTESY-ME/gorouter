# gorouter

**O proxy LLM mais rápido que existe. Um binário. Zero dependências. Overhead imperceptível.**

gorouter é um proxy focado em **performance** e **autonomia**, projetado para fazer bem aquilo que um proxy é realmente útil. Tem o **menor overhead de todos os proxies**, com a **segurança e estabilidade** que um proxy precisa ter para suportar grandes aplicações e fluxos.

Expõe uma API compatível com OpenAI (`/v1/*`) e roteia requests entre múltiplos providers com **combos** — meta-modelos apoiados em listas ordenadas de fallback.

Um único binário estático. Sem runtime, sem VM, sem interpretador. O frontend fica embutido via `go:embed`. Roda em qualquer lugar: laptop, servidor, container, edge.

---

## Por que gorouter

| | gorouter | Bifrost | Portkey | LiteLLM | OmniRoute |
|---|---|---|---|---|---|
| **Runtime** | Go estático | Go (Docker) | Node (Docker) | Python | Node (Docker) |
| **Latência média (c=50)** | **6.3 ms** | 11.6 ms | 209 ms | 1.15 s | ~10 s |
| **RPS (c=50)** | **~7.9k** | ~4.3k | ~238 | ~43 | ~2.6 |
| **Overhead vs mock (c=50)** | **~4.85 ms** | ~10.15 ms | ~207 ms | ~1.15 s | ~10 s |
| **Health tracking** | ✅ | ✅ | — | parcial | — |

> Benchmark local, HTTP, mock OpenAI-compat, `hey -z 10s`, c=1/10/50 (mock intercalado). Metodologia e números completos: **[docs/BENCHMARK.md](docs/BENCHMARK.md)**.

> **~1.8× mais rápido que Bifrost**, **~33× que Portkey**, **~182× que LiteLLM** e **~1600× que OmniRoute**.

---

## Principais recursos

### Proxy multi-formato
Fale com qualquer provider usando o mesmo client OpenAI. O gorouter detecta o formato e traduz para **Anthropic**, **Gemini nativo**, ou **OpenAI Responses API** — tudo transparente.

```
Você:     POST /v1/chat/completions
OpenAI:   POST /v1/chat/completions         ← sem tradução
Anthropic: POST /v1/messages                ← traduzido
Gemini:    POST /v1beta/models/{model}:generateContent  ← traduzido
```

### Metamodelos (Combos)
Combos são **modelos virtuais** que agrupam múltiplos modelos reais sob um único nome. Quando você faz uma request usando o nome do combo, o gorouter roteia automaticamente entre os modelos reais usando a estratégia configurada.

```json
{
  "name": "smart",
  "models": ["openai/gpt-4o", "anthropic/claude-3-5-sonnet", "google/gemini-1.5-pro"],
  "strategy": "ordered_fallback"
}
```

Depois de criar o combo, use `"model": "smart"` em qualquer request — o gorouter cuida do resto. Combos aparecem em `/v1/models` com `owned_by: "combo"`.

### Estratégias de fallback

**`ordered_fallback`** (padrão) — Tenta os modelos na ordem em que foram definidos. Se o primeiro falhar, tenta o segundo, e assim por diante. Ideal para cenários onde você tem um modelo preferido e quer fallbacks em cascata.

**`round-robin`** — Rotaciona o modelo inicial a cada request, distribuindo carga entre todos os modelos do combo. Modelos unhealthy são pulados automaticamente. Ideal para balancear custo ou evitar rate limits.

### Como o fallback funciona

O gorouter decide se deve tentar o próximo modelo com base no tipo de erro:

| Condição | Fallback? | Motivo |
|---|---|---|
| Erro de rede / timeout | ✅ Sim | Falha transitória de infraestrutura |
| 5xx (500-599) | ✅ Sim | Erro upstream transitório |
| 429 (Too Many Requests) | ✅ Sim | Rate limited |
| 408 (Request Timeout) | ✅ Sim | Timeout / indisponível |
| 401 (Unauthorized) | ✅ Sim | Tenta outra conta |
| 403 (Forbidden) | ✅ Sim | Tenta outra conta |
| 404 (Not Found) | ✅ Sim | Modelo removido/deprecado neste provider; o próximo pode ter |
| 400, 422, 415 | ❌ Não | Erro do client, falhará em todos |

### Health tracking integrado

Modelos que falham são marcados como **unhealthy** e pulados em requests subsequentes. Probes de background rodam em paralelo (timeout de 20s, request mínimo) para detectar quando voltam a funcionar — sem downtime manual.

Se todos os modelos do combo estiverem unhealthy, o gorouter tenta todos inline novamente (last-resort pass). Se algum funcionar, é marcado healthy imediatamente.

### Context-window aware routing

O gorouter **estima os tokens do prompt** e **pula modelos do combo cuja context window não comporta** o request — um prompt de ~10k tokens não tenta um modelo de janela 4k (que daria 400 e queimaria o fallback). Requer dados de context window do sync de catálogo; sem dados, nada é filtrado (fail-open).

### Connection-level fallback

Dentro de cada modelo, múltiplas conexões (contas) para o mesmo provider são tentadas em round-robin. Conexões que falham com 429/5xx são temporariamente pausadas (respeitando o header `Retry-After` ou 5s por padrão).

### Combos multimodais

Combos funcionam com **todos os tipos de modelo** — não só LLM. Crie um combo `image-gen` que tenta DALL-E 3, depois Stable Diffusion, depois Midjourney. Ou um combo `embeddings` que tenta OpenAI, depois Cohere. O fallback é automático e transparente para o client.

### Response cache (direct-hash)

Cache de respostas por hash determinístico do request. Requests idênticos recebem a resposta cacheada **sem chamar o provider** — zero latência upstream, zero custo de token.

- **LRU + TTL**: limite de entries (default 10000) com eviction LRU + TTL por entry (default 5min) + sweep de background
- **Normalização determinística**: campos efêmeros (`user`, `request_id`, etc.) são removidos e chaves JSON ordenadas antes do hash — mesmo request com ordem de campos diferente = cache hit. Campos tipo-set (`tools`, `stop`, `modalities`) são ordenados antes do hash (ordem irrelevante), e `n: 1` (default) é normalizado.
- **Stream + non-stream**: ambos suportados; streams são acumulados e replayados verbatim
- **Bypass per-request**: header `x-gr-cache: off` desabilita cache para uma request específica
- **TTL por request**: header `x-gr-cache-ttl: <segundos>` define um TTL mais curto para a entry gravada (a chave não muda — controle por header, nunca no hash)
- **Caching groups**: via `PUT /api/settings`, modelos intercambiáveis compartilham a mesma entry — `{"caching_groups": {"gpt-family": ["gpt-4o", "gpt-4o-mini"]}}`
- **Guard de histórico**: `GOROUTER_CACHE_MAX_HISTORY` (ex. `50`) pula o cache de conversas com mais mensagens que o limite (evita lixo)
- **Observabilidade**: header `x-gr-cache-hit: true` na response; `GET /api/cache/stats` mostra entries/hits/misses; `POST /api/cache/flush` limpa tudo

```bash
# Ativar (env)
GOROUTER_CACHE_ENABLED=true
GOROUTER_CACHE_TTL=5m
GOROUTER_CACHE_MAX_ENTRIES=10000

# Bypass per-request
curl -H "x-gr-cache: off" -d '{"model":"...","messages":[...]}' http://localhost:20128/v1/chat/completions
```

Benchmark: cache hit é **~3x mais rápido** que miss (14.7k vs 5.3k RPS em c=10, mock local).

### Performance obsessiva
O caminho quente (hot path) foi desenhado para ter **overhead mínimo** (~1 ms wall-clock no [benchmark local](docs/BENCHMARK.md)):

- **Caches de hot path**: API keys e conexões ficam em memória com TTL de 30s e RWMutex — sem hits no DB durante requisições
- **Usage assíncrono**: métricas de uso vão para um canal bufferizado (4096) e são persistidas em background — o request nunca espera
- **Parse único**: o body JSON é unmashaled uma única vez no handler, model e stream flag extraídos e passados para o resto do pipeline
- **Connection pooling**: `http.Transport` com 200 idle conns, 50 por host — reutilização máxima
- **Streaming sem buffer**: SSE piped via `io.Copy` direto do provider para o client — zero cópias intermediárias
- **Timeout inteligente**: streams não têm timeout fixo (podem durar minutos), requests síncronos têm por-request timeout via child client
- **Logs só em erro** no access log — sucesso não polui stdout; a aba Logs do dashboard usa o usage async no DB

### Suporte multimodal completo
Não é só texto. O gorouter roteia **todos os tipos de modelo** via combos com fallback:

- **LLM** — chat completions, streaming, vision
- **Embeddings** — vetores para RAG, busca semântica
- **Images** — geração de imagens (DALL-E, Midjourney, Stable Diffusion)
- **Audio** — TTS (text-to-speech) e STT (speech-to-text, Whisper)
- **Rerank** — reordenação de documentos
- **OCR** — extração de texto de imagens
- **Video** — geração e processamento de vídeo

Cada tipo tem seu próprio endpoint (`/v1/chat/completions`, `/v1/embeddings`, `/v1/images/generations`, `/v1/audio/speech`, etc.) e todos funcionam com combos. Crie um combo `image-gen` que tenta DALL-E 3, depois Stable Diffusion, depois Midjourney — o fallback é automático.

### Catálogo de modelos enriquecido
O gorouter sincroniza o catálogo automaticamente com 3 APIs externas (LiteLLM, models.dev, OpenRouter) a cada 2h para descobrir o *kind* dos modelos, context window, e capacidades.

### Pricing automático
O gorouter resolve o preço de cada modelo automaticamente durante o sync, em cascata: **LiteLLM → models.dev → OpenRouter**, com **fuzzy matching** como fallback:

- **Match exato** (provider + model): lookup direto no registry
- **Match por nome**: modelo sem provider prefix
- **Fuzzy matching**: 3 estratégias quando o modelo não existe no registry:
  - **Safe suffix strip**: remove sufixos como `-free`, `-latest`, `-preview`, `-alpha`, `-beta`
  - **Containment**: substring mais longa contida no nome (ex: `0g-glm-5.2` → `glm-5.2`)
  - **Levenshtein**: distância adaptativa para typos e variantes
- **Modelos free ($0)**: modelos com cost=0 em qualquer fonte são aceitos como pricing válido (source set, cost=0) — o dashboard mostra "Free" em vez de "sem preço"
- **Best-wins**: se uma fonte tem pricing data e outra não, a com data ganha (não first-wins cego)
- **Zero overhead no hot path**: pricing é resolvido uma vez no sync e guardado em cache em memória; o hot path faz apenas `RLock + map[string]` lookup (nanosegundos)

```bash
# Override manual de preço (dashboard ou API)
POST /api/model-pricing
```

### RTK token compression
Compressão de requests para reduzir tokens enviados ao upstream. 11 filtros automáticos (gitDiff, gitLog, grep, find, ls, tree, buildOutput, dedupLog, readNumbered, smartTruncate, searchList) com auto-detecção pelo primeiro 1KB. Fail-open: se algo falha, a request original vai intacta.

```bash
# Ativar (env)
GOROUTER_RTK_ENABLED=true

# Toggle live via dashboard (aba Performance)
```

### Savings tracker
Métricas em tempo real de economia: tokens poupados por cache hits e bytes poupados por compressão RTK. Contadores atômicos in-memory (resetam no restart).

```bash
GET /api/savings
# { "cache_hit_tokens": 8200000, "rtk_bytes_saved": 1500000, "rtk_tokens_saved": 375000 }
```

### Segurança
- **API keys** rotacionáveis com rate limit por chave (token bucket)
- **Acesso por modelo** — cada key pode ser restrita a modelos/combos específicos (`allowed_models`, no dashboard): request a modelo fora da lista → 403
- **Dashboard auth** com password definida na primeira abertura ou via env var
- **Rate limiting** upstream automático — conexões que falham com 429/5xx são temporariamente pausadas
- **Secrets via Docker Swarm secrets** ou env vars

### OAuth (Codex + Gemini CLI)
Tokens OAuth para providers que suportam PKCE. Refresh automático antes do upstream call. Fluxo paste-code no dashboard.

### Dashboard embutido
Interface React + Vite + Tailwind + HeroUI compilada e embutida via `go:embed`. Gerencie providers, combos, keys, modelos, e visualize uso e analytics em tempo real — sem precisar rodar um frontend separado.

**Abas:**
- **Dashboard** — stats, cost chart, savings section (cache + RTK), pie charts por provider/model
- **Models** — cards com preço, kind, stats, botões de ação (editar preço, ativar/desativar, excluir); clique no nome para copiar
- **Performance** — toggles live para RTK e cache, cache stats (entries/hits/misses/hit rate), flush button
- **Logs** — requests com cost e tokens
- **Combos** — criação com searchable model selector (Autocomplete com fuzzy search)

---

## Quick start

### Docker

```bash
docker build -t gorouter .
docker run -p 20128:20128 gorouter
```

### Docker Compose (com Postgres)

```bash
docker compose up -d
```

### Docker Swarm

```bash
./deploy.sh
```

O script inicializa o Swarm se necessário, cria secrets, builda a imagem, e faz deploy do stack.

---

## Arquitetura

```
┌──────────────────────────────────────────────────────────────┐
│                      interfaces/http                          │
│   chi router  │  /v1/* handlers  │  /api/* handlers  │ SPA   │
└────────────────────────┬─────────────────────────────────────┘
                         │
┌────────────────────────▼─────────────────────────────────────┐
│                        application                            │
│     RouterService  │  ComboService  │  UsageService  │  Auth  │
└────────────────────────┬─────────────────────────────────────┘
                         │  (ports / interfaces)
┌────────────────────────▼─────────────────────────────────────┐
│                       infrastructure                          │
│  executor  │  translator  │  sse  │  GORM repos  │  cache    │
└──────────────────────────────────────────────────────────────┘
                         │
              ┌──────────┼──────────┐
              │          │          │
         OpenAI     Anthropic    Gemini
         (qualquer    Messages    generateContent
         /v1/*)
```

---

## Configuração

Todas variáveis são opcionais:

| Variável | Default | Descrição |
|---|---|---|
| `GOROUTER_PORT` | `20128` | Porta HTTP |
| `GOROUTER_HOME` | `~/.gorouter` | Diretório de dados |
| `GOROUTER_DB` | `<home>/gorouter.db` | Caminho do SQLite |
| `GOROUTER_DB_DRIVER` | `sqlite` | `sqlite` ou `postgres` |
| `GOROUTER_DB_DSN` | — | Connection string Postgres |
| `GOROUTER_KEY_SECRET` | (gerado primeiro run) | Secret HMAC para API keys |
| `GOROUTER_REQUIRE_KEY` | `true` | Exigir API key em `/v1/*` |
| `GOROUTER_DASHBOARD_TOKEN` | — | Senha fixa do dashboard (env-only) |
| `GOROUTER_UPSTREAM_TIMEOUT` | `600` | Timeout de requests não-streaming (segundos) |
| `GOROUTER_CACHE_ENABLED` | `false` | Ativar response cache (direct-hash LRU + TTL) |
| `GOROUTER_CACHE_TTL` | `5m` | TTL por entry do cache |
| `GOROUTER_CACHE_MAX_ENTRIES` | `10000` | Limite de entries no cache (eviction LRU) |
| `GOROUTER_CACHE_MAX_HISTORY` | `0` | Pula cache de conversas com mais mensagens que isso (0 = off) |
| `GOROUTER_RTK_ENABLED` | `false` | Ativar compressão RTK de requests |
| `GOROUTER_HOOK_MODERATION_PATTERNS` | — | Regex (CSV) para o hook `keyword_moderation` |
| `GOROUTER_HOOK_WEBHOOK_URL` | — | URL do hook `webhook_logging` (eventos de request) |

---

## Hooks e Observabilidade

O gorouter tem um **pipeline de hooks** (`PreCall` / `PostCall` / `PostCallFailure`) espelhado no `CustomLogger` do LiteLLM — moderação, logging e métricas são plugins, sem tocar no router. Hooks ativos são configurados via `GET/PUT /api/settings` (`hooks_enabled`, JSON array), com efeito **live**:

```bash
curl -X PUT localhost:20128/api/settings \
  -H 'Content-Type: application/json' \
  -d '{"hooks_enabled":["keyword_moderation","prompt_injection_heuristic","request_logging","prometheus"]}'
```

| Hook | Ponto | Efeito |
|---|---|---|
| `keyword_moderation` | PreCall | Rejeita (400) mensagens que casam com `GOROUTER_HOOK_MODERATION_PATTERNS` |
| `prompt_injection_heuristic` | PreCall | Rejeita (400) padrões comuns de prompt injection |
| `request_logging` | PostCall / Failure | Log estruturado (slog) de sucesso/falha por request |
| `prometheus` | PostCall / Failure | Alimenta métricas em `/metrics` |
| `webhook_logging` | PostCall / Failure | Envia eventos de request para uma URL HTTP (`GOROUTER_HOOK_WEBHOOK_URL`) — Slack, Datadog, custom |

Sem hooks ativos o hot path não muda: `Router.Hooks` fica nil e cada ponto de hook é pulado com **zero alocação** (1 branch).

### Webhook de observabilidade

Com `webhook_logging` habilitado e `GOROUTER_HOOK_WEBHOOK_URL` definido, cada request gera um `POST` assíncrono com o evento (fail-open — webhook fora do ar nunca afeta o request):

```bash
GOROUTER_HOOK_WEBHOOK_URL=https://hooks.slack.com/services/... \
curl -X PUT localhost:20128/api/settings -d '{"hooks_enabled":["webhook_logging"]}'
```

```json
{ "event": "request.completed", "request_id": "...", "model": "gpt-4o",
  "provider": "openai", "status": 200, "prompt_tokens": 10, "completion_tokens": 5,
  "cost": 0.0012, "latency_ms": 500, "timestamp": "..." }
```

### Prometheus

`GET /metrics` expõe métricas em formato Prometheus (text exposition), zero dependências:

- **Request path** (hook `prometheus`): `gorouter_requests_total`, `gorouter_failed_requests_total`, `gorouter_request_duration_seconds` (histograma), `gorouter_request_ttft_seconds` (histograma), `gorouter_tokens_input_total`, `gorouter_tokens_output_total`, `gorouter_cache_tokens_{read,creation}_total`, `gorouter_spend_usd_total` — labels por model/endpoint/status.
- **State gauges** (sempre presentes, do estado existente): `gorouter_health_*`, `gorouter_cache_*`, `gorouter_semantic_cache_*`, `gorouter_savings_*`, `gorouter_uptime_seconds`.

```bash
curl localhost:20128/metrics | head
```

Os contadores do request path cobrem chat (sucesso via `PostCall`, falha via `PostCallFailure`); passthrough (embeddings/imagens/áudio) entra apenas nas falhas. Séries por métrica são limitadas (cap default 500) para conter cardinalidade de labels.

---

## API

### `/v1/*` — API compatível com OpenAI

| Endpoint | Descrição |
|---|---|
| `GET /v1/models` | Lista modelos disponíveis |
| `POST /v1/chat/completions` | Chat completion (streaming ou não) |
| `POST /v1/completions` | Completion (alias) |
| `POST /v1/embeddings` | Embeddings |
| `POST /v1/images/generations` | Geração de imagens |
| `POST /v1/audio/speech` | TTS |
| `POST /v1/audio/transcriptions` | STT |
| `POST /v1/responses` | OpenAI Responses API |

### Health probes (públicos, sem auth)

| Endpoint | Descrição |
|---|---|
| `GET /health/liveliness` | 200 se o processo está vivo (K8s liveness) |
| `GET /health/readiness` | 200 se pronto (DB ok + provider configurado); 503 caso contrário |
| `GET /health` | 200 + `{status, uptime_seconds}` |

### Headers de controle e debug

| Header | Direção | Descrição |
|---|---|---|
| `x-gr-cache: off` | request | Bypass do cache na request |
| `x-gr-cache-ttl: <seg>` | request | TTL da entry cacheada (sobrescreve o global) |
| `x-gr-timeout: <seg>` | request | Timeout upstream da request não-streaming |
| `x-gr-cache-hit: true` | response | A resposta veio do cache |
| `x-gr-model` | response | Modelo real que serviu (útil em combo) |
| `x-gr-attempted-retries` | response | Quantos fallbacks/retries aconteceram antes de responder |

### Uso

```bash
# Crie uma API key no dashboard, depois:
curl http://localhost:20128/v1/chat/completions \
  -H "Authorization: Bearer <sua-api-key>" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "smart",
    "messages": [{"role": "user", "content": "Olá!"}],
    "stream": true
  }'
```

### Dashboard API (`/api/*`)

Protegida por senha. Endpoints para CRUD de providers, combos, keys, modelos, e consultas de uso/analytics.

| Endpoint | Descrição |
|---|---|
| `GET /api/savings` | Savings (cache hit tokens + RTK bytes saved) |
| `GET /api/cache/stats` | Cache stats (entries, hits, misses, hit rate) |
| `POST /api/cache/flush` | Limpa cache |
| `GET /api/settings` | Settings (RTK, cache, semantic cache, hooks) |
| `PUT /api/settings` | Atualiza settings (toggle live RTK, cache, hooks) |
| `POST /api/model-pricing` | Override manual de preço de um modelo |

---

## Deploy

### Produção

Recomendado com Postgres para clusters:

```bash
# Docker Swarm
./deploy.sh meu-stack

# Manual
docker secret create postgres_password ./postgres_password.txt
docker secret create key_secret ./key_secret.txt
docker stack deploy -c docker-stack.yml gorouter
```

### Kubernetes

A imagem é padrão — use um Deployment + Service + Secret. Postgres gerencie com um StatefulSet ou operador.

### Bare metal

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

## Desenvolvimento

```bash
# Terminal 1 — API
go run ./cmd/gorouter

# Terminal 2 — Frontend (hot reload, proxy para :20128)
cd internal/web && npm install && npm run dev
```

### Build com frontend embutido

```bash
cd internal/web && npm run build && cd ../..
go build -tags embed -o gorouter ./cmd/gorouter
```

### Testes

```bash
go test ./...
cd internal/web && npm test  # se houver
```

---

## Stack

**Go core**
- [chi](https://github.com/go-chi/chi) — router HTTP
- [GORM](https://gorm.io) — ORM (SQLite + Postgres)
- [glebarez/sqlite](https://github.com/glebarez/sqlite) — driver SQLite pure-Go
- [google/uuid](https://github.com/google/uuid) — UUIDs

**Frontend**
- React 18 + TypeScript
- Vite
- Tailwind CSS
- HeroUI
- Recharts (dashboard)

---

## Licença

MIT

---

## Inspirado por

[9router](https://github.com/decolua/9router)
