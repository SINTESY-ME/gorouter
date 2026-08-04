import { useEffect, useState } from "react";
import { Spinner, Switch, Button, Card, Input, Description } from "@heroui/react";
import { api } from "../api";
import { IconCopy, IconCheck } from "../icons";

const HOOKS: { id: string; label: string; desc: string }[] = [
  { id: "keyword_moderation", label: "Keyword moderation", desc: "Rejeita (400) mensagens que casam com GOROUTER_HOOK_MODERATION_PATTERNS." },
  { id: "prompt_injection_heuristic", label: "Prompt injection", desc: "Rejeita (400) padrões comuns de prompt injection em mensagens de usuário." },
  { id: "request_logging", label: "Request logging", desc: "Log estruturado (slog) de sucesso/falha por request." },
  { id: "prometheus", label: "Prometheus", desc: "Alimenta as métricas de request em /metrics." },
  { id: "webhook_logging", label: "Webhook logging", desc: "Envia eventos de request para a URL configurada abaixo." },
];

function endpoints(origin: string) {
  return [
    { path: "/metrics", desc: "Prometheus (público)" },
    { path: "/health", desc: "Status geral (público)" },
    { path: "/health/readiness", desc: "Prontidão — 200 se pronto (público)" },
  ].map((e) => ({ ...e, full: `${origin}${e.path}` }));
}

export default function Settings() {
  const [loading, setLoading] = useState(true);
  const [hooks, setHooks] = useState<string[]>([]);
  const [webhookUrl, setWebhookUrl] = useState("");
  const [groupsText, setGroupsText] = useState("{}");
  const [savingHook, setSavingHook] = useState<string | null>(null);
  const [webhookSaved, setWebhookSaved] = useState(false);
  const [groupsSaved, setGroupsSaved] = useState(false);
  const [groupsError, setGroupsError] = useState<string | null>(null);
  const [copied, setCopied] = useState<string | null>(null);
  const [origin, setOrigin] = useState("");

  useEffect(() => { if (typeof window !== "undefined") setOrigin(window.location.origin); }, []);

  const refresh = () => {
    setLoading(true);
    api.settings.get()
      .then((s) => {
        setHooks(s.hooks_enabled || []);
        setWebhookUrl(s.webhook_url || "");
        setGroupsText(JSON.stringify(s.caching_groups || {}, null, 2));
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

  const saveGroups = () => {
    try {
      const parsed = JSON.parse(groupsText);
      if (typeof parsed !== "object" || parsed === null || Array.isArray(parsed)) throw new Error("espera um objeto { \"grupo\": [\"modelo1\", ...] }");
      for (const [k, v] of Object.entries(parsed)) {
        if (!Array.isArray(v) || v.some((m) => typeof m !== "string")) {
          throw new Error(`\"${k}\" deve ser uma lista de strings`);
        }
      }
      setGroupsError(null);
      api.settings.update({ caching_groups: parsed as Record<string, string[]> })
        .then(() => { setGroupsSaved(true); setTimeout(() => setGroupsSaved(false), 1500); })
        .catch(() => {});
    } catch (e: any) {
      setGroupsError(e?.message ?? "JSON inválido");
    }
  };

  const copy = async (s: string) => {
    try { await navigator.clipboard.writeText(s); setCopied(s); setTimeout(() => setCopied(null), 1200); } catch {}
  };

  if (loading) return <div className="flex justify-center py-20"><Spinner /></div>;

  return (
    <div className="space-y-6 max-w-3xl">
      <div>
        <h1 className="text-2xl font-bold tracking-tight">Configurações</h1>
        <p className="text-sm text-muted mt-0.5">Hooks, observabilidade e cache compartilhado — aplicados ao vivo, sem restart.</p>
      </div>

      {/* Hooks */}
      <Card className="p-6">
        <h3 className="font-semibold">Hooks</h3>
        <p className="text-sm text-muted mt-1">Pipeline de hooks (PreCall/PostCall). Desativado = zero overhead no hot path.</p>
        <div className="mt-4 space-y-1">
          {HOOKS.map((h) => (
            <div key={h.id} className="flex items-start justify-between gap-4 py-2.5 border-b border-border last:border-0">
              <div className="flex-1">
                <p className="text-sm font-medium">{h.label}</p>
                <p className="text-xs text-muted mt-0.5">{h.desc}</p>
              </div>
              <Switch
                isSelected={hooks.includes(h.id)}
                onChange={(v) => toggleHook(h.id, v)}
                isDisabled={savingHook === h.id}
                size="lg"
              >
                <Switch.Content aria-label={h.label}>
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
          <h3 className="font-semibold">Webhook de observabilidade</h3>
          <p className="text-sm text-muted mt-1">
            Eventos de request (sucesso/falha) enviados por POST ao hook <code className="text-xs">webhook_logging</code>.
          </p>
          <div className="mt-4 flex gap-2">
            <Input
              value={webhookUrl}
              onChange={(e) => setWebhookUrl(e.target.value)}
              placeholder="https://hooks.slack.com/services/..."
              className="flex-1"
            />
            <Button variant="primary" onPress={saveWebhook}>{webhookSaved ? "Salvo ✓" : "Salvar"}</Button>
          </div>
          <Description className="mt-2">
            Valor inicial vem de <code className="text-xs">GOROUTER_HOOK_WEBHOOK_URL</code>; salvar aqui persiste e aplica ao vivo.
          </Description>
        </Card>
      )}

      {/* Caching groups */}
      <Card className="p-6">
        <h3 className="font-semibold">Caching groups</h3>
        <p className="text-sm text-muted mt-1">
          Modelos intercambiáveis compartilham a mesma entrada de cache. Responsabilidade do operador — as respostas precisam ser equivalentes.
        </p>
        <div className="mt-4 flex gap-2">
          <Input
            value={groupsText}
            onChange={(e) => setGroupsText(e.target.value)}
            className="flex-1 font-mono text-xs"
            placeholder='{"gpt-family": ["gpt-4o", "gpt-4o-mini"]}'
          />
          <Button variant="primary" onPress={saveGroups}>{groupsSaved ? "Salvo ✓" : "Salvar"}</Button>
        </div>
        <div className="mt-2 space-y-0.5">
          <Description>
            Estrutura — um objeto com nome do grupo → lista de modelos:
          </Description>
          <code className="block text-xs text-muted bg-default-soft px-2 py-1 rounded">
            {"{ \"nome-do-grupo\": [\"modelo1\", \"modelo2\"], \"outro\": [\"modelo3\"] }"}
          </code>
          {groupsError && <p className="text-xs text-danger mt-1">{groupsError}</p>}
        </div>
      </Card>

      {/* Endpoints */}
      <Card className="p-6">
        <h3 className="font-semibold">Endpoints operacionais</h3>
        <p className="text-sm text-muted mt-1">Para health checks (K8s/Swarm) e scraping de métricas.</p>
        <div className="mt-4 space-y-2">
          {endpoints(origin).map((e) => (
            <div key={e.path} className="flex items-center gap-2 text-sm">
              <button
                onClick={() => copy(e.full)}
                className="group flex items-center gap-2 font-mono text-xs text-accent hover:underline"
                title="Copiar"
              >
                {copied === e.full ? <IconCheck className="w-3.5 h-3.5 text-success" /> : <IconCopy className="w-3.5 h-3.5 text-muted group-hover:text-accent" />}
                {e.full}
              </button>
              <span className="text-xs text-muted">— {e.desc}</span>
            </div>
          ))}
        </div>
      </Card>
    </div>
  );
}
