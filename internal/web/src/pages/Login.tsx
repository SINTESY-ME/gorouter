import { useState } from "react";
import { api, setDashboardToken, clearDashboardToken } from "../api";

// Login is the dashboard login page. The user enters the password set
// during first-run setup (or the GOROUTER_DASHBOARD_TOKEN env value). On
// success the token is stored and the dashboard mounts.
export default function Login({ onDone }: { onDone: () => void }) {
  const [pw, setPw] = useState("");
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setErr("");
    setBusy(true);
    try {
      const res = await api.auth.login(pw);
      setDashboardToken(res.token);
      onDone();
    } catch (e: any) {
      const msg = e?.message ?? "Falha ao entrar.";
      setErr(/401|invalid/i.test(msg) ? "Senha incorreta." : msg);
      clearDashboardToken();
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="min-h-screen bg-default-50 flex items-center justify-center px-4">
      <div className="w-full max-w-sm">
        <div className="flex items-center gap-3 mb-8 justify-center">
          <IconRoute className="w-6 h-6 text-primary" />
          <div>
            <p className="font-bold text-lg leading-tight">gorouter</p>
            <p className="text-xs text-default-500 leading-tight">LLM router</p>
          </div>
        </div>
        <div className="bg-content1 rounded-xl border border-default-100 p-6 space-y-4">
          <h2 className="text-lg font-semibold">Entrar</h2>
          <form onSubmit={submit} className="space-y-3">
            <input
              type="password"
              placeholder="Senha"
              value={pw}
              onChange={(e) => setPw(e.target.value)}
              autoFocus
              disabled={busy}
              className="w-full rounded-lg border border-default-200 bg-default-50 px-3 py-2 text-sm outline-none focus:border-primary"
            />
            {err && <p className="text-sm text-danger">{err}</p>}
            <button
              type="submit"
              disabled={busy}
              className="w-full rounded-lg bg-primary text-white py-2 text-sm font-medium hover:opacity-90 disabled:opacity-50"
            >
              {busy ? "Entrando..." : "Entrar"}
            </button>
          </form>
        </div>
      </div>
    </div>
  );
}

function IconRoute({ className }: { className?: string }) {
  return (
    <svg className={className} viewBox="0 0 14 20" fill="currentColor" xmlns="http://www.w3.org/2000/svg" fillRule="evenodd">
      <path d="M10.008,12.17 C10.008,13.275 9.108,14.17 8,14.17 C6.89,14.17 5.992,13.275 5.992,12.17 C5.992,11.065 6.89,10.17 8,10.17 C9.108,10.17 10.008,11.065 10.008,12.17 M7.973,18.005 C5.39,18.005 3.035,16.295 2.239,13.848 C1.446,11.41 2.358,8.739 4.344,7.227 C4.894,6.808 5.095,6.113 5.005,5.428 C4.781,3.732 6.099,2 7.973,2 C9.846,2 11.164,3.732 10.94,5.428 C10.85,6.112 11.051,6.808 11.601,7.227 C13.586,8.739 14.499,11.41 13.705,13.848 C12.91,16.295 10.555,18.005 7.973,18.005 M13.316,6.039 C13.076,5.823 12.955,5.519 12.968,5.198 C13.075,2.432 10.833,0 7.973,0 C5.111,0 2.868,2.433 2.977,5.2 C2.989,5.52 2.869,5.824 2.629,6.038 C-1.615,9.817 -0.632,17.124 4.94,19.416 C7.89,20.629 11.377,19.909 13.631,17.658 C17.125,14.17 16.528,8.905 13.316,6.039" />
    </svg>
  );
}