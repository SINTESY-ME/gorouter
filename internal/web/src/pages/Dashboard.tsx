import { useEffect, useState } from "react";
import { Spinner, Select, SelectItem, Popover, PopoverTrigger, PopoverContent, Input, Button, Chip, Tabs, Tab, Divider } from "@heroui/react";
import {
  ResponsiveContainer, AreaChart, Area, XAxis, YAxis, Tooltip, CartesianGrid,
  BarChart, Bar, PieChart, Pie, Cell, Legend,
} from "recharts";
import { api, type UsageStats, type SavingsStats, type ApiKey, type StatusSnapshot } from "../api";
import { formatCompact, formatCost } from "../format";

const PIE_COLORS = ["#00C2A8", "#FF6B6B", "#4DA3FF", "#FFB347", "#B266FF", "#FFD93D", "#6BCB77"];

const periods: { key: string; label: string }[] = [
  { key: "1h", label: "1 hora" },
  { key: "24h", label: "24 horas" },
  { key: "7d", label: "7 dias" },
  { key: "30d", label: "30 dias" },
  { key: "60d", label: "60 dias" },
];

const buckets: { key: string; label: string }[] = [
  { key: "", label: "Auto" },
  { key: "minute", label: "Minuto" },
  { key: "5m", label: "5 min" },
  { key: "30m", label: "30 min" },
  { key: "hour", label: "Hora" },
  { key: "day", label: "Dia" },
];

const bucketLabel: Record<string, string> = {
  minute: "minuto", "5m": "5 min", "30m": "30 min", hour: "hora", day: "dia",
};

const chartTooltipStyle = {
  backgroundColor: "#1a1a1a",
  border: "1px solid #333",
  borderRadius: "8px",
  fontSize: "12px",
  color: "#eee",
};
const chartItemStyle = { color: "#eee" };

// formatBucketLabel formats the date string from the backend based on the
// bucket type so the XAxis shows meaningful labels.
function formatBucketLabel(dateStr: string, bucket: string): string {
  // day: "2026-07-26" -> "07-26"
  // hour: "2026-07-26T14:00" -> "14:00"
  // minute/5m/30m: "2026-07-26T14:35" -> "14:35"
  if (bucket === "day") return dateStr.slice(5);
  if (dateStr.includes("T")) {
    const time = dateStr.split("T")[1];
    return time;
  }
  return dateStr.slice(5);
}

// maskKey keeps only the first 6 and last 4 chars. Used to display the
// api key in the dashboard without leaking the full secret.
function maskKey(k: string): string {
  if (!k) return "(vazio)";
  if (k.length <= 12) return k.slice(0, 3) + "..." + k.slice(-2);
  return k.slice(0, 6) + "..." + k.slice(-4);
}

// nameKey looks up the masked key's friendly name from the api_keys list
// so we can show "my-token" instead of "gr-xxx...1234" in tables.
function nameKey(k: string, keys: ApiKey[]): string {
  const found = keys.find((x) => x.key === k);
  return found?.name || maskKey(k);
}

