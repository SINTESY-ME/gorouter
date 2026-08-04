import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { Spinner, Switch, Button, Card, Input, Description } from "@heroui/react";
import { api } from "../api";
import { IconCopy, IconCheck } from "../icons";

const HOOKS: { id: string; labelKey: string; descKey: string }[] = [
  { id: "keyword_moderation", labelKey: "settings.keywordLabel", descKey: "settings.keywordDesc" },
  { id: "prompt_injection_heuristic", labelKey: "settings.promptLabel", descKey: "settings.promptDesc" },
  { id: "request_logging", labelKey: "settings.loggingLabel", descKey: "settings.loggingDesc" },
  { id: "prometheus", labelKey: "settings.prometheusLabel", descKey: "settings.prometheusDesc" },
  { id: "webhook_logging", labelKey: "settings.webhookLabel", descKey: "settings.webhookDesc" },
];

function endpoints(origin: string) {
  return [
    { path: "/metrics", descKey: "settings.metricsDesc" },
    { path: "/health", descKey: "settings.healthDesc" },
    { path: "/health/readiness", descKey: "settings.readinessDesc" },
  ].map((e) => ({ ...e, full: `${origin}${e.path}` }));
}

export default function Settings() {
  const { t } = useTranslation();
  const [loading, setLoading] = useState(true);
  const [hooks, setHooks] = useState<string[]>([]);
  const [webhookUrl, setWebhookUrl] = useState("");
  const [savingHook, setSavingHook] = useState<string | null>(null);
  const [webhookSaved, setWebhookSaved] = useState(false);
  const [copied, setCopied] = useState<string | null>(null);
  const [origin, setOrigin] = useState("");

  useEffect(() => { if (typeof window !== "undefined") setOrigin(window.location.origin); }, []);

  const refresh = () => {
    setLoading(true);
    api.settings.get()
      .then((s) => {
        setHooks(s.hooks_enabled || []);
        setWebhookUrl(s.webhook_url || "");
      })
      .catch(() => {})
      .finally(() => setLoading(false));
  };
  useEffect(refresh, []);

  const toggleHook = (id: string, enabled: boolean) => {
    setSavingHook(id);
    const next = enabled ? [...hooks, id] : hooks.filter((h) => h !== id);
    setHooks(next);
    api.settings.update({ hooks_enabled: next })
      .catch(() => refresh())
      .finally(() => setSavingHook(null));
  };

  const saveWebhook = () => {
    api.settings.update({ webhook_url: webhookUrl.trim() })
      .then(() => { setWebhookSaved(true); setTimeout(() => setWebhookSaved(false), 1500); })
      .catch(() => {});
  };

  const copy = async (s: string) => {
    try { await navigator.clipboard.writeText(s); setCopied(s); setTimeout(() => setCopied(null), 1200); } catch {}
  };

  if (loading) return <div className="flex justify-center py-20"><Spinner /></div>;

  return (
    <div className="space-y-6 max-w-3xl">
      <div>
        <h1 className="text-2xl font-bold tracking-tight">{t("settings.title")}</h1>
        <p className="text-sm text-muted mt-0.5">{t("settings.subtitle")}</p>
      </div>

      {/* Hooks */}
      <Card className="p-6">
        <h3 className="font-semibold">{t("settings.hooksTitle")}</h3>
        <p className="text-sm text-muted mt-1">{t("settings.hooksDesc")}</p>
        <div className="mt-4 space-y-1">
          {HOOKS.map((h) => (
            <div key={h.id} className="flex items-start justify-between gap-4 py-2.5 border-b border-border last:border-0">
              <div className="flex-1">
                <p className="text-sm font-medium">{t(h.labelKey)}</p>
                <p className="text-xs text-muted mt-0.5">{t(h.descKey)}</p>
              </div>
              <Switch
                isSelected={hooks.includes(h.id)}
                onChange={(v) => toggleHook(h.id, v)}
                isDisabled={savingHook === h.id}
                size="lg"
              >
                <Switch.Content aria-label={t(h.labelKey)}>
                  <Switch.Control><Switch.Thumb /></Switch.Control>
                </Switch.Content>
              </Switch>
            </div>
          ))}
        </div>
      </Card>

      {/* Webhook */}
      {hooks.includes("webhook_logging") && (
        <Card className="p-6">
          <h3 className="font-semibold">{t("settings.webhookTitle")}</h3>
          <p className="text-sm text-muted mt-1">
            {t("settings.webhookDesc2")}
          </p>
          <div className="mt-4 flex gap-2">
            <Input
              value={webhookUrl}
              onChange={(e) => setWebhookUrl(e.target.value)}
              placeholder={t("settings.webhookPlaceholder")}
              className="flex-1"
            />
            <Button variant="primary" onPress={saveWebhook}>{webhookSaved ? t("settings.saved") : t("settings.save")}</Button>
          </div>
          <Description className="mt-2">
            {t("settings.webhookHint")}
          </Description>
        </Card>
      )}

      {/* Endpoints */}
      <Card className="p-6">
        <h3 className="font-semibold">{t("settings.endpointsTitle")}</h3>
        <p className="text-sm text-muted mt-1">{t("settings.endpointsDesc")}</p>
        <div className="mt-4 space-y-2">
          {endpoints(origin).map((e) => (
            <div key={e.path} className="flex items-center gap-2 text-sm">
              <button
                onClick={() => copy(e.full)}
                className="group flex items-center gap-2 font-mono text-xs text-accent hover:underline"
                title={t("settings.copyTitle")}
              >
                {copied === e.full ? <IconCheck className="w-3.5 h-3.5 text-success" /> : <IconCopy className="w-3.5 h-3.5 text-muted group-hover:text-accent" />}
                {e.full}
              </button>
              <span className="text-xs text-muted">— {t(e.descKey)}</span>
            </div>
          ))}
        </div>
      </Card>
    </div>
  );
}
