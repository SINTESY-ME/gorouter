// Thin fetch wrapper for the gorouter dashboard API. All responses are JSON.
// Throws on non-2xx with the server's error message when available.

const BASE = "";

// Dashboard auth token. The user enters a password via the Login/Setup page;
// on success the server confirms it and we stash it here for all subsequent
// /api/* calls. Empty = no auth (localhost trust).
const DASHBOARD_TOKEN_KEY = "gorouter_dashboard_token";
function dashboardToken(): string {
  try { return localStorage.getItem(DASHBOARD_TOKEN_KEY) ?? ""; } catch { return ""; }
}
export function setDashboardToken(t: string) {
  try { if (t) localStorage.setItem(DASHBOARD_TOKEN_KEY, t); else localStorage.removeItem(DASHBOARD_TOKEN_KEY); } catch {}
}
export function clearDashboardToken() {
  try { localStorage.removeItem(DASHBOARD_TOKEN_KEY); } catch {}
}
// On first load, check if the URL has ?dashboard_token= and stash it.
if (typeof window !== "undefined") {
  const params = new URLSearchParams(window.location.search);
  const t = params.get("dashboard_token");
  if (t) { setDashboardToken(t); }
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const token = dashboardToken();
  const headers: Record<string, string> = { "Content-Type": "application/json", ...(init?.headers as Record<string, string> || {}) };
  if (token && path.startsWith("/api/")) {
    headers["Authorization"] = `Bearer ${token}`;
  }
  const res = await fetch(BASE + path, { ...init, headers });
  const text = await res.text();
  let body: unknown = null;
  if (text) {
    try { body = JSON.parse(text); } catch { body = text; }
  }
  if (!res.ok) {
    const msg = (body as any)?.error?.message ?? (typeof body === "string" ? body : `HTTP ${res.status}`);
    throw new Error(msg);
  }
  return body as T;
}

export interface Provider {
  id: string;
  name: string;
  description: string;
  base_url: string;
  format: string;
  auth: string;
  load_balance: string;
  created_at?: string;
  updated_at?: string;
}