export default function Dashboard() {
  const [stats, setStats] = useState<UsageStats | null>(null);
  const [savings, setSavings] = useState<SavingsStats | null>(null);
  const [status, setStatus] = useState<StatusSnapshot | null>(null);
  const [period, setPeriod] = useState("24h");
  const [bucket, setBucket] = useState(""); // "" = auto
  const [customMode, setCustomMode] = useState(false);
  const [fromDate, setFromDate] = useState("");
  const [toDate, setToDate] = useState("");
  const [loading, setLoading] = useState(true);
  const [apiKeys, setApiKeys] = useState<ApiKey[]>([]);
  const [selectedKeyId, setSelectedKeyId] = useState<string>("");

  useEffect(() => {
    api.keys.list().then(setApiKeys).catch(() => {});
  }, []);

  useEffect(() => {
    setLoading(true);
    const params: { period?: string; from?: string; to?: string; bucket?: string; api_key_id?: string } = {};
    if (customMode && fromDate) {
      params.from = new Date(fromDate).toISOString();
      if (toDate) params.to = new Date(toDate).toISOString();
      if (bucket) params.bucket = bucket;
    } else {
      params.period = period;
      if (bucket) params.bucket = bucket;
    }
    if (selectedKeyId) params.api_key_id = selectedKeyId;
    Promise.all([
      api.usage.stats(params),
      api.savings.stats(customMode && fromDate ? "60d" : period, selectedKeyId).catch(() => null),
      api.status().catch(() => null),
    ])
      .then(([s, sv, st]) => { setStats(s); setSavings(sv); setStatus(st); })
      .catch(() => setStats(null))
      .finally(() => setLoading(false));
  }, [period, bucket, customMode, fromDate, toDate, selectedKeyId]);

  if (loading) return (
    <div className="flex justify-center py-20"><Spinner label="Carregando..." /></div>
  );
  if (!stats) return (
    <div className="text-center py-20 text-default-500">
      Não há dados de uso ainda. Crie um provider e faça uma requisição.
    </div>
  );

  const activeBucket = stats.bucket || "day";
  const daily = stats.daily.map((d) => ({ ...d, label: formatBucketLabel(d.date, activeBucket) }));
  const byProvider = Object.entries(stats.by_provider).map(([name, value]) => ({ name, value }));
  const byModel = Object.entries(stats.by_model).map(([name, value]) => ({ name, value }));
  const byModelCost = Object.entries(stats.by_model_cost || {}).map(([name, value]) => ({ name, value })).sort((a, b) => b.value - a.value);
  const byCombo = Object.entries(stats.by_combo || {}).map(([name, value]) => ({ name, value })).sort((a, b) => b.value - a.value);
  const byEndpoint = Object.entries(stats.by_endpoint || {}).map(([name, value]) => ({ name, value })).sort((a, b) => b.value - a.value);
  const byApiKey = Object.entries(stats.by_api_key || {}).map(([name, value]) => ({ name: nameKey(name, apiKeys), value })).sort((a, b) => b.value - a.value);

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between flex-wrap gap-3">
        <div>
          <h1 className="text-2xl font-bold tracking-tight">Visão geral</h1>
          <p className="text-sm text-default-500 mt-0.5">
            Total de {stats.requests.toLocaleString("en-US")} requisições no período
          </p>
        </div>
        <div className="flex items-center gap-2 flex-wrap">
          <div className="flex bg-content1 rounded-lg p-0.5 border border-default-100">
            {periods.map((p) => (
              <button
                key={p.key}
                onClick={() => { setPeriod(p.key); setCustomMode(false); }}
                className={`px-3 py-1.5 text-sm rounded-md transition-colors ${
                  !customMode && period === p.key ? "bg-primary text-white" : "text-default-600 hover:bg-default-100"
                }`}
              >
                {p.label}
              </button>
            ))}
          </div>
          <Popover placement="bottom">
            <PopoverTrigger>
              <Button
                size="sm"
                variant={customMode ? "solid" : "flat"}
                color={customMode ? "primary" : "default"}
                onPress={() => setCustomMode(true)}
              >
                <IconCalendar className="w-4 h-4" />
                {customMode && fromDate ? formatRangeLabel(fromDate, toDate) : "Personalizado"}
              </Button>
            </PopoverTrigger>
            <PopoverContent className="p-3">
              <div className="space-y-3 w-64">
                <Input
                  type="datetime-local"
                  label="De"
                  value={fromDate}
                  onValueChange={(v) => { setFromDate(v); setCustomMode(true); }}
                  size="sm"
                  classNames={{ inputWrapper: "h-9 min-h-9" }}
                />
                <Input
                  type="datetime-local"
                  label="Até"
                  value={toDate}
                  onValueChange={setToDate}
                  size="sm"
                  placeholder="Agora"
                  classNames={{ inputWrapper: "h-9 min-h-9" }}
                />
                <Button size="sm" color="primary" className="w-full" onPress={() => { setCustomMode(true); }}>
                  Aplicar
                </Button>
                {customMode && (
                  <Button size="sm" variant="flat" className="w-full" onPress={() => { setCustomMode(false); setFromDate(""); setToDate(""); }}>
                    Voltar para presets
                  </Button>
                )}
              </div>
            </PopoverContent>
          </Popover>
          <Select
            aria-label="Granularidade"
            selectedKeys={[bucket]}
            onChange={(e) => setBucket(e.target.value)}
            size="sm"
            className="w-32"
            disallowEmptySelection
          >
            {buckets.map((b) => (
              <SelectItem key={b.key}>{b.label}</SelectItem>
            ))}
          </Select>
          {apiKeys.length > 0 && (
            <Select
              aria-label="Token"
              selectedKeys={selectedKeyId ? [selectedKeyId] : []}
              onChange={(e) => setSelectedKeyId(e.target.value)}
              size="sm"
              className="w-44"
              placeholder="Todos os tokens"
            >
              {apiKeys.map((k) => (
                <SelectItem key={k.id}>{k.name}</SelectItem>
              ))}
            </Select>
          )}
        </div>
      </div>

      {/* System status — runtime health snapshot, polled every dashboard refresh */}
      {status && (
        <div className="grid grid-cols-2 md:grid-cols-4 gap-3">
          <SystemCard label="Combos" value={status.combos.total.toString()} sub="estratégias de roteamento" />
          <SystemCard label="Conexões ativas" value={`${status.connections.active}/${status.connections.total}`} sub={`${status.connections.rate_limited} rate-limited`} warn={status.connections.rate_limited > 0} />
          <SystemCard label="Saúde" value={status.health.unhealthy > 0 ? `${status.health.unhealthy} unhealthy` : "OK"} sub={`${status.health.probing} probing · ${status.health.healthy} healthy`} warn={status.health.unhealthy > 0} />
          <SystemCard label="Tokens ativos" value={apiKeys.filter(k => k.is_active).length.toString()} sub={`de ${apiKeys.length} cadastrados`} />
        </div>
      )}

      {/* Top-line counters */}
      <div className="grid grid-cols-2 lg:grid-cols-4 gap-4">
        <StatCard label="Requests" value={formatCompact(stats.requests)} sub="total no período" full={stats.requests.toLocaleString("en-US")} />
        <StatCard label="Tokens" value={formatCompact(stats.prompt_tokens + stats.completion_tokens)} sub={`${formatCompact(stats.prompt_tokens)} prompt · ${formatCompact(stats.completion_tokens)} completion`} />
        <StatCard label="Custo" value={formatCost(stats.cost)} sub={stats.cost_per_request > 0 ? `${formatCost(stats.cost_per_request)}/req` : "—"} full={`$${stats.cost.toFixed(6)}`} />
        <StatCard label="Custo poupado" value={formatCost(stats.cost_saved)} sub={`${formatCompact(stats.tokens_saved)} tokens (cache+RTK)`} full={`$${stats.cost_saved.toFixed(6)}`} color="text-success" />
      </div>

      {/* Performance — TTFT, TPS, latency, error rate */}
      <div className="grid grid-cols-2 md:grid-cols-3 lg:grid-cols-6 gap-3">
        <MetricCard label="TTFT médio" value={stats.avg_ttft_ms > 0 ? `${stats.avg_ttft_ms}ms` : "—"} sub="time to first token" />
        <MetricCard label="TPS médio" value={stats.avg_tps > 0 ? stats.avg_tps.toFixed(1) : "—"} sub="tokens/s (geração)" />
        <MetricCard label="Latência média" value={stats.avg_latency_ms > 0 ? `${stats.avg_latency_ms}ms` : "—"} sub="por request" />
        <MetricCard label="P50 / P95 / P99" value={stats.p95_latency_ms > 0 ? `${stats.p50_latency_ms}/${stats.p95_latency_ms}/${stats.p99_latency_ms}` : "—"} sub="ms · percentis" small />
        <MetricCard
          label="Taxa de erro"
          value={`${(stats.error_rate * 100).toFixed(1)}%`}
          sub={`${stats.error_requests.toLocaleString("en-US")} erros`}
          color={stats.error_rate > 0.05 ? "text-danger" : "text-success"}
        />
        <MetricCard
          label="Cache hit rate"
          value={`${(stats.cache_hit_rate * 100).toFixed(1)}%`}
          sub={`${stats.cache_hits.toLocaleString("en-US")} hits`}
          color="text-success"
        />
      </div>

      {/* Efficiency — tokens per $, cost per request */}
      <div className="grid grid-cols-2 lg:grid-cols-4 gap-3">
        <MetricCard label="$/1k tokens" value={stats.tokens_per_dollar > 0 ? `${(1000 / stats.tokens_per_dollar).toFixed(4)}` : "—"} sub={stats.tokens_per_dollar > 0 ? `${stats.tokens_per_dollar.toFixed(0)} tokens/$` : "sem dados"} />
        <MetricCard label="$/request" value={stats.cost_per_request > 0 ? `$${stats.cost_per_request.toFixed(6)}` : "—"} sub="média por chamada" />
        <MetricCard label="Combos usados" value={stats.combo_requests.toLocaleString("en-US")} sub="de rotas" sub2={`${stats.requests > 0 ? ((stats.combo_requests / stats.requests) * 100).toFixed(1) : 0}% do total`} />
        <MetricCard label="Endpoints" value={Object.keys(stats.by_endpoint || {}).length.toString()} sub="rotas OpenAI" />
      </div>

      {/* Savings cards (cache + RTK, persisted in DB) */}
      {savings && (
        <div className="bg-content1 rounded-2xl border border-default-100 p-6">
          <h3 className="font-semibold mb-1">Economia</h3>
          <p className="text-xs text-default-500 mb-4">Tokens e custos economizados por Response Cache e RTK — Token Compression</p>
          <div className="grid grid-cols-2 md:grid-cols-4 gap-4">
            <SavingsCard
              label="Cache hits"
              value={formatCompact(savings.cache_hits)}
              sub="respostas servidas do cache"
              full={savings.cache_hits.toLocaleString("en-US")}
              color="#00C2A8"
            />
            <SavingsCard
              label="Tokens poupados (cache)"
              value={formatCompact(savings.cache_tokens_saved)}
              sub={`custo poupado: ${formatCost(savings.cache_cost_saved)}`}
              full={savings.cache_tokens_saved.toLocaleString("en-US")}
              color="#00C2A8"
            />
            <SavingsCard
              label="Compressões RTK"
              value={formatCompact(savings.rtk_compressions)}
              sub="tool_results comprimidos"
              full={savings.rtk_compressions.toLocaleString("en-US")}
              color="#4DA3FF"
            />
            <SavingsCard
              label="Tokens poupados (RTK)"
              value={formatCompact(savings.rtk_tokens_saved)}
              sub={`custo poupado: ${formatCost(savings.rtk_cost_saved)}`}
              full={savings.rtk_tokens_saved.toLocaleString("en-US")}
              color="#4DA3FF"
            />
          </div>
        </div>
      )}

      {/* Time series chart with tabbed metrics: requests / tokens / cost / errors / TPS */}
      <div className="bg-content1 rounded-2xl border border-default-100 p-6">
        <div className="flex items-center justify-between mb-4 gap-3 flex-wrap">
          <div>
            <h3 className="font-semibold">Série temporal</h3>
            <p className="text-xs text-default-500 mt-0.5">Volume por {bucketLabel[activeBucket] || "período"}</p>
          </div>
          <Tabs
            size="sm"
            variant="solid"
            color="primary"
            selectedKey="requests"
            classNames={{ tabList: "bg-content2" }}
          >
            <Tab key="requests" title="Requests"><TimeSeries data={daily} metric="requests" color="#00C2A8" formatY={formatCompact} formatTooltip={(v) => v.toLocaleString("en-US")} name="Requests" /></Tab>
            <Tab key="tokens" title="Tokens"><TimeSeries data={daily} metric="tokens" color="#4DA3FF" formatY={formatCompact} formatTooltip={(v) => v.toLocaleString("en-US")} name="Tokens" /></Tab>
            <Tab key="cost" title="Custo"><TimeSeries data={daily} metric="cost" color="#FFB347" formatY={formatCost} formatTooltip={(v) => `$${v.toFixed(6)}`} name="Custo" /></Tab>
            <Tab key="errors" title="Erros"><TimeSeries data={daily} metric="errors" color="#FF6B6B" formatY={formatCompact} formatTooltip={(v) => v.toLocaleString("en-US")} name="Erros" /></Tab>
            <Tab key="tps" title="TPS"><TimeSeries data={daily} metric="avg_tps" color="#B266FF" formatY={(v) => v.toFixed(1)} formatTooltip={(v) => `${v.toFixed(2)} tok/s`} name="TPS" /></Tab>
          </Tabs>
        </div>
      </div>

      {/* Distribution charts — provider, model, model cost, combo, endpoint, key */}
      <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
        <DistributionChart title="Por provider" subtitle="Distribuição de requisições" data={byProvider} color="#00C2A8" />
        <DistributionChart title="Por modelo" subtitle="Requisições por modelo" data={byModel} color="#4DA3FF" />
        <DistributionChart title="Custo por modelo" subtitle="Gasto em USD por modelo" data={byModelCost} color="#FFB347" formatValue={formatCost} formatTooltip={(v) => `$${v.toFixed(6)}`} />
        {byCombo.length > 0 && (
          <DistributionChart title="Por combo" subtitle="Distribuição entre combos" data={byCombo} color="#B266FF" />
        )}
        {byEndpoint.length > 0 && (
          <DistributionChart title="Por endpoint" subtitle="Rotas OpenAI utilizadas" data={byEndpoint.map((e) => ({ name: e.name || "(none)", value: e.value }))} color="#FFD93D" />
        )}
        {byApiKey.length > 0 && (
          <DistributionChart title="Por token" subtitle="Requisições por token de API" data={byApiKey} color="#6BCB77" />
        )}
      </div>
    </div>
  );
}

