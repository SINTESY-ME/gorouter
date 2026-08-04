import { useEffect, useState } from "react";
import { Spinner, Button, Card } from "@heroui/react";
import { api } from "../api";
import { formatCompact, formatCost } from "../format";

// parseMetric sums the value of every line whose metric name matches `name`
// across all label sets (e.g. gorouter_requests_total{...} 3).
function parseMetric(text: string, name: string): number {
  const re = new RegExp(`^${name}(\\{[^}]*\\})?\\s+([0-9.eE+\\-]+)`, "gm");
  let sum = 0;
  let m: RegExpExecArray | null;
  while ((m = re.exec(text))) {
    const v = parseFloat(m[2]);
    if (Number.isFinite(v)) sum += v;
  }
  return sum;
}

export default function Metrics() {
  const [text, setText] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [showRaw, setShowRaw] = useState(false);

  useEffect(() => {
    api.metrics.fetch()
      .then(setText)
      .catch((e) => setError(e?.message ?? "falha ao buscar /metrics"))
      .finally(() => {});
  }, []);

  if (error) return <div className="text-center py-20 text-muted">{error}</div>;
  if (text === null) return <div className="flex justify-center py-20"><Spinner /></div>;

  const requests = parseMetric(text, "gorouter_requests_total");
  const failed = parseMetric(text, "gorouter_failed_requests_total");
  const input = parseMetric(text, "gorouter_tokens_input_total");
  const output = parseMetric(text, "gorouter_tokens_output_total");
  const spend = parseMetric(text, "gorouter_spend_usd_total");
  const uptime = parseMetric(text, "gorouter_uptime_seconds");
  const unhealthy = parseMetric(text, "gorouter_health_unhealthy");
  const probing = parseMetric(text, "gorouter_health_probing");
  const healthy = parseMetric(text, "gorouter_health_healthy");
  const cacheEntries = parseMetric(text, "gorouter_cache_entries");
  const cacheHits = parseMetric(text, "gorouter_cache_hits");
  const cacheMisses = parseMetric(text, "gorouter_cache_misses");

  const cards = [
    { label: "Requests", value: formatCompact(requests), sub: `${formatCompact(failed)} falhas`, danger: failed > 0 },
    { label: "Tokens", value: formatCompact(input + output), sub: `${formatCompact(input)} in · ${formatCompact(output)} out` },
    { label: "Custo", value: formatCost(spend), sub: "total em USD" },
    { label: "Uptime", value: uptime > 0 ? `${Math.floor(uptime / 3600)}h ${Math.floor((uptime % 3600) / 60)}m` : "—", sub: "desde o start" },
  ];
  const healthCards = [
    { label: "Saúde", value: formatCompact(healthy), sub: "saudáveis", danger: false },
    { label: "Unhealthy", value: formatCompact(unhealthy), sub: "pausados", danger: unhealthy > 0 },
    { label: "Probando", value: formatCompact(probing), sub: "probes em voo" },
    { label: "Cache entries", value: formatCompact(cacheEntries), sub: `${formatCompact(cacheHits)} hits · ${formatCompact(cacheMisses)} misses` },
  ];

  return (
    <div className="space-y-6 max-w-4xl">
      <div className="flex items-center justify-between flex-wrap gap-3">
        <div>
          <h1 className="text-2xl font-bold tracking-tight">Metrics</h1>
          <p className="text-sm text-muted mt-0.5">Métricas em tempo real do /metrics (Prometheus).</p>
        </div>
        <Button size="sm" variant="secondary" onPress={() => setShowRaw((v) => !v)}>
          {showRaw ? "Ver resumo" : "Ver raw"}
        </Button>
      </div>

      {showRaw ? (
        <Card className="p-4">
          <pre className="text-[11px] leading-relaxed overflow-auto max-h-[70vh] font-mono">{text}</pre>
        </Card>
      ) : (
        <>
          <div className="grid grid-cols-2 lg:grid-cols-4 gap-4">
            {cards.map((c) => (
              <Card key={c.label} className={`p-5 ${c.danger ? "border-danger/30" : "border-border"}`}>
                <p className="text-xs text-muted uppercase tracking-wide font-medium">{c.label}</p>
                <p className="text-2xl font-bold mt-2 tabular-nums">{c.value}</p>
                <p className="text-xs text-muted mt-1">{c.sub}</p>
              </Card>
            ))}
          </div>
          <div className="grid grid-cols-2 lg:grid-cols-4 gap-4">
            {healthCards.map((c) => (
              <Card key={c.label} className={`p-5 ${c.danger ? "border-danger/30" : "border-border"}`}>
                <p className="text-xs text-muted uppercase tracking-wide font-medium">{c.label}</p>
                <p className="text-2xl font-bold mt-2 tabular-nums">{c.value}</p>
                <p className="text-xs text-muted mt-1">{c.sub}</p>
              </Card>
            ))}
          </div>
          <p className="text-xs text-muted">
            As métricas de request requerem o hook <code className="text-xs">prometheus</code> ativo (Configurações). Os gauges de estado sempre presentes.
          </p>
        </>
      )}
    </div>
  );
}