export interface Connection {
  id: string;
  provider_id: string;
  name: string;
  api_key: string;
  priority: number;
  is_active: boolean;
  rate_limited_until?: string;
  created_at?: string;
  updated_at?: string;
}
export interface ModelInfo {
  id: string; object: string; owned_by: string; kind?: string;
}
export interface ModelPricing {
  input_cost_per_token: number;
  output_cost_per_token: number;
  input_cost_per_token_batches?: number;
  output_cost_per_token_batches?: number;
  cache_read_input_token_cost?: number;
  cache_creation_input_token_cost?: number;
  input_cost_per_token_above_128k?: number;
  input_cost_per_token_above_200k?: number;
  output_cost_per_token_above_128k?: number;
  output_cost_per_token_above_200k?: number;
  output_cost_per_image?: number;
  input_cost_per_pixel?: number;
  input_cost_per_second?: number;
  output_cost_per_second?: number;
  input_cost_per_character?: number;
  output_cost_per_character?: number;
  input_cost_per_query?: number;
  source?: string;
  last_synced_at?: string;
}
export interface ModelEntry {
  id: string; provider_id: string; model_id: string; name: string;
  kind: string; source: string; is_active: boolean; context: number;
  supports_vision: boolean; supports_tool_call: boolean; supports_reasoning: boolean;
  pricing?: ModelPricing;
  last_synced_at: string; created_at: string; updated_at: string;
}
export interface ComboModelMeta {
  weight?: number;
  description?: string;
}
export interface Combo {
  id: string; name: string; models: string[]; strategy: string; kind?: string;
  model_meta?: Record<string, ComboModelMeta>;
  classifier_model?: string;
  mcp_clients?: string[];
  created_at: string; updated_at: string;
}
export interface KeyLimit {
  id: string;
  kind: "rate" | "budget";
  max: number;
  duration: string;
}
export interface ApiKey {
  id: string; key: string; name: string; is_active: boolean; limits: KeyLimit[]; allowed_models?: string[]; created_at: string;
}
export interface UserPermissions {
  can_manage_own_providers: boolean;
  can_create_combos: boolean;
  can_manage_cache: boolean;
  can_access_settings: boolean;
}
export interface User {
  id: string; name: string; email: string; role: "admin" | "member";
  permissions?: UserPermissions;
  allowed_models?: string[]; allowed_combos?: string[]; allowed_providers?: string[];
  api_keys_count: number; session_active: boolean;
  created_at: string; updated_at: string;
}
export interface UsageStats {
  requests: number; prompt_tokens: number; completion_tokens: number; cost: number;
  by_provider: Record<string, number>; by_model: Record<string, number>;
  by_model_cost: Record<string, number>;
  by_api_key: Record<string, number>;
  by_combo: Record<string, number>;
  by_combo_tokens: Record<string, number>;
  by_combo_cost: Record<string, number>;
  by_endpoint: Record<string, number>;
  daily: { date: string; requests: number; tokens: number; cost: number; errors?: number; avg_tps?: number }[];
  bucket?: string;
  // Performance
  avg_ttft_ms: number; avg_latency_ms: number; avg_tps: number;
  p50_latency_ms: number; p95_latency_ms: number; p99_latency_ms: number;
  // Reliability
  successful_requests: number; error_requests: number; error_rate: number;
  combo_requests: number;
  // Efficiency
  cache_hits: number; cache_hit_rate: number;
  tokens_saved: number; cost_saved: number;
  tokens_per_dollar: number; cost_per_request: number;
}
export interface UsageDailyPoint {
  date: string; requests: number; tokens: number; cost: number;
  errors?: number; avg_tps?: number;
}
export interface HealthSummary { unhealthy: number; probing: number; healthy: number; total_keys: number; }
export interface StatusSnapshot {
  combos: { total: number };
  connections: { total: number; active: number; rate_limited: number };
  health: HealthSummary;
}
export interface UsageEntry {
  id: number; timestamp: string; provider: string; model: string; combo_chain?: string[];
  connection_id: string; api_key: string; endpoint: string;
  prompt_tokens: number; completion_tokens: number; cached_tokens: number;
  cost: number; status: number; latency_ms?: number; ttft_ms?: number;
  cache_hit?: boolean; cache_tokens_saved?: number; cache_cost_saved?: number;
  rtk_compressed?: boolean; rtk_bytes_saved?: number; rtk_tokens_saved?: number; rtk_cost_saved?: number;
  request_id?: string; attempt?: number; error?: string;
}
export interface ProviderDef {
  id: string;
  display: { name: string; color?: string; website?: string; api_key_url?: string };
  category: string;
  aliases?: string[];
  priority?: number;
  transport: { base_url: string; format: string; auth: string; headers?: Record<string, string> };
  executor?: string;
  no_auth?: boolean;
  capabilities?: string[];
  installed?: boolean;
}
export interface ModelStat {
  avg_tps: number;
  avg_ttft_ms?: number;
  avg_latency_ms: number;
  requests: number;
}
export interface StoreEntry {
  id: string;
  name: string;
  category: string;
  color?: string;
  capabilities?: string[];
  installed: boolean;
}
export interface SavingsStats {
  cache_hits: number;
  cache_tokens_saved: number;
  cache_cost_saved: number;
  rtk_compressions: number;
  rtk_bytes_saved: number;
  rtk_tokens_saved: number;
  rtk_cost_saved: number;
  semantic_hits?: number;
  semantic_tokens_saved?: number;
  semantic_cost_saved?: number;
}

// MCP gateway types.
export interface MCPClient {
  id: string;
  name: string;
  connection_type: "http" | "sse" | "stdio";
  url?: string;
  headers?: Record<string, string>;
  stdio_command?: string;
  stdio_args?: string[];
  auth_type: "none" | "bearer";
  tools_to_execute?: string[];
  enabled: boolean;
  sync_seconds?: number;
  created_at?: string;
  updated_at?: string;
  state?: string;
  error?: string;
  tool_count?: number;
  last_sync_at?: string;
}
export interface MCPToolDef {
  name: string;
  description?: string;
  input_schema?: Record<string, any>;
  client_id?: string;
}