// TimeSeries renders a single-metric area chart for the time series tab.
// `metric` is the dataKey name on the row objects.
function TimeSeries({ data, metric, color, formatY, formatTooltip, name }: {
  data: any[]; metric: string; color: string; formatY: (v: number) => string; formatTooltip: (v: number) => string; name: string;
}) {
  const gradId = `grad-${metric}`;
  return (
    <ResponsiveContainer width="100%" height={280}>
      <AreaChart data={data} margin={{ left: -16, right: 8, top: 8 }}>
        <defs>
          <linearGradient id={gradId} x1="0" y1="0" x2="0" y2="1">
            <stop offset="5%" stopColor={color} stopOpacity={0.6} />
            <stop offset="95%" stopColor={color} stopOpacity={0.02} />
          </linearGradient>
        </defs>
        <CartesianGrid strokeDasharray="3 3" stroke="#2a2a2a" vertical={false} />
        <XAxis dataKey="label" stroke="#666" tick={{ fontSize: 11 }} tickLine={false} axisLine={false} interval="preserveStartEnd" minTickGap={20} />
        <YAxis stroke="#666" tick={{ fontSize: 12 }} tickLine={false} axisLine={false} allowDecimals={false} tickFormatter={formatY} />
        <Tooltip contentStyle={chartTooltipStyle} itemStyle={chartItemStyle} labelStyle={{ color: "#888" }} formatter={(v: number) => [formatTooltip(v), name]} />
        <Area type="monotone" dataKey={metric} stroke={color} strokeWidth={2} fill={`url(#${gradId})`} />
      </AreaChart>
    </ResponsiveContainer>
  );
}

