import { useState } from "react";
import { Button, Card, Input, Label, TextField } from "@heroui/react";
import { api, setDashboardToken } from "../api";
import { IconRoute } from "../icons";

// Setup is the first-run page shown when no dashboard password is
// configured. The user sets a password; it's stored hashed on the server
// and the user is logged in immediately.
export default function Setup({ onDone }: { onDone: () => void }) {
  const [pw, setPw] = useState("");
  const [confirm, setConfirm] = useState("");
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setErr("");
    if (pw.length < 4) { setErr("Password deve ter ao menos 4 caracteres."); return; }
    if (pw !== confirm) { setErr("As senhas não coincidem."); return; }
    setBusy(true);
    try {
      await api.auth.setup(pw);
      const res = await api.auth.login(pw);
      setDashboardToken(res.token);
      onDone();
    } catch (e: any) {
      setErr(e?.message ?? "Falha ao definir senha.");
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
          <div>
            <h2 className="text-lg font-semibold">Bem-vindo</h2>
            <p className="text-sm text-muted mt-1">
              Defina uma senha para o dashboard. Ela será solicitada em
              cada acesso futuro.
            </p>
          </div>
          <form onSubmit={submit} className="space-y-3">
            <TextField isRequired value={pw} onChange={setPw} type="password">
              <Label>Senha</Label>
              <Input placeholder="Senha" autoFocus disabled={busy} />
            </TextField>
            <TextField isRequired value={confirm} onChange={setConfirm} type="password">
              <Label>Confirmar senha</Label>
              <Input placeholder="Confirmar senha" disabled={busy} />
            </TextField>
            {err && <p className="text-sm text-danger">{err}</p>}
            <Button type="submit" fullWidth isPending={busy}>
              {busy ? "Definindo..." : "Definir senha"}
            </Button>
          </form>
        </Card>
      </div>
    </div>
  );
}