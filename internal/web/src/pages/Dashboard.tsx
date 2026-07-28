import { useEffect, useState } from "react";
import {
  Spinner, Select, SelectItem, Popover, PopoverTrigger, PopoverContent,
  Input, Button, Card, CardBody, CardHeader, Tabs, Tab,
  Table, TableHeader, TableColumn, TableBody, TableRow, TableCell,
  Chip, Progress,
} from "@heroui/react";
import {
  ResponsiveContainer, AreaChart, Area, XAxis, YAxis, Tooltip as RTooltip,
  CartesianGrid, BarChart, Bar, PieChart, Pie, Cell, Legend,
} from "recharts";
import { api, type UsageStats, type SavingsStats, type ApiKey, type StatusSnapshot, type ModelStat } from "../api";
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
  const [modelStats, setModelStats] = useState<Record<string, ModelStat>>({});
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
      api.models.stats().catch(() => ({})),
    ])
      .then(([s, sv, st, ms]) => { setStats(s); setSavings(sv); setStatus(st); setModelStats(ms); })
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

  // Build per-model performance table rows: merge by_model (request counts)
  // with modelStats (TTFT, TPS, latency).
  const perfRows = byModel.map((m) => {
    const ms = modelStats[m.name];
    const cost = stats.by_model_cost?.[m.name] || 0;
    return {
      name: m.name,
      requests: m.value,
      ttft: ms?.avg_ttft_ms,
      tps: ms?.avg_tps,
      latency: ms?.avg_latency_ms,
      cost,
      costPerReq: m.value > 0 ? cost / m.value : 0,
      tokensPerReq: m.value > 0 ? (totalTokens / stats.requests) * (m.value / stats.requests) : 0,
    };
  });

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

      {/* Top stats */}
      <div className="grid grid-cols-2 lg:grid-cols-4 gap-4">
        <StatCard label="Requests" value={formatCompact(stats.requests)} sub="total no período" full={stats.requests.toLocaleString("en-US")} />
        <StatCard label="Tokens" value={formatCompact(totalTokens)} sub={`${formatCompact(stats.prompt_tokens)} in · ${formatCompact(stats.completion_tokens)} out`} full={totalTokens.toLocaleString("en-US")} />
        <StatCard label="Custo" value={formatCost(stats.cost)} sub={stats.cost_per_request > 0 ? `${formatCost(stats.cost_per_request)}/req` : "—"} full={`$${stats.cost.toFixed(6)}`} />
        <StatCard label="Economia" value={formatCost(stats.cost_saved)} sub={`${formatCompact(stats.tokens_saved)} tokens poupados`} full={`$${stats.cost_saved.toFixed(6)}`} />
      </div>

      {/* Time series */}
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
                  <stop offset="5%" stopColor={activeMetric.color} stopOpacity={0.4} />
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

      {/* Performance por modelo — table with TTFT, TPS, latency, cost per request */}
      {perfRows.length > 0 && (
        <Card className="border border-default-100">
          <CardHeader>
            <div>
              <h3 className="font-semibold">Performance por modelo</h3>
              <p className="text-xs text-default-500">TTFT, TPS, latência e custo por request</p>
            </div>
          </CardHeader>
          <CardBody>
            <Table removeWrapper aria-label="performance por modelo" className="text-sm">
              <TableHeader>
                <TableColumn>MODELO</TableColumn>
                <TableColumn align="end">REQUESTS</TableColumn>
                <TableColumn align="end">TTFT</TableColumn>
                <TableColumn align="end">TPS</TableColumn>
                <TableColumn align="end">LATÊNCIA</TableColumn>
                <TableColumn align="end">CUSTO/REQ</TableColumn>
                <TableColumn align="end">CUSTO TOTAL</TableColumn>
              </TableHeader>
              <TableBody items={perfRows}>
                {(r) => (
                  <TableRow key={r.name}>
                    <TableCell><code className="text-xs">{r.name}</code></TableCell>
                    <TableCell className="text-right tabular-nums">{r.requests.toLocaleString("en-US")}</TableCell>
                    <TableCell className="text-right tabular-nums">{r.ttft ? `${r.ttft}ms` : "—"}</TableCell>
                    <TableCell className="text-right tabular-nums">{r.tps ? r.tps.toFixed(1) : "—"}</TableCell>
                    <TableCell className="text-right tabular-nums">{r.latency ? `${r.latency}ms` : "—"}</TableCell>
                    <TableCell className="text-right tabular-nums">{r.costPerReq > 0 ? formatCost(r.costPerReq) : "—"}</TableCell>
                    <TableCell className="text-right tabular-nums">{r.cost > 0 ? formatCost(r.cost) : "—"}</TableCell>
                  </TableRow>
                )}
              </TableBody>
            </Table>
          </CardBody>
        </Card>
      )}

      {/* Confiabilidade — taxa de erro + requests OK + combo */}
      <Card className="border border-default-100">
        <CardHeader><h3 className="font-semibold">Confiabilidade</h3></CardHeader>
        <CardBody className="space-y-5">
          <div>
            <div className="flex justify-between items-baseline mb-2">
              <span className="text-sm text-default-500">Taxa de erro</span>
              <span className="text-lg font-semibold tabular-nums">{errorPct.toFixed(1)}%</span>
            </div>
            <Progress
              aria-label="Taxa de erro"
              size="md"
              color={errorPct > 5 ? "danger" : "success"}
              value={Math.min(errorPct, 100)}
            />
            <p className="text-xs text-default-400 mt-1.5">
              {stats.error_requests.toLocaleString("en-US")} erros de {stats.requests.toLocaleString("en-US")}
            </p>
          </div>
          <div className="grid grid-cols-2 gap-4">
            <div>
              <p className="text-xs text-default-500 uppercase tracking-wide font-medium">Requests OK</p>
              <p className="text-2xl font-semibold mt-1 tabular-nums">{stats.successful_requests.toLocaleString("en-US")}</p>
            </div>
            <div>
              <p className="text-xs text-default-500 uppercase tracking-wide font-medium">Combo requests</p>
              <p className="text-2xl font-semibold mt-1 tabular-nums">{stats.combo_requests.toLocaleString("en-US")}</p>
              <p className="text-xs text-default-400 mt-0.5">
                {stats.requests > 0 ? `${((stats.combo_requests / stats.requests) * 100).toFixed(0)}% do total` : ""}
              </p>
            </div>
          </div>
        </CardBody>
      </Card>

      {/* Economia — card pai com grid interno + total */}
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
              <SavingsCard label="Cache hits" value={formatCompact(savings.cache_hits)} sub="respostas do cache" full={savings.cache_hits.toLocaleString("en-US")} dot="#00C2A8" />
              <SavingsCard label="Tokens (cache)" value={formatCompact(savings.cache_tokens_saved)} sub={formatCost(savings.cache_cost_saved)} full={savings.cache_tokens_saved.toLocaleString("en-US")} dot="#00C2A8" />
              <SavingsCard label="Compressões RTK" value={formatCompact(savings.rtk_compressions)} sub="tool_results" full={savings.rtk_compressions.toLocaleString("en-US")} dot="#4DA3FF" />
              <SavingsCard label="Tokens (RTK)" value={formatCompact(savings.rtk_tokens_saved)} sub={formatCost(savings.rtk_cost_saved)} full={savings.rtk_tokens_saved.toLocaleString("en-US")} dot="#4DA3FF" />
            </div>
            {(savings.cache_cost_saved + savings.rtk_cost_saved) > 0 && (
              <div className="flex items-baseline gap-8 pt-2 border-t border-default-100">
                <div className="flex items-baseline gap-2">
                  <span className="text-sm text-default-500">Total economizado</span>
                  <span className="text-lg font-semibold tabular-nums">{formatCost(savings.cache_cost_saved + savings.rtk_cost_saved)}</span>
                </div>
                <div className="flex items-baseline gap-2">
                  <span className="text-sm text-default-500">Tokens</span>
                  <span className="text-lg font-semibold tabular-nums">{formatCompact(savings.cache_tokens_saved + savings.rtk_tokens_saved)}</span>
                </div>
              </div>
            )}
          </CardBody>
        </Card>
      )}

      {/* Distributions */}
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

      {/* Sistema — chips neutros */}
      {status && (
        <Card className="border border-default-100">
          <CardHeader><h3 className="font-semibold">Sistema</h3></CardHeader>
          <CardBody>
            <div className="flex flex-wrap gap-3">
              <Chip variant="flat" size="lg">
                Combos: <b className="ml-1">{status.combos.total}</b>
              </Chip>
              <Chip variant="flat" size="lg" color={status.connections.rate_limited > 0 ? "warning" : "default"}>
                Conexões: <b className="ml-1">{status.connections.active}/{status.connections.total}</b>
                {status.connections.rate_limited > 0 && <span className="ml-1">· {status.connections.rate_limited} rate-limited</span>}
              </Chip>
              <Chip variant="flat" size="lg" color={status.health.unhealthy > 0 ? "danger" : "success"}>
                Saúde: <b className="ml-1">{status.health.unhealthy > 0 ? `${status.health.unhealthy} unhealthy` : "OK"}</b>
                {status.health.probing > 0 && <span className="ml-1">· {status.health.probing} probing</span>}
              </Chip>
              <Chip variant="flat" size="lg">
                Tokens: <b className="ml-1">{apiKeys.filter(k => k.is_active).length}</b>
                <span className="text-default-400 ml-1">/ {apiKeys.length}</span>
              </Chip>
            </div>
          </CardBody>
        </Card>
      )}
    </div>
  );
}

// ---- Components ----

function StatCard({ label, value, sub, full }: { label: string; value: string | number; sub: string; full?: string }) {
  return (
    <Card className="border border-default-100 hover:border-default-200 transition-colors">
      <CardBody className="p-5">
        <p className="text-xs text-default-500 uppercase tracking-wide font-medium">{label}</p>
        <p className="text-3xl font-bold mt-2 tabular-nums" title={full}>{value}</p>
        <p className="text-xs text-default-500 mt-1">{sub}</p>
      </CardBody>
    </Card>
  );
}

function SavingsCard({ label, value, sub, full, dot }: { label: string; value: string; sub: string; full?: string; dot: string }) {
  return (
    <div className="rounded-xl border border-default-100 p-5">
      <div className="flex items-center gap-2">
        <span className="w-2 h-2 rounded-full" style={{ backgroundColor: dot }} />
        <p className="text-xs text-default-500 uppercase tracking-wide font-medium">{label}</p>
      </div>
      <p className="text-2xl font-bold mt-2 tabular-nums" title={full}>{value}</p>
      <p className="text-xs text-default-500 mt-1">{sub}</p>
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