export const api = {
  auth: {
    status: () =>
      request<{ configured: boolean; authenticated: boolean }>("/api/auth/status"),
    me: () =>
      request<User>("/api/auth/me"),
    setup: (name: string, email: string, password: string) =>
      request<{ token: string }>("/api/auth/setup", { method: "POST", body: JSON.stringify({ name, email, password }) }),
    login: (email: string, password: string) =>
      request<{ token: string }>("/api/auth/login", { method: "POST", body: JSON.stringify({ email, password }) }),
    logout: () => request<void>("/api/auth/logout", { method: "POST" }),
  },
  providers: {
    list: () => request<Provider[]>("/api/providers"),
    create: (p: Partial<Provider> & { template_id?: string }) =>
      request<Provider>("/api/providers", { method: "POST", body: JSON.stringify(p) }),
    update: (id: string, p: Partial<Provider>) => request<Provider>(`/api/providers/${id}`, { method: "PUT", body: JSON.stringify(p) }),
    remove: (id: string) => request<void>(`/api/providers/${id}`, { method: "DELETE" }),
    models: (id: string) => request<ModelEntry[]>(`/api/providers/${id}/models`),
    syncModels: (id: string) => request<ModelEntry[]>(`/api/providers/${id}/models/sync`, { method: "POST" }),
    addModel: (id: string, m: { model_id: string; name?: string; kind?: string; context?: number }) =>
      request<ModelEntry>(`/api/providers/${id}/models`, { method: "POST", body: JSON.stringify(m) }),
    catalog: () => request<ProviderDef[]>("/api/provider-catalog"),
    catalogDetail: (id: string) => request<ProviderDef>(`/api/provider-catalog/${id}`),
    store: {
      list: () => request<StoreEntry[]>("/api/provider-store"),
      install: (id: string) => request<ProviderDef>(`/api/provider-store/install/${id}`, { method: "POST" }),
      remove: (id: string) => request<void>(`/api/provider-store/${id}`, { method: "DELETE" }),
    },
  },
  connections: {
    list: () => request<Connection[]>("/api/connections"),
    create: (c: Partial<Connection>) => request<Connection>("/api/connections", { method: "POST", body: JSON.stringify(c) }),
    update: (id: string, c: Partial<Connection>) => request<Connection>(`/api/connections/${id}`, { method: "PUT", body: JSON.stringify(c) }),
    remove: (id: string) => request<void>(`/api/connections/${id}`, { method: "DELETE" }),
    reorder: (ids: string[]) => request<void>("/api/connections/reorder", { method: "POST", body: JSON.stringify(ids) }),
  },
  oauth: {
    list: () => request<string[]>("/api/oauth/providers"),
    start: (provider: string, redirect_uri?: string) =>
      request<{ auth_url: string; state: string; redirect_uri: string; uses_pkce: boolean; paste_code: boolean }>(
        `/api/oauth/${provider}/start`,
        { method: "POST", body: JSON.stringify({ redirect_uri: redirect_uri || "" }) }
      ),
    complete: (provider: string, body: { state: string; code: string; name?: string }) =>
      request<Connection>(`/api/oauth/${provider}/complete`, { method: "POST", body: JSON.stringify(body) }),
  },
  models: {
    list: () => request<ModelInfo[]>("/api/models"),
    all: () => request<ModelEntry[]>("/api/models/all"),
    stats: () => request<Record<string, ModelStat>>("/api/models/stats"),
    update: (id: string, m: { is_active?: boolean; kind?: string; name?: string }) =>
      request<ModelEntry>(`/api/models/${id}`, { method: "PUT", body: JSON.stringify(m) }),
    remove: (id: string) => request<void>(`/api/models/${id}`, { method: "DELETE" }),
    pricing: (modelId: string, pricing: ModelPricing) =>
      request<ModelEntry>("/api/model-pricing", { method: "POST", body: JSON.stringify({ model_id: modelId, pricing }) }),
  },
  combos: {
    list: () => request<Combo[]>("/api/combos"),
    create: (c: Partial<Combo>) => request<Combo>("/api/combos", { method: "POST", body: JSON.stringify(c) }),
    update: (id: string, c: Partial<Combo>) => request<Combo>(`/api/combos/${id}`, { method: "PUT", body: JSON.stringify(c) }),
    remove: (id: string) => request<void>(`/api/combos/${id}`, { method: "DELETE" }),
  },
  keys: {
    list: () => request<ApiKey[]>("/api/keys"),
    create: (k: { name: string; limits?: KeyLimit[]; allowed_models?: string[] }) => request<ApiKey>("/api/keys", { method: "POST", body: JSON.stringify(k) }),
    update: (id: string, k: { name?: string; is_active?: boolean; limits?: KeyLimit[]; allowed_models?: string[] }) => request<ApiKey>(`/api/keys/${id}`, { method: "PUT", body: JSON.stringify(k) }),
    remove: (id: string) => request<void>(`/api/keys/${id}`, { method: "DELETE" }),
  },
  users: {
    list: () => request<User[]>("/api/users"),
    create: (u: { name: string; email: string; password: string; role: string; permissions?: UserPermissions }) =>
      request<User>("/api/users", { method: "POST", body: JSON.stringify(u) }),
    update: (id: string, u: { name?: string; email?: string; password?: string; role?: string; permissions?: UserPermissions }) =>
      request<User>(`/api/users/${id}`, { method: "PUT", body: JSON.stringify(u) }),
    remove: (id: string) => request<void>(`/api/users/${id}`, { method: "DELETE" }),
    setAccess: (id: string, kind: "provider" | "model" | "combo", ids: string[]) =>
      request<User>(`/api/users/${id}/access`, { method: "PUT", body: JSON.stringify({ kind, ids }) }),
  },
  usage: {
    stats: (params: { period?: string; from?: string; to?: string; bucket?: string; api_key_id?: string } | string = "24h") => {
      const q = typeof params === "string" ? { period: params } : params;
      const sp = new URLSearchParams();
      if (q.period) sp.set("period", q.period);
      if (q.from) sp.set("from", q.from);
      if (q.to) sp.set("to", q.to);
      if (q.bucket) sp.set("bucket", q.bucket);
      if (q.api_key_id) sp.set("api_key_id", q.api_key_id);
      return request<UsageStats>(`/api/usage/stats?${sp.toString()}`);
    },
    history: (params: { from?: string; to?: string; model?: string; combo?: string; api_key_id?: string; api_key?: string; search?: string; limit?: number; page?: number; per_page?: number } = {}) => {
      const sp = new URLSearchParams();
      if (params.from) sp.set("from", params.from);
      if (params.to) sp.set("to", params.to);
      if (params.model) sp.set("model", params.model);
      if (params.combo) sp.set("combo", params.combo);
      if (params.api_key_id) sp.set("api_key_id", params.api_key_id);
      if (params.api_key) sp.set("api_key", params.api_key);
      if (params.search) sp.set("search", params.search);
      if (params.page) sp.set("page", String(params.page));
      sp.set("per_page", String(params.per_page ?? 25));
      return request<{ data: UsageEntry[]; total: number; page: number; per_page: number; has_more: boolean }>(`/api/usage/history?${sp.toString()}`);
    },
    filters: (search?: string) => {
      const sp = new URLSearchParams();
      if (search) sp.set("search", search);
      return request<{ models: string[]; combos: string[]; providers: string[] }>(`/api/usage/filters${sp.toString() ? `?${sp.toString()}` : ""}`);
    },
  },
  settings: {
    get: () => request<{
      rtk_enabled: boolean; cache_enabled: boolean;
      semantic_cache_enabled: boolean; semantic_cache_mode: string; semantic_cache_model: string;
      hooks_enabled: string[]; caching_groups: Record<string, string[]>; webhook_url: string;
    }>("/api/settings"),
    update: (s: {
      rtk_enabled?: boolean; cache_enabled?: boolean;
      semantic_cache_enabled?: boolean; semantic_cache_mode?: string; semantic_cache_model?: string;
      hooks_enabled?: string[]; caching_groups?: Record<string, string[]>; webhook_url?: string;
    }) => request<{ status: string }>("/api/settings", { method: "PUT", body: JSON.stringify(s) }),
  },
  metrics: {
    fetch: () => fetch("/metrics").then((r) => (r.ok ? r.text() : Promise.reject(new Error(`HTTP ${r.status}`)))),
  },
  health: {
    ready: () => fetch("/health/readiness").then((r) => r.ok),
  },
  cache: {
    stats: () => request<{ enabled: boolean; entries?: number; hits?: number; misses?: number }>("/api/cache/stats"),
    flush: () => request<{ status: string }>("/api/cache/flush", { method: "POST" }),
  },
  semanticCache: {
    stats: () => request<{ enabled: boolean; mode?: string; entries?: number; hits?: number; misses?: number }>("/api/semantic-cache/stats"),
    flush: () => request<{ status: string }>("/api/semantic-cache/flush", { method: "POST" }),
  },
  savings: {
    stats: (period = "60d", apiKeyId = "") => request<SavingsStats>(`/api/savings?period=${period}${apiKeyId ? `&api_key_id=${apiKeyId}` : ""}`),
  },
  mcpClients: {
    list: () => request<MCPClient[]>("/api/mcp/clients"),
    create: (c: Partial<MCPClient>) => request<MCPClient>("/api/mcp/clients", { method: "POST", body: JSON.stringify(c) }),
    update: (id: string, c: Partial<MCPClient>) => request<MCPClient>(`/api/mcp/clients/${id}`, { method: "PUT", body: JSON.stringify(c) }),
    remove: (id: string) => request<void>(`/api/mcp/clients/${id}`, { method: "DELETE" }),
    reconnect: (id: string) => request<{ status: string }>(`/api/mcp/clients/${id}/reconnect`, { method: "POST" }),
    enable: (id: string) => request<{ status: string }>(`/api/mcp/clients/${id}/enable`, { method: "POST" }),
    disable: (id: string) => request<{ status: string }>(`/api/mcp/clients/${id}/disable`, { method: "POST" }),
    tools: () => request<MCPToolDef[]>("/api/mcp/tools"),
  },
  status: () => request<StatusSnapshot>("/api/status"),
};