// DistributionChart renders a horizontal bar chart for grouped data
// (by_provider, by_model, by_combo, etc). Uses pie when ≤ 6 items,
// otherwise bar — pie becomes unreadable past ~7 slices.
function DistributionChart({ title, subtitle, data, color, formatValue, formatTooltip }: {
  title: string; subtitle: string; data: { name: string; value: number }[]; color: string;
  formatValue?: (v: number) => string; formatTooltip?: (v: number) => string;
}) {
  const fmtV = formatValue ?? formatCompact;
  const fmtT = formatTooltip ?? ((v: number) => v.toLocaleString("en-US"));
  const usePie = data.length <= 5;
  return (
    <div className="bg-content1 rounded-2xl border border-default-100 p-6">
      <h3 className="font-semibold mb-1">{title}</h3>
      <p className="text-xs text-default-500 mb-4">{subtitle}</p>
      {data.length === 0 ? (
        <EmptyChart />
      ) : usePie ? (
        <ResponsiveContainer width="100%" height={260}>
          <PieChart>
            <Pie data={data} dataKey="value" nameKey="name" cx="50%" cy="50%" innerRadius={60} outerRadius={95} paddingAngle={2} label={(entry: any) => <text x={entry.x} y={entry.y} fill="#aaa" fontSize={11} textAnchor={entry.x > entry.cx ? "start" : "end"} dominantBaseline="central">{entry.name}</text>} labelLine={{ stroke: "#666" }}>
              {data.map((_, i) => <Cell key={i} fill={PIE_COLORS[i % PIE_COLORS.length]} stroke="none" />)}
            </Pie>
            <Legend formatter={(v) => <span className="text-xs text-default-600">{v}</span>} />
            <Tooltip contentStyle={chartTooltipStyle} itemStyle={chartItemStyle} labelStyle={{ color: "#aaa" }} />
          </PieChart>
        </ResponsiveContainer>
      ) : (
        <ResponsiveContainer width="100%" height={Math.max(260, data.length * 24)}>
          <BarChart data={data} layout="vertical" margin={{ left: 20, right: 8, top: 8 }}>
            <CartesianGrid strokeDasharray="3 3" stroke="#2a2a2a" horizontal={false} />
            <XAxis type="number" stroke="#666" tick={{ fontSize: 12 }} tickLine={false} axisLine={false} allowDecimals={false} tickFormatter={fmtV} />
            <YAxis type="category" dataKey="name" stroke="#666" tick={{ fontSize: 11 }} tickLine={false} axisLine={false} width={120} />
            <Tooltip contentStyle={chartTooltipStyle} itemStyle={chartItemStyle} cursor={{ fill: "#ffffff10" }} formatter={(v: number) => [fmtT(v), title.replace(/^Por |^Custo por /, "")]} />
            <Bar dataKey="value" fill={color} radius={[0, 4, 4, 0]} barSize={18} />
          </BarChart>
        </ResponsiveContainer>
      )}
    </div>
  );
}

