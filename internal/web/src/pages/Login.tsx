import { useState } from "react";
import { Button, Card, Input, Label, TextField } from "@heroui/react";
import { useTranslation } from "react-i18next";
import { api, setDashboardToken, clearDashboardToken } from "../api";
import { IconRoute } from "../icons";

// Login is the dashboard login page. The user enters their email and
// password; on success the returned session token is stored and the
// dashboard mounts.
export default function Login({ onDone }: { onDone: () => void }) {
  const { t } = useTranslation();
  const [email, setEmail] = useState("");
  const [pw, setPw] = useState("");
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setErr("");
    setBusy(true);
    try {
      const res = await api.auth.login(email, pw);
      setDashboardToken(res.token);
      onDone();
    } catch (e: any) {
      const msg = e?.message ?? t("login.error");
      setErr(/401|invalid/i.test(msg) ? t("login.errorWrong") : msg);
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
            <p className="font-bold text-lg leading-tight">{t("login.brand")}</p>
            <p className="text-xs text-muted leading-tight">{t("login.tagline")}</p>
          </div>
        </div>
        <Card className="p-6 space-y-4">
          <h2 className="text-lg font-semibold">{t("login.title")}</h2>
          <form onSubmit={submit} className="space-y-3">
            <TextField isRequired value={email} onChange={setEmail} type="email">
              <Label>{t("login.email")}</Label>
              <Input variant="secondary" placeholder={t("login.emailPlaceholder")} autoFocus disabled={busy} autoComplete="email" />
            </TextField>
            <TextField isRequired value={pw} onChange={setPw} type="password">
              <Label>{t("login.password")}</Label>
              <Input variant="secondary" placeholder={t("login.passwordPlaceholder")} disabled={busy} autoComplete="current-password" />
            </TextField>
            {err && <p className="text-sm text-danger">{err}</p>}
            <Button type="submit" fullWidth isPending={busy}>
              {busy ? t("login.submitting") : t("login.submit")}
            </Button>
          </form>
        </Card>
      </div>
    </div>
  );
}
