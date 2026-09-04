const BASE_URL = "https://auth.openai.com";
const API_BASE_URL = `${BASE_URL}/api/accounts`;
const CLIENT_ID = "app_EMoamEEZ73f0CkXaXp7hrann";
const DEVICE_AUTH_URL = `${BASE_URL}/codex/device`;
const DEVICE_REDIRECT_URI = `${BASE_URL}/deviceauth/callback`;
const DEFAULT_INTERVAL_SECONDS = 5;
const MAX_POLLS = 180;

type DeviceUserCode = {
  device_auth_id: string;
  user_code?: string;
  usercode?: string;
  interval?: number | string;
};

export type CodexDeviceTokens = {
  access_token: string;
  refresh_token?: string;
  id_token?: string;
  expires_in?: number;
};

export type CodexDeviceCode = {
  userCode: string;
  verificationUri: string;
};

type RunOptions = {
  onUserCode?: (code: CodexDeviceCode) => void;
  signal?: AbortSignal;
};

async function jsonRequest(url: string, init: RequestInit): Promise<{ response: Response; data: any }> {
  const response = await fetch(url, init);
  let data: any = null;
  try { data = await response.json(); } catch { /* handled below */ }
  return { response, data };
}

function intervalMs(raw: unknown): number {
  const n = typeof raw === "number" ? raw : Number.parseInt(String(raw ?? ""), 10);
  return Number.isFinite(n) && n > 0 ? n * 1000 : DEFAULT_INTERVAL_SECONDS * 1000;
}

function wait(ms: number, signal?: AbortSignal): Promise<void> {
  return new Promise((resolve, reject) => {
    const timer = window.setTimeout(resolve, ms);
    const abort = () => {
      window.clearTimeout(timer);
      reject(new Error("Device authorization cancelled"));
    };
    if (signal?.aborted) return abort();
    signal?.addEventListener("abort", abort, { once: true });
  });
}

function apiError(prefix: string, response: Response, data: any): Error {
  const detail = typeof data === "string" ? data : data?.error_description || data?.message || "";
  return new Error(`${prefix} (${response.status})${detail ? `: ${detail}` : ""}`);
}

/** Implements OpenAI's custom Codex deviceauth flow in the user's browser. */
export async function runCodexDeviceFlow(options: RunOptions = {}): Promise<CodexDeviceTokens> {
  const init = await jsonRequest(`${API_BASE_URL}/deviceauth/usercode`, {
    method: "POST",
    headers: { "Content-Type": "application/json", Accept: "application/json" },
    body: JSON.stringify({ client_id: CLIENT_ID }),
    signal: options.signal,
  });
  if (!init.response.ok) {
    throw apiError(init.response.status === 404 ? "Device login is not enabled for this account" : "Failed to start Codex device login", init.response, init.data);
  }
  const userCode = init.data?.user_code || init.data?.usercode;
  const deviceAuthId = init.data?.device_auth_id;
  if (!deviceAuthId || !userCode) throw new Error("OpenAI returned an incomplete device code");
  options.onUserCode?.({ userCode, verificationUri: DEVICE_AUTH_URL });

  const pollDelay = intervalMs(init.data?.interval);
  for (let poll = 0; poll < MAX_POLLS; poll++) {
    await wait(poll === 0 ? Math.min(pollDelay, 5000) : pollDelay, options.signal);
    const result = await jsonRequest(`${API_BASE_URL}/deviceauth/token`, {
      method: "POST",
      headers: { "Content-Type": "application/json", Accept: "application/json" },
      body: JSON.stringify({ device_auth_id: deviceAuthId, user_code: userCode }),
      signal: options.signal,
    });
    if (result.response.ok) {
      const authorizationCode = result.data?.authorization_code;
      const codeVerifier = result.data?.code_verifier;
      if (!authorizationCode || !codeVerifier) throw new Error("OpenAI returned an incomplete authorization code");
      const token = await jsonRequest(`${BASE_URL}/oauth/token`, {
        method: "POST",
        headers: { "Content-Type": "application/x-www-form-urlencoded", Accept: "application/json" },
        body: new URLSearchParams({
          grant_type: "authorization_code",
          client_id: CLIENT_ID,
          code: authorizationCode,
          code_verifier: codeVerifier,
          redirect_uri: DEVICE_REDIRECT_URI,
        }).toString(),
        signal: options.signal,
      });
      if (!token.response.ok) throw apiError("Failed to exchange Codex authorization code", token.response, token.data);
      if (!token.data?.access_token) throw new Error("OpenAI token response has no access token");
      return token.data as CodexDeviceTokens;
    }
    // OpenAI uses 403/404 while the user has not entered the code yet.
    if (result.response.status === 403 || result.response.status === 404) continue;
    throw apiError("Codex device authorization polling failed", result.response, result.data);
  }
  throw new Error("Codex device authorization timed out; start a new login");
}
