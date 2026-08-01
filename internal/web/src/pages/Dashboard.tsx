import { useEffect, useState } from "react";
import {
  Spinner, Select, ListBox, Popover, Button, Card, Tabs,
  Table, ProgressBar, Separator,
  DateRangePicker, DateField, RangeCalendar,
} from "@heroui/react";
import type { CalendarDateTime } from "@internationalized/date";
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
  const [dateRange, setDateRange] = useState<{ start: CalendarDateTime; end: CalendarDateTime } | null>(null);
  const [loading, setLoading] = useState(true);
  const [apiKeys, setApiKeys] = useState<ApiKey[]>([]);
  const [selectedKeyId, setSelectedKeyId] = useState<string>("");
  const [chartMetric, setChartMetric] = useState("requests");

  useEffect(() => { api.keys.list().then(setApiKeys).catch(() => {}); }, []);

  useEffect(() => {
    setLoading(true);
    const params: { period?: string; from?: string; to?: string; bucket?: string; api_key_id?: string } = {};
    if (customMode && dateRange) {
      params.from = new Date(dateRange.start.toString()).toISOString();
      params.to = new Date(dateRange.end.toString()).toISOString();
      if (bucket) params.bucket = bucket;
    } else {
      params.period = period;
      if (bucket) params.bucket = bucket;
    }
    if (selectedKeyId) params.api_key_id = selectedKeyId;
    Promise.all([
      api.usage.stats(params),
      api.savings.stats(customMode && dateRange ? "60d" : period, selectedKeyId).catch(() => null),
      api.status().catch(() => null),
      api.models.stats().catch(() => ({})),
    ])
      .then(([s, sv, st, ms]) => { setStats(s); setSavings(sv); setStatus(st); setModelStats(ms); })
      .catch(() => setStats(null))
      .finally(() => setLoading(false));
  }, [period, bucket, customMode, dateRange, selectedKeyId]);

  if (loading) return <div className="flex justify-center py-20"><Spinner /></div>;
  if (!stats) return (
    <div className="text-center py-20 text-muted">
      Não há dados de uso ainda. Crie um provider e faça uma requisição.
    </div>
  );

  const activeBucket = stats.bucket || "day";
  const daily = stats.daily.map((d) => ({ ...d, label: formatBucketLabel(d.date, activeBucket) }));
  const byProvider = Object.entries(stats.by_provider).map(([name, value]) => ({ name, value }));
  const byModel = Object.entries(stats.by_model).map(([name, value]) => ({ name, value })).sort((a, b) => b.value - a.value);
  const byModelCost = Object.entries(stats.by_model_cost || {}).map(([name, value]) => ({ name, value })).sort((a, b) => b.value - a.value);
  const byCombo = Object.entries(stats.by_combo || {}).map(([name, value]) => ({ name, value })).sort((a, b) => b.value - a.value);
  const byComboTokens = Object.entries(stats.by_combo_tokens || {}).map(([name, value]) => ({ name, value })).sort((a, b) => b.value - a.value);
  const byApiKey = Object.entries(stats.by_api_key || {}).map(([name, value]) => ({ name: nameKey(name, apiKeys), value })).sort((a, b) => b.value - a.value);

  const totalTokens = stats.prompt_tokens + stats.completion_tokens;
  const errorPct = stats.error_rate * 100;

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
      <div className="flex items-center justify-between flex-wrap gap-3">
        <div>
          <h1 className="text-2xl font-bold tracking-tight">Visão geral</h1>
          <p className="text-sm text-muted mt-0.5">
            {stats.requests.toLocaleString("en-US")} requisições no período
          </p>
        </div>
        <div className="flex items-center gap-2 flex-wrap">
          <div className="flex bg-surface rounded-lg p-0.5 border border-border">
            {periods.map((p) => (
              <button
                key={p.key}
                onClick={() => { setPeriod(p.key); setCustomMode(false); }}
                className={`px-3 py-1.5 text-sm rounded-md transition-colors ${
                  !customMode && period === p.key ? "bg-accent text-white" : "text-foreground/80 hover:bg-default-soft"
                }`}
              >
                {p.label}
              </button>
            ))}
          </div>
          <Popover>
            <Popover.Trigger>
              <Button size="sm" variant={customMode ? "primary" : "secondary"} onPress={() => setCustomMode(true)}>
                <IconCalendar className="w-4 h-4" />
                {formatDateRangeLabel(customMode ? dateRange : null)}
              </Button>
            </Popover.Trigger>
            <Popover.Content placement="bottom" className="p-3">
              <div className="space-y-3 w-80">
                <DateRangePicker
                  aria-label="Período personalizado"
                  className="w-full"
                  startName="startDate"
                  endName="endDate"
                  value={dateRange}
                  onChange={(v) => { setDateRange(v); setCustomMode(true); }}
                >
                  <DateField.Group fullWidth>
                    <DateField.Input slot="start">
                      {(segment) => <DateField.Segment segment={segment} />}
                    </DateField.Input>
                    <DateRangePicker.RangeSeparator />
                    <DateField.Input slot="end">
                      {(segment) => <DateField.Segment segment={segment} />}
                    </DateField.Input>
                    <DateField.Suffix>
                      <DateRangePicker.Trigger>
                        <DateRangePicker.TriggerIndicator />
                      </DateRangePicker.Trigger>
                    </DateField.Suffix>
                  </DateField.Group>
                  <DateRangePicker.Popover>
                    <RangeCalendar aria-label="Período personalizado">
                      <RangeCalendar.Header>
                        <RangeCalendar.YearPickerTrigger>
                          <RangeCalendar.YearPickerTriggerHeading />
                          <RangeCalendar.YearPickerTriggerIndicator />
                        </RangeCalendar.YearPickerTrigger>
                        <RangeCalendar.NavButton slot="previous" />
                        <RangeCalendar.NavButton slot="next" />
                      </RangeCalendar.Header>
                      <RangeCalendar.Grid>
                        <RangeCalendar.GridHeader>
                          {(day) => <RangeCalendar.HeaderCell>{day}</RangeCalendar.HeaderCell>}
                        </RangeCalendar.GridHeader>
                        <RangeCalendar.GridBody>
                          {(date) => <RangeCalendar.Cell date={date} />}
                        </RangeCalendar.GridBody>
                      </RangeCalendar.Grid>
                      <RangeCalendar.YearPickerGrid>
                        <RangeCalendar.YearPickerGridBody>
                          {({year}) => <RangeCalendar.YearPickerCell year={year} />}
                        </RangeCalendar.YearPickerGridBody>
                      </RangeCalendar.YearPickerGrid>
                    </RangeCalendar>
                  </DateRangePicker.Popover>
                </DateRangePicker>
                <Button size="sm" variant="primary" className="w-full" onPress={() => setCustomMode(true)}>Aplicar</Button>
                {customMode && (
                  <Button size="sm" variant="secondary" className="w-full" onPress={() => { setCustomMode(false); setDateRange(null); }}>
                    Voltar para presets
                  </Button>
                )}
              </div>
            </Popover.Content>
          </Popover>
          {apiKeys.length > 0 && (
            <Select
              aria-label="Token"
              selectedKey={selectedKeyId || null}
              onSelectionChange={(k) => setSelectedKeyId((k as string) ?? "")}
              className="w-44"
            >
              <Select.Trigger>
                <Select.Value>{selectedKeyId ? apiKeys.find((k) => k.id === selectedKeyId)?.name ?? "Token" : "Todos os tokens"}</Select.Value>
                <Select.Indicator />
              </Select.Trigger>
              <Select.Popover>
                <ListBox>
                  <ListBox.Item id="">Todos os tokens</ListBox.Item>
                  {apiKeys.map((k) => <ListBox.Item key={k.id} id={k.id}>{k.name}</ListBox.Item>)}
                </ListBox>
              </Select.Popover>
            </Select>
          )}
        </div>
      </div>

      <div className="grid grid-cols-2 lg:grid-cols-4 gap-4">
        <StatCard label="Requests" value={formatCompact(stats.requests)} sub="total no período" full={stats.requests.toLocaleString("en-US")} />
        <StatCard label="Tokens" value={formatCompact(totalTokens)} sub={`${formatCompact(stats.prompt_tokens)} in · ${formatCompact(stats.completion_tokens)} out`} full={totalTokens.toLocaleString("en-US")} />
        <StatCard label="Custo" value={formatCost(stats.cost)} sub={stats.cost_per_request > 0 ? `${formatCost(stats.cost_per_request)}/req` : "—"} full={`$${stats.cost.toFixed(6)}`} />
        <StatCard label="Economia" value={formatCost(stats.cost_saved)} sub={`${formatCompact(stats.tokens_saved)} tokens poupados`} full={`$${stats.cost_saved.toFixed(6)}`} />
      </div>

      <Card className="border border-border">
        <Card.Header className="flex items-center justify-between gap-3 flex-wrap pb-0">
          <div>
            <h3 className="font-semibold">Volume por {bucketLabel[activeBucket] || "período"}</h3>
            <p className="text-xs text-muted">Série temporal</p>
          </div>
          <div className="flex items-center gap-2">
            <Select
              aria-label="Granularidade"
              selectedKey={bucket}
              onSelectionChange={(k) => setBucket((k as string) ?? "")}
              className="w-32"
            >
              <Select.Trigger>
                <Select.Value>{buckets.find((b) => b.key === bucket)?.label ?? "Auto"}</Select.Value>
                <Select.Indicator />
              </Select.Trigger>
              <Select.Popover>
                <ListBox>
                  {buckets.map((b) => <ListBox.Item key={b.key} id={b.key}>{b.label}</ListBox.Item>)}
                </ListBox>
              </Select.Popover>
            </Select>
            <Tabs selectedKey={chartMetric} onSelectionChange={(k) => setChartMetric(k as string)} aria-label="Métrica">
              <Tabs.List>
                {chartMetrics.map((m) => <Tabs.Tab key={m.key} id={m.key}>{m.label}</Tabs.Tab>)}
              </Tabs.List>
            </Tabs>
          </div>
        </Card.Header>
        <Card.Content>
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
        </Card.Content>
      </Card>

      <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
        {byProvider.length > 0 && (
          <Card className="border border-border">
            <Card.Header><div><h3 className="font-semibold">Distribuição de requisições</h3><p className="text-xs text-muted">Por provider</p></div></Card.Header>
            <Card.Content>
              <ResponsiveContainer width="100%" height={260}>
                <PieChart>
                  <Pie data={byProvider} dataKey="value" nameKey="name" cx="50%" cy="50%" innerRadius={55} outerRadius={90} paddingAngle={2}
                    label={(e: any) => <text x={e.x} y={e.y} fill="#aaa" fontSize={11} textAnchor={e.x > e.cx ? "start" : "end"} dominantBaseline="central">{e.name}</text>}
                    labelLine={{ stroke: "#666" }}>
                    {byProvider.map((_, i) => <Cell key={i} fill={PIE_COLORS[i % PIE_COLORS.length]} stroke="none" />)}
                  </Pie>
                  <Legend formatter={(v) => <span className="text-xs text-foreground/80">{v}</span>} />
                  <RTooltip contentStyle={tooltipStyle} itemStyle={itemStyle} labelStyle={{ color: "#aaa" }} />
                </PieChart>
              </ResponsiveContainer>
            </Card.Content>
          </Card>
        )}

        {byModel.length > 0 && (
          <Card className="border border-border">
            <Card.Header><div><h3 className="font-semibold">Requisições por modelo</h3><p className="text-xs text-muted">Por modelo</p></div></Card.Header>
            <Card.Content>
              <ResponsiveContainer width="100%" height={Math.max(260, byModel.length * 26)}>
                <BarChart data={byModel} layout="vertical" margin={{ left: 20, right: 8, top: 8 }}>
                  <CartesianGrid strokeDasharray="3 3" stroke="#2a2a2a" horizontal={false} />
                  <XAxis type="number" stroke="#666" tick={{ fontSize: 12 }} tickLine={false} axisLine={false} allowDecimals={false} tickFormatter={formatCompact} />
                  <YAxis type="category" dataKey="name" stroke="#666" tick={{ fontSize: 11 }} tickLine={false} axisLine={false} width={100} />
                  <RTooltip contentStyle={tooltipStyle} itemStyle={itemStyle} cursor={{ fill: "#ffffff10" }} formatter={(v: number) => [v.toLocaleString("en-US"), "Requests"]} />
                  <Bar dataKey="value" fill="#4DA3FF" radius={[0, 4, 4, 0]} barSize={18} />
                </BarChart>
              </ResponsiveContainer>
            </Card.Content>
          </Card>
        )}
      </div>

      {savings && (
        <Card className="border border-border">
          <Card.Header>
            <div>
              <h3 className="font-semibold">Economia</h3>
              <p className="text-xs text-muted">Tokens e custos economizados por Response Cache e RTK</p>
            </div>
          </Card.Header>
          <Card.Content className="space-y-5">
            <div className="grid grid-cols-2 md:grid-cols-4 gap-4">
              <SavingsCard label="Cache hits" value={formatCompact(savings.cache_hits)} sub="respostas do cache" full={savings.cache_hits.toLocaleString("en-US")} dot="#00C2A8" />
              <SavingsCard label="Tokens (cache)" value={formatCompact(savings.cache_tokens_saved)} sub={formatCost(savings.cache_cost_saved)} full={savings.cache_tokens_saved.toLocaleString("en-US")} dot="#00C2A8" />
              <SavingsCard label="Compressões RTK" value={formatCompact(savings.rtk_compressions)} sub="tool_results" full={savings.rtk_compressions.toLocaleString("en-US")} dot="#4DA3FF" />
              <SavingsCard label="Tokens (RTK)" value={formatCompact(savings.rtk_tokens_saved)} sub={formatCost(savings.rtk_cost_saved)} full={savings.rtk_tokens_saved.toLocaleString("en-US")} dot="#4DA3FF" />
            </div>
            {(savings.cache_cost_saved + savings.rtk_cost_saved) > 0 && (
              <div className="flex items-baseline gap-8 pt-2 border-t border-border">
                <div className="flex items-baseline gap-2">
                  <span className="text-sm text-muted">Total economizado</span>
                  <span className="text-lg font-semibold tabular-nums">{formatCost(savings.cache_cost_saved + savings.rtk_cost_saved)}</span>
                </div>
                <div className="flex items-baseline gap-2">
                  <span className="text-sm text-muted">Tokens</span>
                  <span className="text-lg font-semibold tabular-nums">{formatCompact(savings.cache_tokens_saved + savings.rtk_tokens_saved)}</span>
                </div>
              </div>
            )}
          </Card.Content>
        </Card>
      )}

      {perfRows.length > 0 && (
        <Card className="border border-border">
          <Card.Header>
            <div>
              <h3 className="font-semibold">Performance por modelo</h3>
              <p className="text-xs text-muted">TTFT, TPS, latência e custo por request</p>
            </div>
          </Card.Header>
          <Card.Content>
            <Table>
              <Table.ScrollContainer>
                <Table.Content aria-label="performance por modelo" className="text-sm min-w-[640px]">
                  <Table.Header>
                    <Table.Column isRowHeader id="model">Modelo</Table.Column>
                    <Table.Column id="requests" className="text-right">Requests</Table.Column>
                    <Table.Column id="ttft" className="text-right">TTFT</Table.Column>
                    <Table.Column id="tps" className="text-right">TPS</Table.Column>
                    <Table.Column id="latency" className="text-right">Latência</Table.Column>
                    <Table.Column id="costperreq" className="text-right">Custo/Req</Table.Column>
                    <Table.Column id="costtotal" className="text-right">Custo Total</Table.Column>
                  </Table.Header>
                  <Table.Body items={perfRows}>
                    {(r) => (
                      <Table.Row key={r.name} id={r.name}>
                        <Table.Cell><code className="text-xs">{r.name}</code></Table.Cell>
                        <Table.Cell className="text-right tabular-nums">{r.requests.toLocaleString("en-US")}</Table.Cell>
                        <Table.Cell className="text-right tabular-nums">{r.ttft ? `${r.ttft}ms` : "—"}</Table.Cell>
                        <Table.Cell className="text-right tabular-nums">{r.tps ? r.tps.toFixed(1) : "—"}</Table.Cell>
                        <Table.Cell className="text-right tabular-nums">{r.latency ? `${r.latency}ms` : "—"}</Table.Cell>
                        <Table.Cell className="text-right tabular-nums">{r.costPerReq > 0 ? formatCost(r.costPerReq) : "—"}</Table.Cell>
                        <Table.Cell className="text-right tabular-nums">{r.cost > 0 ? formatCost(r.cost) : "—"}</Table.Cell>
                      </Table.Row>
                    )}
                  </Table.Body>
                </Table.Content>
              </Table.ScrollContainer>
            </Table>
          </Card.Content>
        </Card>
      )}

      <Card className="border border-border">
        <Card.Content className="py-4">
          <div className="flex items-center gap-6 flex-wrap">
            <div className="flex-1 min-w-[200px]">
              <div className="flex justify-between items-baseline mb-1.5">
                <span className="text-sm text-muted">Taxa de erro</span>
                <span className="text-sm font-semibold tabular-nums">{errorPct.toFixed(1)}%</span>
              </div>
              <ProgressBar
                aria-label="Taxa de erro"
                size="sm"
                color={errorPct > 5 ? "danger" : "success"}
                value={Math.min(errorPct, 100)}
              />
              <p className="text-[11px] text-muted mt-1">
                {stats.error_requests.toLocaleString("en-US")} erros · {stats.successful_requests.toLocaleString("en-US")} ok
              </p>
            </div>
            <Separator orientation="vertical" className="hidden md:block h-12" />
            <div className="flex gap-6">
              <div>
                <p className="text-xs text-muted">Combos</p>
                <p className="text-lg font-semibold tabular-nums mt-0.5">{stats.combo_requests.toLocaleString("en-US")}</p>
              </div>
              <div>
                <p className="text-xs text-muted">Erro/Total</p>
                <p className="text-lg font-semibold tabular-nums mt-0.5">{stats.error_requests.toLocaleString("en-US")}/{stats.requests.toLocaleString("en-US")}</p>
              </div>
            </div>
          </div>
        </Card.Content>
      </Card>

      <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
        {byModelCost.length > 0 && (
          <Card className="border border-border">
            <Card.Header><div><h3 className="font-semibold">Gasto em USD</h3><p className="text-xs text-muted">Custo por modelo</p></div></Card.Header>
            <Card.Content>
              <ResponsiveContainer width="100%" height={Math.max(260, byModelCost.length * 26)}>
                <BarChart data={byModelCost} layout="vertical" margin={{ left: 20, right: 8, top: 8 }}>
                  <CartesianGrid strokeDasharray="3 3" stroke="#2a2a2a" horizontal={false} />
                  <XAxis type="number" stroke="#666" tick={{ fontSize: 12 }} tickLine={false} axisLine={false} tickFormatter={formatCost} />
                  <YAxis type="category" dataKey="name" stroke="#666" tick={{ fontSize: 11 }} tickLine={false} axisLine={false} width={100} />
                  <RTooltip contentStyle={tooltipStyle} itemStyle={itemStyle} cursor={{ fill: "#ffffff10" }} formatter={(v: number) => [`$${v.toFixed(6)}`, "Custo"]} />
                  <Bar dataKey="value" fill="#FFB347" radius={[0, 4, 4, 0]} barSize={18} />
                </BarChart>
              </ResponsiveContainer>
            </Card.Content>
          </Card>
        )}

        {byCombo.length > 0 && (
          <Card className="border border-border">
            <Card.Header><div><h3 className="font-semibold">Distribuição entre combos</h3><p className="text-xs text-muted">Por combo</p></div></Card.Header>
            <Card.Content>
              <ResponsiveContainer width="100%" height={Math.max(260, byCombo.length * 26)}>
                <BarChart data={byCombo} layout="vertical" margin={{ left: 20, right: 8, top: 8 }}>
                  <CartesianGrid strokeDasharray="3 3" stroke="#2a2a2a" horizontal={false} />
                  <XAxis type="number" stroke="#666" tick={{ fontSize: 12 }} tickLine={false} axisLine={false} allowDecimals={false} tickFormatter={formatCompact} />
                  <YAxis type="category" dataKey="name" stroke="#666" tick={{ fontSize: 11 }} tickLine={false} axisLine={false} width={100} />
                  <RTooltip contentStyle={tooltipStyle} itemStyle={itemStyle} cursor={{ fill: "#ffffff10" }} formatter={(v: number) => [v.toLocaleString("en-US"), "Requests"]} />
                  <Bar dataKey="value" fill="#B266FF" radius={[0, 4, 4, 0]} barSize={18} />
                </BarChart>
              </ResponsiveContainer>
            </Card.Content>
          </Card>
        )}

        {byComboTokens.length > 0 && (
          <Card className="border border-border">
            <Card.Header><div><h3 className="font-semibold">Tokens por combo</h3><p className="text-xs text-muted">Prompt + completion</p></div></Card.Header>
            <Card.Content>
              <ResponsiveContainer width="100%" height={Math.max(260, byComboTokens.length * 26)}>
                <BarChart data={byComboTokens} layout="vertical" margin={{ left: 20, right: 8, top: 8 }}>
                  <CartesianGrid strokeDasharray="3 3" stroke="#2a2a2a" horizontal={false} />
                  <XAxis type="number" stroke="#666" tick={{ fontSize: 12 }} tickLine={false} axisLine={false} allowDecimals={false} tickFormatter={formatCompact} />
                  <YAxis type="category" dataKey="name" stroke="#666" tick={{ fontSize: 11 }} tickLine={false} axisLine={false} width={100} />
                  <RTooltip contentStyle={tooltipStyle} itemStyle={itemStyle} cursor={{ fill: "#ffffff10" }} formatter={(v: number) => [formatCompact(v), "Tokens"]} />
                  <Bar dataKey="value" fill="#4DA3FF" radius={[0, 4, 4, 0]} barSize={18} />
                </BarChart>
              </ResponsiveContainer>
            </Card.Content>
          </Card>
        )}

        {byApiKey.length > 0 && (
          <Card className="border border-border">
            <Card.Header><div><h3 className="font-semibold">Requisições por API key</h3><p className="text-xs text-muted">Por token</p></div></Card.Header>
            <Card.Content>
              <ResponsiveContainer width="100%" height={Math.max(260, byApiKey.length * 26)}>
                <BarChart data={byApiKey} layout="vertical" margin={{ left: 20, right: 8, top: 8 }}>
                  <CartesianGrid strokeDasharray="3 3" stroke="#2a2a2a" horizontal={false} />
                  <XAxis type="number" stroke="#666" tick={{ fontSize: 12 }} tickLine={false} axisLine={false} allowDecimals={false} tickFormatter={formatCompact} />
                  <YAxis type="category" dataKey="name" stroke="#666" tick={{ fontSize: 11 }} tickLine={false} axisLine={false} width={100} />
                  <RTooltip contentStyle={tooltipStyle} itemStyle={itemStyle} cursor={{ fill: "#ffffff10" }} formatter={(v: number) => [v.toLocaleString("en-US"), "Requests"]} />
                  <Bar dataKey="value" fill="#6BCB77" radius={[0, 4, 4, 0]} barSize={18} />
                </BarChart>
              </ResponsiveContainer>
            </Card.Content>
          </Card>
        )}
      </div>

      {status && (
        <div className="grid grid-cols-2 md:grid-cols-4 gap-4">
          <SystemCard label="Combos" value={status.combos.total} sub="estratégias" />
          <SystemCard
            label="Conexões"
            value={status.connections.total}
            sub={`${status.connections.active} ativas${status.connections.rate_limited > 0 ? ` · ${status.connections.rate_limited} rate-limited` : ""}`}
          />
          <SystemCard
            label="Saúde"
            value={status.health.unhealthy > 0 ? status.health.unhealthy : 0}
            sub={status.health.unhealthy > 0 ? "unhealthy" : "OK"}
            variant={status.health.unhealthy > 0 ? "danger" : "success"}
          />
          <SystemCard
            label="Tokens"
            value={apiKeys.filter(k => k.is_active).length}
            sub={`de ${apiKeys.length} cadastrados`}
          />
        </div>
      )}
    </div>
  );
}