function StatCard({ label, value, sub, full, color }: { label: string; value: string | number; sub: string; full?: string; color?: string }) {
  return (
    <div className="bg-content1 rounded-2xl border border-default-100 p-5 hover:border-default-200 transition-colors">
      <p className="text-xs text-default-500 uppercase tracking-wide font-medium">{label}</p>
      <p className={`text-3xl font-bold mt-2 tabular-nums ${color || ""}`} title={full}>{value}</p>
      <p className="text-xs text-default-500 mt-1">{sub}</p>
    </div>
  );
}

// SystemCard is a small chip-style card for runtime stats (combos,
// connections, health). `warn` switches the color to red.
function SystemCard({ label, value, sub, warn }: { label: string; value: string; sub: string; warn?: boolean }) {
  return (
    <div className={`bg-content1 rounded-xl border p-3 transition-colors ${warn ? "border-warning/40 bg-warning/5" : "border-default-100"}`}>
      <p className="text-[10px] text-default-500 uppercase tracking-wide font-medium">{label}</p>
      <p className={`text-xl font-bold mt-1 tabular-nums ${warn ? "text-warning" : ""}`}>{value}</p>
      <p className="text-[11px] text-default-500 mt-0.5">{sub}</p>
    </div>
  );
}

// MetricCard is a smaller stat card for the performance row. `small` drops
// the value size down so a long "P50/P95/P99" label fits.
function MetricCard({ label, value, sub, sub2, color, small }: { label: string; value: string; sub: string; sub2?: string; color?: string; small?: boolean }) {
  return (
    <div className="bg-content1 rounded-xl border border-default-100 p-4">
      <p className="text-[11px] text-default-500 uppercase tracking-wide font-medium">{label}</p>
      <p className={`${small ? "text-lg" : "text-2xl"} font-bold mt-1.5 tabular-nums ${color || ""}`}>{value}</p>
      <p className="text-[11px] text-default-500 mt-0.5">{sub}</p>
      {sub2 && <p className="text-[11px] text-default-500">{sub2}</p>}
    </div>
  );
}

