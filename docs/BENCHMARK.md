# Benchmark: gorouter vs Bifrost vs LiteLLM

Resultados de um teste **wall-clock** ponta a ponta, na mesma máquina, contra o mesmo mock OpenAI-compatível. Medido em **2026-07-09**.

## Resumo

| Proxy | c=1 avg / RPS | c=10 avg / RPS | c=50 avg / RPS | Overhead ~ (c=1) |
|-------|---------------|----------------|----------------|------------------|
| **mock direto** | 0.2 ms / 6.2k | 0.4 ms / 25k | 1.6 ms / 31k | — |
| **gorouter** | **0.9 ms / 1.2k** | **1.9 ms / 5.3k** | **6.0 ms / 8.4k** | **~0.7 ms** |
| **Bifrost** | 1.7 ms / 597 | 2.4 ms / 4.2k | 7.5 ms / 6.6k | ~1.5 ms |
| **LiteLLM** | 18 ms / 55 | 208 ms / 48 | 905 ms / 54 | ~18 ms |

**Ranking (RPS em c=10):** gorouter ≈ Bifrost ≫ LiteLLM (~100× mais lento).

Em uso real de LLM (centenas de ms a dezenas de segundos), o overhead de gorouter e Bifrost é ruído. LiteLLM ainda fica perceptível sob carga concorrente.

---

## Objetivo

Comparar o **custo do proxy** (latência e throughput) no path de `POST /v1/chat/completions`, isolando o gateway do tempo do modelo real.

Não é um benchmark de qualidade de resposta, features, ou custo de tokens.

---

## Ambiente