function SystemCard({ label, value, sub, variant }: { label: string; value: number; sub: string; variant?: "danger" | "success" | "default" }) {
  const borderClass = variant === "danger" ? "border-danger/30" : variant === "success" ? "border-border" : "border-border";
  return (
    <div className={`bg-surface rounded-2xl border ${borderClass} p-5`}>
      <p className="text-xs text-muted uppercase tracking-wide font-medium">{label}</p>
      <p className="text-2xl font-bold mt-2 tabular-nums">{value}</p>
      <p className="text-xs text-muted mt-1">{sub}</p>
    </div>
  );
}

function StatCard({ label, value, sub, full }: { label: string; value: string | number; sub: string; full?: string }) {
  return (
    <Card className="border border-border hover:border-border transition-colors">
      <Card.Content className="p-5">
        <p className="text-xs text-muted uppercase tracking-wide font-medium">{label}</p>
        <p className="text-3xl font-bold mt-2 tabular-nums" title={full}>{value}</p>
        <p className="text-xs text-muted mt-1">{sub}</p>
      </Card.Content>
    </Card>
  );
}

function SavingsCard({ label, value, sub, full, dot }: { label: string; value: string; sub: string; full?: string; dot: string }) {
  return (
    <div className="rounded-xl border border-border p-5">
      <div className="flex items-center gap-2">
        <span className="w-2 h-2 rounded-full" style={{ backgroundColor: dot }} />
        <p className="text-xs text-muted uppercase tracking-wide font-medium">{label}</p>
      </div>
      <p className="text-2xl font-bold mt-2 tabular-nums" title={full}>{value}</p>
      <p className="text-xs text-muted mt-1">{sub}</p>
    </div>
  );
}

function formatDateRangeLabel(range: { start: { toString(): string }; end: { toString(): string } } | null): string {
  if (!range) return "Personalizado";
  const fmt = (s: string) => s.slice(11, 16) || s.slice(0, 10);
  return `${fmt(range.start.toString())} → ${fmt(range.end.toString())}`;
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
