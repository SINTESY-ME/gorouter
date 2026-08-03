import { useState } from "react";
import { Button, Card, Input, Label, TextField } from "@heroui/react";
import { api, setDashboardToken, clearDashboardToken } from "../api";
import { IconRoute } from "../icons";

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
    <div className="min-h-screen bg-background flex items-center justify-center px-4">
      <div className="w-full max-w-sm">
        <div className="flex items-center gap-3 mb-8 justify-center">
          <IconRoute className="w-6 h-6 text-accent" />
          <div>
            <p className="font-bold text-lg leading-tight">gorouter</p>
            <p className="text-xs text-muted leading-tight">LLM router</p>
          </div>
        </div>
        <Card className="p-6 space-y-4">
          <h2 className="text-lg font-semibold">Entrar</h2>
          <form onSubmit={submit} className="space-y-3">
            <TextField isRequired value={pw} onChange={setPw} type="password">
              <Label>Senha</Label>
              <Input placeholder="Senha" autoFocus disabled={busy} />
            </TextField>
            {err && <p className="text-sm text-danger">{err}</p>}
            <Button type="submit" fullWidth isPending={busy}>
              {busy ? "Entrando..." : "Entrar"}
            </Button>
          </form>
        </Card>
      </div>
    </div>
  );
}