| Item | Valor |
|------|--------|
| Host | Linux x86_64 (máquina local de desenvolvimento) |
| Rede | loopback `127.0.0.1` — sem Traefik, sem TLS, sem Docker Swarm |
| Carga | [`hey`](https://github.com/rakyll/hey) |
| Duração | `-z 8s` por cenário |
| Concorrência | `c=1`, `c=10`, `c=50` |
| Body | `{"model":"…","messages":[{"role":"user","content":"hi"}]}` |
| Warmup | 20 requests (`-n 20 -c 5`) por alvo antes da medição |

### Upstream (mock)

Servidor HTTP Go mínimo, concorrente, respondendo JSON fixo de chat completion:

- Bind: `127.0.0.1:19998`
- Path: qualquer `POST` → 200 + body OpenAI-compat
- Sem logging, sem auth, sem I/O de disco

Isso fixa o “chão” de latência do stack (cliente + SO + mock).

### Proxies sob teste

| Proxy | Como rodou | Porta | Config relevante |
|-------|------------|-------|------------------|
| **gorouter** | binário nativo (`go build ./cmd/gorouter`) | `20129` | SQLite, `REQUIRE_KEY=false`, provider mock → `http://127.0.0.1:19998`, model `mock/mock` |
| **Bifrost** | Docker `maximhq/bifrost:latest`, `--network host` | `8080` | provider custom `mock-local` (base OpenAI), `base_url=http://127.0.0.1:19998`, key `*`, concurrency 200; plugins default (logging, telemetry, governance, model-catalog) ativos |
| **LiteLLM** | `litellm --config` (uv tool / Python) | `4000` | `model_list` → `openai/mock` com `api_base=http://127.0.0.1:19998/v1` |

Modelos usados no request:

- mock: `mock`
- gorouter: `mock/mock`
- Bifrost: `mock-local/mock`
- LiteLLM: `mock`

---

## Metodologia

1. Subir o mock e validar `200` com `curl`.
2. Subir cada proxy e criar/configurar um provider apontando **só** para o mock.
3. Warmup de 20 requests em cada alvo.
4. Para cada `(alvo × concorrência)`:
   ```bash
   hey -z 8s -c <N> -m POST \
     -H "Content-Type: application/json" \
     -D body.json \
     http://127.0.0.1:<port>/v1/chat/completions
   ```
5. Registrar **Average**, **Requests/sec**, distribuição de status (todos os cenários acima: **100% HTTP 200**).
6. Overhead aproximado: `avg(proxy) − avg(mock)` no mesmo `c`.

### O que a métrica inclui

Wall-clock do ponto de vista do cliente:

```
hey → TCP/HTTP → proxy (auth/route/translate/forward/tee/log) → mock → resposta
```

Não é:

- tempo interno “só do núcleo” em µs (claims de marketing),
- latência de LLM real,
- path com TLS / reverse proxy / rede WAN.

### O que NÃO controlamos / caveats

| Caveat | Impacto |
|--------|---------|
| Bifrost em **Docker** (host network); gorouter e LiteLLM nativos | leve vantagem de setup para nativos |
| Bifrost com **plugins default ligados** | path de produção, não “modo microbench” |
| gorouter grava usage **async** em SQLite | contende sob c=50, mas não bloqueia o hot path de forma síncrona |
| LiteLLM em Python/Uvicorn | esperado ser bem mais lento em RPS |
| Uma máquina, um run de 8s | não é estatística multi-run / multi-host; ordens de grandeza são estáveis |
| Claims públicos Bifrost (~11 µs @ 5k RPS) usam **metodologia diferente** | não comparáveis 1:1 com este wall-clock |

---

## Resultados detalhados

### c = 1

| Alvo | Slowest | Fastest | Average | RPS | Status |
|------|---------|---------|---------|-----|--------|
| mock | 3.5 ms | 0.1 ms | **0.2 ms** | **6246** | 200 |
| gorouter | 7.7 ms | 0.3 ms | **0.9 ms** | **1167** | 200 |
| Bifrost | 9.4 ms | 0.7 ms | **1.7 ms** | **597** | 200 |
| LiteLLM | 188 ms | 10.5 ms | **18.2 ms** | **55** | 200 |

### c = 10

| Alvo | Slowest | Fastest | Average | RPS | Status |
|------|---------|---------|---------|-----|--------|
| mock | 5.8 ms | 0.1 ms | **0.4 ms** | **24638** | 200 |
| gorouter | 10.0 ms | 0.6 ms | **1.9 ms** | **5324** | 200 |
| Bifrost | 23.9 ms | 1.2 ms | **2.4 ms** | **4154** | 200 |
| LiteLLM | 300 ms | 80 ms | **208 ms** | **48** | 200 |

### c = 50

| Alvo | Slowest | Fastest | Average | RPS | Status |
|------|---------|---------|---------|-----|--------|
| mock | 30 ms | 0.1 ms | **1.6 ms** | **31169** | 200 |
| gorouter | 36 ms | 0.6 ms | **6.0 ms** | **8363** | 200 |
| Bifrost | 55 ms | 1.4 ms | **7.5 ms** | **6638** | 200 |
| LiteLLM | 3.2 s | 172 ms | **905 ms** | **54** | 200 |

---

## Interpretação

1. **gorouter e Bifrost** estão na mesma ordem de magnitude (~1–3 ms em c=1/10). Neste setup, gorouter ficou um pouco à frente.
2. **LiteLLM** satura cedo (~50 RPS) e a latência explode com concorrência — típico de proxy Python full-featured.
3. O “chão” do mock (~0.2–0.4 ms) mostra que a maior parte do tempo dos proxies Go é trabalho real de gateway + HTTP client, não só o mock.
4. Para chat real, latência do modelo domina; a escolha entre gorouter e Bifrost deve ser por **produto** (OAuth, combos, catalog, UI, semantic cache, MCP, etc.), não por µs.
5. Números de marketing em **µs** (overhead interno) e este teste em **ms wall-clock** respondem perguntas diferentes — ambos úteis, desde que não se misturem.

---

## Reproduzir

Pré-requisitos: Go, Docker (Bifrost), LiteLLM (`uv tool` / pip), [`hey`](https://github.com/rakyll/hey).

```bash
# 1) Mock
# (servidor HTTP Go que devolve chat.completion fixo na :19998)

# 2) gorouter
GOROUTER_HOME=/tmp/gr GOROUTER_PORT=20129 GOROUTER_REQUIRE_KEY=false \
  GOROUTER_DASHBOARD_TOKEN=bench ./gorouter
# POST /api/providers → base_url http://127.0.0.1:19998, format openai

# 3) Bifrost
docker run --rm --network host -v "$PWD/bf-data:/app/data" maximhq/bifrost:latest
# POST /api/providers (custom openai) + POST /api/providers/mock-local/keys

# 4) LiteLLM
# model_list: mock → openai/mock @ http://127.0.0.1:19998/v1
litellm --config litellm.yaml --port 4000 --host 127.0.0.1

# 5) Carga
hey -z 8s -c 10 -m POST -H "Content-Type: application/json" \
  -D body.json http://127.0.0.1:20129/v1/chat/completions
```

---

## Histórico

| Data | Nota |
|------|------|
| 2026-07-09 | Primeira rodada local: mock + gorouter + Bifrost + LiteLLM, `hey -z 8s`, c=1/10/50 |

Se rodar de novo em outra máquina, espere **valores absolutos** diferentes; a **ordem relativa** (Go ≫ Python) deve se manter.
