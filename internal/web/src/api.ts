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
  id: string; provider_id: string; name: string; api_key: string;
  base_url: string; format: string; auth: string; priority: number;
  is_active: boolean; rate_limited_until: string; created_at: string; updated_at: string;
}
export interface ModelInfo {
  id: string; object: string; owned_by: string; kind?: string;
}
export interface ModelEntry {
  id: string; provider_id: string; model_id: string; name: string;
  kind: string; source: string; is_active: boolean; context: number;
  supports_vision: boolean; supports_tool_call: boolean; supports_reasoning: boolean;
  last_synced_at: string; created_at: string; updated_at: string;
}
export interface Combo {
  id: string; name: string; models: string[]; strategy: string; kind?: string;
  created_at: string; updated_at: string;
}
export interface ApiKey {
  id: string; key: string; name: string; is_active: boolean; rate_limit_rpm: number; created_at: string;
}
export interface UsageStats {
  requests: number; prompt_tokens: number; completion_tokens: number; cost: number;
  by_provider: Record<string, number>; by_model: Record<string, number>;
  by_api_key: Record<string, number>;
  daily: { date: string; requests: number; tokens: number; cost: number }[];
}
export interface UsageEntry {
  timestamp: string; provider: string; model: string; combo_name?: string;
  connection_id: string; api_key: string; endpoint: string;
  prompt_tokens: number; completion_tokens: number; cached_tokens: number;
  cost: number; status: number; latency_ms?: number;
}

export const api = {
  auth: {
    status: () =>
      request<{ configured: boolean; authenticated: boolean }>("/api/auth/status"),
    setup: (password: string) =>
      request<{ status: string }>("/api/auth/setup", { method: "POST", body: JSON.stringify({ password }) }),
    login: (password: string) =>
      request<{ token: string }>("/api/auth/login", { method: "POST", body: JSON.stringify({ password }) }),
  },
  providers: {
    list: () => request<Provider[]>("/api/providers"),
    create: (p: Partial<Provider>) => request<Provider>("/api/providers", { method: "POST", body: JSON.stringify(p) }),
    update: (id: string, p: Partial<Provider>) => request<Provider>(`/api/providers/${id}`, { method: "PUT", body: JSON.stringify(p) }),
    remove: (id: string) => request<void>(`/api/providers/${id}`, { method: "DELETE" }),
    reorder: (ids: string[]) => request<void>("/api/providers/reorder", { method: "POST", body: JSON.stringify(ids) }),
    models: (id: string) => request<ModelEntry[]>(`/api/providers/${id}/models`),
    syncModels: (id: string) => request<ModelEntry[]>(`/api/providers/${id}/models/sync`, { method: "POST" }),
    addModel: (id: string, m: { model_id: string; name?: string; kind?: string; context?: number }) =>
      request<ModelEntry>(`/api/providers/${id}/models`, { method: "POST", body: JSON.stringify(m) }),
  },
  models: {
    list: () => request<ModelInfo[]>("/api/models"),
    update: (id: string, m: { is_active?: boolean; kind?: string; name?: string }) =>
      request<ModelEntry>(`/api/models/${id}`, { method: "PUT", body: JSON.stringify(m) }),
    remove: (id: string) => request<void>(`/api/models/${id}`, { method: "DELETE" }),
  },
  combos: {
    list: () => request<Combo[]>("/api/combos"),
    create: (c: Partial<Combo>) => request<Combo>("/api/combos", { method: "POST", body: JSON.stringify(c) }),
    update: (id: string, c: Partial<Combo>) => request<Combo>(`/api/combos/${id}`, { method: "PUT", body: JSON.stringify(c) }),
    remove: (id: string) => request<void>(`/api/combos/${id}`, { method: "DELETE" }),
  },
  keys: {
    list: () => request<ApiKey[]>("/api/keys"),
    create: (k: { name: string; rate_limit_rpm?: number }) => request<ApiKey>("/api/keys", { method: "POST", body: JSON.stringify(k) }),
    update: (id: string, k: { name?: string; is_active?: boolean; rate_limit_rpm?: number }) => request<ApiKey>(`/api/keys/${id}`, { method: "PUT", body: JSON.stringify(k) }),
    remove: (id: string) => request<void>(`/api/keys/${id}`, { method: "DELETE" }),
  },
  usage: {
    stats: (period = "24h") => request<UsageStats>(`/api/usage/stats?period=${period}`),
    history: (limit = 100) => request<UsageEntry[]>(`/api/usage/history?limit=${limit}`),
  },
};