function SavingsCard({ label, value, sub, full, color }: { label: string; value: string | number; sub: string; full?: string; color: string }) {
  return (
    <div className="bg-content1 rounded-2xl border border-default-100 p-5 hover:border-default-200 transition-colors">
      <div className="flex items-center gap-2">
        <span className="w-2 h-2 rounded-full" style={{ backgroundColor: color }} />
        <p className="text-xs text-default-500 uppercase tracking-wide font-medium">{label}</p>
      </div>
      <p className="text-2xl font-bold mt-2 tabular-nums" title={full}>{value}</p>
      <p className="text-xs text-default-500 mt-1">{sub}</p>
    </div>
  );
}

function EmptyChart() {
  return <div className="h-[260px] flex items-center justify-center text-sm text-default-400">Sem dados</div>;
}

// formatRangeLabel shows a compact label on the "Personalizado" button.
function formatRangeLabel(from: string, to: string): string {
  const fmt = (s: string) => s.slice(11, 16) || s.slice(0, 10);
  const f = from ? fmt(from) : "?";
  const t = to ? fmt(to) : "agora";
  return `${f} → ${t}`;
}

function IconCalendar({ className }: { className?: string }) {
  return (
    <svg className={className} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
      <rect x="3" y="4" width="18" height="18" rx="2" ry="2" />
      <line x1="16" y1="2" x2="16" y2="6" />
      <line x1="8" y1="2" x2="8" y2="6" />
      <line x1="3" y1="10" x2="21" y2="10" />
    </svg>
  );
}