import { useEffect, useState } from "react";
import {
  Spinner, Select, SelectItem, Popover, PopoverTrigger, PopoverContent,
  Input, Button, Card, CardBody, CardHeader, Divider, Chip, Tabs, Tab,
  Progress, Tooltip as HeroTooltip,
} from "@heroui/react";
import {
  ResponsiveContainer, AreaChart, Area, XAxis, YAxis, Tooltip as RTooltip,
  CartesianGrid, BarChart, Bar, PieChart, Pie, Cell, Legend, LineChart, Line,
} from "recharts";
import { api, type UsageStats, type SavingsStats, type ApiKey, type StatusSnapshot } from "../api";
import { formatCompact, formatCost } from "../format";

const PIE_COLORS = ["#00C2A8", "#FF6B6B", "#4DA3FF", "#FFB347", "#B266FF", "#FFD93D", "#6BCB77"];

const periods: { key: string; label: string }[] = [
  { key: "1h", label: "1h" },
  { key: "24h", label: "24h" },
  { key: "7d", label: "7d" },
  { key: "30d", label: "30d" },
  { key: "60d", label: "60d" },
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

const tooltipStyle = {
  backgroundColor: "#1a1a1a", border: "1px solid #333", borderRadius: "8px",
  fontSize: "12px", color: "#eee",
};
const itemStyle = { color: "#eee" };

function formatBucketLabel(dateStr: string, bucket: string): string {
  if (bucket === "day") return dateStr.slice(5);
  if (dateStr.includes("T")) return dateStr.split("T")[1];
  return dateStr.slice(5);
}

function maskKey(k: string): string {
  if (!k) return "—";
  if (k.length <= 12) return k.slice(0, 3) + "..." + k.slice(-2);
  return k.slice(0, 6) + "..." + k.slice(-4);
}

function nameKey(k: string, keys: ApiKey[]): string {
  return keys.find((x) => x.key === k)?.name || maskKey(k);
}

export default function Dashboard() {
  const [stats, setStats] = useState<UsageStats | null>(null);
  const [savings, setSavings] = useState<SavingsStats | null>(null);
  const [status, setStatus] = useState<StatusSnapshot | null>(null);
  const [period, setPeriod] = useState("24h");
  const [bucket, setBucket] = useState("");
  const [customMode, setCustomMode] = useState(false);
  const [fromDate, setFromDate] = useState("");
  const [toDate, setToDate] = useState("");
  const [loading, setLoading] = useState(true);
  const [apiKeys, setApiKeys] = useState<ApiKey[]>([]);
  const [selectedKeyId, setSelectedKeyId] = useState<string>("");
  const [chartMetric, setChartMetric] = useState("requests");

  useEffect(() => { api.keys.list().then(setApiKeys).catch(() => {}); }, []);

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

  if (loading) return <div className="flex justify-center py-20"><Spinner label="Carregando..." /></div>;
  if (!stats) return (
    <div className="text-center py-20 text-default-500">
      Não há dados de uso ainda. Crie um provider e faça uma requisição.
    </div>
  );

  const activeBucket = stats.bucket || "day";
  const daily = stats.daily.map((d) => ({ ...d, label: formatBucketLabel(d.date, activeBucket) }));
  const byProvider = Object.entries(stats.by_provider).map(([name, value]) => ({ name, value }));
  const byModel = Object.entries(stats.by_model).map(([name, value]) => ({ name, value })).sort((a, b) => b.value - a.value);
  const byModelCost = Object.entries(stats.by_model_cost || {}).map(([name, value]) => ({ name, value })).sort((a, b) => b.value - a.value);
  const byCombo = Object.entries(stats.by_combo || {}).map(([name, value]) => ({ name, value })).sort((a, b) => b.value - a.value);
  const byApiKey = Object.entries(stats.by_api_key || {}).map(([name, value]) => ({ name: nameKey(name, apiKeys), value })).sort((a, b) => b.value - a.value);

  const totalTokens = stats.prompt_tokens + stats.completion_tokens;
  const errorPct = stats.error_rate * 100;
  const cachePct = stats.cache_hit_rate * 100;

  const chartMetrics = [
    { key: "requests", label: "Requests", color: "#00C2A8", fmt: (v: number) => v.toLocaleString("en-US"), yFmt: formatCompact },
    { key: "tokens", label: "Tokens", color: "#4DA3FF", fmt: (v: number) => v.toLocaleString("en-US"), yFmt: formatCompact },
    { key: "cost", label: "Custo", color: "#FFB347", fmt: (v: number) => `$${v.toFixed(6)}`, yFmt: formatCost },
    { key: "errors", label: "Erros", color: "#FF6B6B", fmt: (v: number) => v.toLocaleString("en-US"), yFmt: formatCompact },
    { key: "avg_tps", label: "TPS", color: "#B266FF", fmt: (v: number) => `${v.toFixed(2)} tok/s`, yFmt: (v: number) => v.toFixed(1) },
  ];
  const activeMetric = chartMetrics.find((m) => m.key === chartMetric) || chartMetrics[0];

  return (
    <div className="space-y-6">
      {/* Header + filters */}
      <div className="flex items-center justify-between flex-wrap gap-3">
        <div>
          <h1 className="text-2xl font-bold tracking-tight">Visão geral</h1>
          <p className="text-sm text-default-500 mt-0.5">
            {stats.requests.toLocaleString("en-US")} requisições no período
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
              <Button size="sm" variant={customMode ? "solid" : "flat"} color={customMode ? "primary" : "default"} onPress={() => setCustomMode(true)}>
                <IconCalendar className="w-4 h-4" />
                {customMode && fromDate ? formatRangeLabel(fromDate, toDate) : "Personalizado"}
              </Button>
            </PopoverTrigger>
            <PopoverContent className="p-3">
              <div className="space-y-3 w-64">
                <Input type="datetime-local" label="De" value={fromDate} onValueChange={(v) => { setFromDate(v); setCustomMode(true); }} size="sm" classNames={{ inputWrapper: "h-9 min-h-9" }} />
                <Input type="datetime-local" label="Até" value={toDate} onValueChange={setToDate} size="sm" placeholder="Agora" classNames={{ inputWrapper: "h-9 min-h-9" }} />
                <Button size="sm" color="primary" className="w-full" onPress={() => setCustomMode(true)}>Aplicar</Button>
                {customMode && (
                  <Button size="sm" variant="flat" className="w-full" onPress={() => { setCustomMode(false); setFromDate(""); setToDate(""); }}>
                    Voltar para presets
                  </Button>
                )}
              </div>
            </PopoverContent>
          </Popover>
          <Select aria-label="Granularidade" selectedKeys={[bucket]} onChange={(e) => setBucket(e.target.value)} size="sm" className="w-32" disallowEmptySelection>
            {buckets.map((b) => <SelectItem key={b.key}>{b.label}</SelectItem>)}
          </Select>
          {apiKeys.length > 0 && (
            <Select aria-label="Token" selectedKeys={selectedKeyId ? [selectedKeyId] : []} onChange={(e) => setSelectedKeyId(e.target.value)} size="sm" className="w-44" placeholder="Todos os tokens">
              {apiKeys.map((k) => <SelectItem key={k.id}>{k.name}</SelectItem>)}
            </Select>
          )}
        </div>
      </div>

      {/* Top stats — 4 most important numbers */}
      <div className="grid grid-cols-2 lg:grid-cols-4 gap-4">
        <StatCard label="Requests" value={formatCompact(stats.requests)} sub="total no período" full={stats.requests.toLocaleString("en-US")} />
        <StatCard label="Tokens" value={formatCompact(totalTokens)} sub={`${formatCompact(stats.prompt_tokens)} in · ${formatCompact(stats.completion_tokens)} out`} full={totalTokens.toLocaleString("en-US")} />
        <StatCard label="Custo" value={formatCost(stats.cost)} sub={stats.cost_per_request > 0 ? `${formatCost(stats.cost_per_request)}/req` : "—"} full={`$${stats.cost.toFixed(6)}`} />
        <StatCard label="Economia" value={formatCost(stats.cost_saved)} sub={`${formatCompact(stats.tokens_saved)} tokens poupados`} full={`$${stats.cost_saved.toFixed(6)}`} color="text-success" />
      </div>

      {/* Time series — main chart, tabbed metric */}
      <Card className="border border-default-100">
        <CardHeader className="flex items-center justify-between gap-3 flex-wrap pb-0">
          <div>
            <h3 className="font-semibold">Série temporal</h3>
            <p className="text-xs text-default-500">Volume por {bucketLabel[activeBucket] || "período"}</p>
          </div>
          <Tabs size="sm" variant="underlined" selectedKey={chartMetric} onSelectionChange={(k) => setChartMetric(k as string)}>
            {chartMetrics.map((m) => <Tab key={m.key} title={m.label} />)}
          </Tabs>
        </CardHeader>
        <CardBody>
          <ResponsiveContainer width="100%" height={300}>
            <AreaChart data={daily} margin={{ left: -16, right: 8, top: 8 }}>
              <defs>
                <linearGradient id="gradChart" x1="0" y1="0" x2="0" y2="1">
                  <stop offset="5%" stopColor={activeMetric.color} stopOpacity={0.5} />
                  <stop offset="95%" stopColor={activeMetric.color} stopOpacity={0.02} />
                </linearGradient>
              </defs>
              <CartesianGrid strokeDasharray="3 3" stroke="#2a2a2a" vertical={false} />
              <XAxis dataKey="label" stroke="#666" tick={{ fontSize: 11 }} tickLine={false} axisLine={false} interval="preserveStartEnd" minTickGap={20} />
              <YAxis stroke="#666" tick={{ fontSize: 12 }} tickLine={false} axisLine={false} tickFormatter={activeMetric.yFmt} />
              <RTooltip contentStyle={tooltipStyle} itemStyle={itemStyle} labelStyle={{ color: "#888" }} formatter={(v: number) => [activeMetric.fmt(v), activeMetric.label]} />
              <Area type="monotone" dataKey={chartMetric} stroke={activeMetric.color} strokeWidth={2} fill="url(#gradChart)" />
            </AreaChart>
          </ResponsiveContainer>
        </CardBody>
      </Card>

      {/* Performance + reliability — visual gauges + key numbers */}
      <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
        {/* Performance card */}
        <Card className="border border-default-100">
          <CardHeader><h3 className="font-semibold">Performance</h3></CardHeader>
          <CardBody className="space-y-4">
            <div className="grid grid-cols-2 gap-4">
              <Metric label="TTFT médio" value={stats.avg_ttft_ms > 0 ? `${stats.avg_ttft_ms}ms` : "—"} />
              <Metric label="TPS médio" value={stats.avg_tps > 0 ? stats.avg_tps.toFixed(1) : "—"} suffix="tok/s" />
              <Metric label="Latência média" value={stats.avg_latency_ms > 0 ? `${stats.avg_latency_ms}ms` : "—"} />
              <Metric label="P95 / P99" value={stats.p95_latency_ms > 0 ? `${stats.p95_latency_ms} / ${stats.p99_latency_ms}ms` : "—"} />
            </div>
            {stats.p50_latency_ms > 0 && (
              <div>
                <div className="flex justify-between text-xs text-default-500 mb-1">
                  <span>Distribuição de latência (P50 → P99)</span>
                </div>
                <div className="flex items-center gap-1 h-8">
                  <BarMini value={stats.p50_latency_ms} max={stats.p99_latency_ms} color="#00C2A8" label="P50" />
                  <BarMini value={stats.p95_latency_ms} max={stats.p99_latency_ms} color="#FFB347" label="P95" />
                  <BarMini value={stats.p99_latency_ms} max={stats.p99_latency_ms} color="#FF6B6B" label="P99" />
                </div>
              </div>
            )}
          </CardBody>
        </Card>

        {/* Reliability card */}
        <Card className="border border-default-100">
          <CardHeader><h3 className="font-semibold">Confiabilidade</h3></CardHeader>
          <CardBody className="space-y-4">
            <GaugeBar
              label="Taxa de erro"
              value={errorPct}
              color={errorPct > 5 ? "danger" : "success"}
              display={`${errorPct.toFixed(1)}%`}
              sub={`${stats.error_requests.toLocaleString("en-US")} de ${stats.requests.toLocaleString("en-US")}`}
            />
            <GaugeBar
              label="Cache hit rate"
              value={cachePct}
              color="primary"
              display={`${cachePct.toFixed(1)}%`}
              sub={`${stats.cache_hits.toLocaleString("en-US")} hits`}
            />
            <div className="grid grid-cols-2 gap-4 pt-2">
              <Metric label="Requests OK" value={stats.successful_requests.toLocaleString("en-US")} color="text-success" />
              <Metric label="Combo requests" value={stats.combo_requests.toLocaleString("en-US")} sub={stats.requests > 0 ? `${((stats.combo_requests / stats.requests) * 100).toFixed(0)}% do total` : ""} />
            </div>
          </CardBody>
        </Card>
      </div>

      {/* Efficiency — cost insights */}
      <Card className="border border-default-100">
        <CardHeader><h3 className="font-semibold">Eficiência</h3></CardHeader>
        <CardBody>
          <div className="grid grid-cols-2 md:grid-cols-4 gap-4">
            <Metric label="Custo por request" value={stats.cost_per_request > 0 ? `$${stats.cost_per_request.toFixed(6)}` : "—"} />
            <Metric label="Tokens por $" value={stats.tokens_per_dollar > 0 ? formatCompact(stats.tokens_per_dollar) : "—"} suffix={stats.tokens_per_dollar > 0 ? "tok/$" : ""} />
            <Metric label="Custo / 1k tokens" value={stats.tokens_per_dollar > 0 ? `$${(1000 / stats.tokens_per_dollar).toFixed(4)}` : "—"} />
            <Metric label="Tokens / request" value={stats.requests > 0 ? formatCompact(totalTokens / stats.requests) : "—"} />
          </div>
        </CardBody>
      </Card>

      {/* Savings — cache + RTK visual breakdown */}
      {savings && (
        <Card className="border border-default-100">
          <CardHeader>
            <div>
              <h3 className="font-semibold">Economia</h3>
              <p className="text-xs text-default-500">Tokens e custos economizados por Response Cache e RTK</p>
            </div>
          </CardHeader>
          <CardBody className="space-y-5">
            <div className="grid grid-cols-2 md:grid-cols-4 gap-4">
              <SavingsMetric label="Cache hits" value={formatCompact(savings.cache_hits)} sub={`${formatCost(savings.cache_cost_saved)} poupado`} color="#00C2A8" />
              <SavingsMetric label="Tokens (cache)" value={formatCompact(savings.cache_tokens_saved)} sub="poupados" color="#00C2A8" />
              <SavingsMetric label="Compressões RTK" value={formatCompact(savings.rtk_compressions)} sub="tool_results" color="#4DA3FF" />
              <SavingsMetric label="Tokens (RTK)" value={formatCompact(savings.rtk_tokens_saved)} sub={`${formatCost(savings.rtk_cost_saved)} poupado`} color="#4DA3FF" />
            </div>
            {(savings.cache_cost_saved + savings.rtk_cost_saved) > 0 && (
              <div className="flex items-center gap-6 pt-2 border-t border-default-100">
                <div className="flex items-center gap-2">
                  <span className="text-sm text-default-500">Total economizado:</span>
                  <Chip color="success" variant="flat" size="lg" className="font-bold">
                    {formatCost(savings.cache_cost_saved + savings.rtk_cost_saved)}
                  </Chip>
                </div>
                <div className="flex items-center gap-2">
                  <span className="text-sm text-default-500">Tokens:</span>
                  <span className="text-lg font-bold text-success tabular-nums">
                    {formatCompact(savings.cache_tokens_saved + savings.rtk_tokens_saved)}
                  </span>
                </div>
              </div>
            )}
          </CardBody>
        </Card>
      )}

      {/* Distributions — charts in 2-col grid */}
      <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
        {byProvider.length > 0 && (
          <Card className="border border-default-100">
            <CardHeader><div><h3 className="font-semibold">Por provider</h3><p className="text-xs text-default-500">Distribuição de requisições</p></div></CardHeader>
            <CardBody>
              <ResponsiveContainer width="100%" height={260}>
                <PieChart>
                  <Pie data={byProvider} dataKey="value" nameKey="name" cx="50%" cy="50%" innerRadius={55} outerRadius={90} paddingAngle={2}
                    label={(e: any) => <text x={e.x} y={e.y} fill="#aaa" fontSize={11} textAnchor={e.x > e.cx ? "start" : "end"} dominantBaseline="central">{e.name}</text>}
                    labelLine={{ stroke: "#666" }}>
                    {byProvider.map((_, i) => <Cell key={i} fill={PIE_COLORS[i % PIE_COLORS.length]} stroke="none" />)}
                  </Pie>
                  <Legend formatter={(v) => <span className="text-xs text-default-600">{v}</span>} />
                  <RTooltip contentStyle={tooltipStyle} itemStyle={itemStyle} labelStyle={{ color: "#aaa" }} />
                </PieChart>
              </ResponsiveContainer>
            </CardBody>
          </Card>
        )}

        {byModel.length > 0 && (
          <Card className="border border-default-100">
            <CardHeader><div><h3 className="font-semibold">Por modelo</h3><p className="text-xs text-default-500">Requisições por modelo</p></div></CardHeader>
            <CardBody>
              <ResponsiveContainer width="100%" height={Math.max(260, byModel.length * 26)}>
                <BarChart data={byModel} layout="vertical" margin={{ left: 20, right: 8, top: 8 }}>
                  <CartesianGrid strokeDasharray="3 3" stroke="#2a2a2a" horizontal={false} />
                  <XAxis type="number" stroke="#666" tick={{ fontSize: 12 }} tickLine={false} axisLine={false} allowDecimals={false} tickFormatter={formatCompact} />
                  <YAxis type="category" dataKey="name" stroke="#666" tick={{ fontSize: 11 }} tickLine={false} axisLine={false} width={100} />
                  <RTooltip contentStyle={tooltipStyle} itemStyle={itemStyle} cursor={{ fill: "#ffffff10" }} formatter={(v: number) => [v.toLocaleString("en-US"), "Requests"]} />
                  <Bar dataKey="value" fill="#4DA3FF" radius={[0, 4, 4, 0]} barSize={18} />
                </BarChart>
              </ResponsiveContainer>
            </CardBody>
          </Card>
        )}

        {byModelCost.length > 0 && (
          <Card className="border border-default-100">
            <CardHeader><div><h3 className="font-semibold">Custo por modelo</h3><p className="text-xs text-default-500">Gasto em USD</p></div></CardHeader>
            <CardBody>
              <ResponsiveContainer width="100%" height={Math.max(260, byModelCost.length * 26)}>
                <BarChart data={byModelCost} layout="vertical" margin={{ left: 20, right: 8, top: 8 }}>
                  <CartesianGrid strokeDasharray="3 3" stroke="#2a2a2a" horizontal={false} />
                  <XAxis type="number" stroke="#666" tick={{ fontSize: 12 }} tickLine={false} axisLine={false} tickFormatter={formatCost} />
                  <YAxis type="category" dataKey="name" stroke="#666" tick={{ fontSize: 11 }} tickLine={false} axisLine={false} width={100} />
                  <RTooltip contentStyle={tooltipStyle} itemStyle={itemStyle} cursor={{ fill: "#ffffff10" }} formatter={(v: number) => [`$${v.toFixed(6)}`, "Custo"]} />
                  <Bar dataKey="value" fill="#FFB347" radius={[0, 4, 4, 0]} barSize={18} />
                </BarChart>
              </ResponsiveContainer>
            </CardBody>
          </Card>
        )}

        {byCombo.length > 0 && (
          <Card className="border border-default-100">
            <CardHeader><div><h3 className="font-semibold">Por combo</h3><p className="text-xs text-default-500">Distribuição entre combos</p></div></CardHeader>
            <CardBody>
              <ResponsiveContainer width="100%" height={Math.max(260, byCombo.length * 26)}>
                <BarChart data={byCombo} layout="vertical" margin={{ left: 20, right: 8, top: 8 }}>
                  <CartesianGrid strokeDasharray="3 3" stroke="#2a2a2a" horizontal={false} />
                  <XAxis type="number" stroke="#666" tick={{ fontSize: 12 }} tickLine={false} axisLine={false} allowDecimals={false} tickFormatter={formatCompact} />
                  <YAxis type="category" dataKey="name" stroke="#666" tick={{ fontSize: 11 }} tickLine={false} axisLine={false} width={100} />
                  <RTooltip contentStyle={tooltipStyle} itemStyle={itemStyle} cursor={{ fill: "#ffffff10" }} formatter={(v: number) => [v.toLocaleString("en-US"), "Requests"]} />
                  <Bar dataKey="value" fill="#B266FF" radius={[0, 4, 4, 0]} barSize={18} />
                </BarChart>
              </ResponsiveContainer>
            </CardBody>
          </Card>
        )}

        {byApiKey.length > 0 && (
          <Card className="border border-default-100">
            <CardHeader><div><h3 className="font-semibold">Por token</h3><p className="text-xs text-default-500">Requisições por API key</p></div></CardHeader>
            <CardBody>
              <ResponsiveContainer width="100%" height={Math.max(260, byApiKey.length * 26)}>
                <BarChart data={byApiKey} layout="vertical" margin={{ left: 20, right: 8, top: 8 }}>
                  <CartesianGrid strokeDasharray="3 3" stroke="#2a2a2a" horizontal={false} />
                  <XAxis type="number" stroke="#666" tick={{ fontSize: 12 }} tickLine={false} axisLine={false} allowDecimals={false} tickFormatter={formatCompact} />
                  <YAxis type="category" dataKey="name" stroke="#666" tick={{ fontSize: 11 }} tickLine={false} axisLine={false} width={100} />
                  <RTooltip contentStyle={tooltipStyle} itemStyle={itemStyle} cursor={{ fill: "#ffffff10" }} formatter={(v: number) => [v.toLocaleString("en-US"), "Requests"]} />
                  <Bar dataKey="value" fill="#6BCB77" radius={[0, 4, 4, 0]} barSize={18} />
                </BarChart>
              </ResponsiveContainer>
            </CardBody>
          </Card>
        )}
      </div>

      {/* System status — compact footer with chips */}
      {status && (
        <Card className="border border-default-100">
          <CardHeader><h3 className="font-semibold">Sistema</h3></CardHeader>
          <CardBody>
            <div className="flex flex-wrap gap-3">
              <Chip variant="flat" size="lg">
                <span className="text-default-500 mr-1">Combos:</span>
                <b>{status.combos.total}</b>
              </Chip>
              <Chip variant="flat" size="lg" color={status.connections.rate_limited > 0 ? "warning" : "default"}>
                <span className="text-default-500 mr-1">Conexões:</span>
                <b>{status.connections.active}/{status.connections.total}</b>
                {status.connections.rate_limited > 0 && <span className="text-warning ml-1">· {status.connections.rate_limited} rate-limited</span>}
              </Chip>
              <Chip variant="flat" size="lg" color={status.health.unhealthy > 0 ? "danger" : "success"}>
                <span className="text-default-500 mr-1">Saúde:</span>
                <b>{status.health.unhealthy > 0 ? `${status.health.unhealthy} unhealthy` : "OK"}</b>
                {status.health.probing > 0 && <span className="text-default-500 ml-1">· {status.health.probing} probing</span>}
              </Chip>
              <Chip variant="flat" size="lg">
                <span className="text-default-500 mr-1">Tokens:</span>
                <b>{apiKeys.filter(k => k.is_active).length}</b>
                <span className="text-default-500 ml-1">/ {apiKeys.length}</span>
              </Chip>
            </div>
          </CardBody>
        </Card>
      )}
    </div>
  );
}

// ---- Reusable display components ----

function StatCard({ label, value, sub, full, color }: { label: string; value: string | number; sub: string; full?: string; color?: string }) {
  return (
    <Card className="border border-default-100 hover:border-default-200 transition-colors">
      <CardBody className="p-5">
        <p className="text-xs text-default-500 uppercase tracking-wide font-medium">{label}</p>
        <p className={`text-3xl font-bold mt-2 tabular-nums ${color || ""}`} title={full}>{value}</p>
        <p className="text-xs text-default-500 mt-1">{sub}</p>
      </CardBody>
    </Card>
  );
}

function Metric({ label, value, sub, suffix, color }: { label: string; value: string | number; sub?: string; suffix?: string; color?: string }) {
  return (
    <div>
      <p className="text-xs text-default-500 uppercase tracking-wide font-medium">{label}</p>
      <p className={`text-xl font-bold mt-1 tabular-nums ${color || ""}`}>
        {value}{suffix && <span className="text-xs text-default-400 ml-1">{suffix}</span>}
      </p>
      {sub && <p className="text-[11px] text-default-400 mt-0.5">{sub}</p>}
    </div>
  );
}

function SavingsMetric({ label, value, sub, color }: { label: string; value: string; sub: string; color: string }) {
  return (
    <div className="flex items-start gap-3">
      <span className="w-2.5 h-2.5 rounded-full mt-1.5 shrink-0" style={{ backgroundColor: color }} />
      <div>
        <p className="text-xs text-default-500 uppercase tracking-wide font-medium">{label}</p>
        <p className="text-2xl font-bold tabular-nums mt-0.5">{value}</p>
        <p className="text-[11px] text-default-400">{sub}</p>
      </div>
    </div>
  );
}

function GaugeBar({ label, value, color, display, sub }: { label: string; value: number; color: "danger" | "success" | "primary"; display: string; sub: string }) {
  return (
    <div>
      <div className="flex justify-between items-baseline mb-1.5">
        <span className="text-sm text-default-500">{label}</span>
        <span className={`text-lg font-bold tabular-nums ${color === "danger" ? "text-danger" : color === "success" ? "text-success" : "text-primary"}`}>{display}</span>
      </div>
      <Progress
        aria-label={label}
        size="md"
        color={color}
        value={Math.min(value, 100)}
        className="max-w-full"
      />
      <p className="text-[11px] text-default-400 mt-1">{sub}</p>
    </div>
  );
}

function BarMini({ value, max, color, label }: { value: number; max: number; color: string; label: string }) {
  const pct = max > 0 ? (value / max) * 100 : 0;
  return (
    <div className="flex-1 flex flex-col items-center gap-1">
      <div className="w-full h-6 bg-default-100 rounded relative overflow-hidden">
        <div className="absolute bottom-0 w-full rounded transition-all" style={{ height: `${pct}%`, backgroundColor: color, opacity: 0.8 }} />
      </div>
      <span className="text-[10px] text-default-500">{label}</span>
    </div>
  );
}

// ---- helpers ----

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