// ---- Chat playground (streaming) ----

export interface ChatMessage {
  role: "system" | "user" | "assistant";
  content: string;
}

export interface ChatStreamChunk {
  delta: string;
  done: boolean;
  usage?: {
    prompt_tokens: number;
    completion_tokens: number;
    total_tokens: number;
  };
  model?: string;
}

// streamChat sends a chat completion request to /v1/chat/completions with
// stream:true and yields parsed SSE chunks as they arrive. The caller
// provides an onChunk callback that receives each incremental delta. Returns
// the final chunk (which carries usage stats) or throws on HTTP error.
export async function streamChat(
  messages: ChatMessage[],
  model: string,
  onChunk: (chunk: ChatStreamChunk) => void,
  signal?: AbortSignal,
): Promise<ChatStreamChunk> {
  const token = dashboardToken();
  const headers: Record<string, string> = { "Content-Type": "application/json" };
  if (token) headers["Authorization"] = `Bearer ${token}`;

  const res = await fetch("/v1/chat/completions", {
    method: "POST",
    headers,
    body: JSON.stringify({
      model,
      messages,
      stream: true,
      stream_options: { include_usage: true },
    }),
    signal,
  });

  if (!res.ok) {
    const text = await res.text();
    let msg = `HTTP ${res.status}`;
    try { const j = JSON.parse(text); msg = j?.error?.message ?? msg; } catch {}
    throw new Error(msg);
  }

  const reader = res.body!.getReader();
  const decoder = new TextDecoder();
  let buffer = "";
  let lastChunk: ChatStreamChunk = { delta: "", done: false };

  for (;;) {
    const { done, value } = await reader.read();
    if (done) break;
    buffer += decoder.decode(value, { stream: true });

    // SSE events are separated by "\n\n"; each event has "data: ..." lines.
    const events = buffer.split("\n\n");
    buffer = events.pop() ?? "";

    for (const evt of events) {
      const line = evt.trim();
      if (!line.startsWith("data: ")) continue;
      const data = line.slice(6);
      if (data === "[DONE]") {
        lastChunk.done = true;
        onChunk(lastChunk);
        return lastChunk;
      }
      try {
        const json = JSON.parse(data);
        const delta = json?.choices?.[0]?.delta?.content ?? "";
        const usage = json?.usage;
        const model_ = json?.model;
        const chunk: ChatStreamChunk = {
          delta,
          done: false,
          usage: usage ? {
            prompt_tokens: usage.prompt_tokens ?? 0,
            completion_tokens: usage.completion_tokens ?? 0,
            total_tokens: usage.total_tokens ?? 0,
          } : undefined,
          model: model_,
        };
        if (usage) lastChunk.usage = chunk.usage;
        if (model_) lastChunk.model = model_;
        onChunk(chunk);
      } catch { /* skip malformed */ }
    }
  }

  lastChunk.done = true;
  onChunk(lastChunk);
  return lastChunk;
}