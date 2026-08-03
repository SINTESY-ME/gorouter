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
import { IconCalendar } from "../icons";

const PIE_COLORS = ["var(--accent)", "var(--danger)", "var(--success)", "var(--warning)", "var(--default)", "var(--warning)", "var(--success)"];
const CHART_COLORS = ["var(--accent)", "var(--success)", "var(--warning)", "var(--danger)"];

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
    { key: "requests", label: "Requests", color: "var(--accent)", fmt: (v: number) => v.toLocaleString("en-US"), yFmt: formatCompact },
    { key: "tokens", label: "Tokens", color: "var(--success)", fmt: (v: number) => v.toLocaleString("en-US"), yFmt: formatCompact },
    { key: "cost", label: "Custo", color: "var(--warning)", fmt: (v: number) => `$${v.toFixed(6)}`, yFmt: formatCost },
    { key: "errors", label: "Erros", color: "var(--danger)", fmt: (v: number) => v.toLocaleString("en-US"), yFmt: formatCompact },
    { key: "avg_tps", label: "TPS", color: "var(--default)", fmt: (v: number) => `${v.toFixed(2)} tok/s`, yFmt: (v: number) => v.toFixed(1) },
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
          <div className="flex bg-surface rounded-lg p-0.5 border border-border gap-0.5">
            {periods.map((p) => (
              <Button
                key={p.key}
                size="sm"
                variant={!customMode && period === p.key ? "primary" : "tertiary"}
                onPress={() => { setPeriod(p.key); setCustomMode(false); }}
              >
                {p.label}
              </Button>
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
              <CartesianGrid strokeDasharray="3 3" stroke="var(--border)" vertical={false} />
              <XAxis dataKey="label" stroke="var(--muted)" tick={{ fontSize: 11 }} tickLine={false} axisLine={false} interval="preserveStartEnd" minTickGap={20} />
              <YAxis stroke="var(--muted)" tick={{ fontSize: 12 }} tickLine={false} axisLine={false} tickFormatter={activeMetric.yFmt} />
              <RTooltip cursor={{ stroke: "var(--border)" }} formatter={(v: number) => [activeMetric.fmt(v), activeMetric.label]} />
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
                    label={(e: any) => <text x={e.x} y={e.y} fill="var(--muted)" fontSize={11} textAnchor={e.x > e.cx ? "start" : "end"} dominantBaseline="central">{e.name}</text>}
                    labelLine={{ stroke: "var(--muted)" }}>
                    {byProvider.map((_, i) => <Cell key={i} fill={PIE_COLORS[i % PIE_COLORS.length]} stroke="none" />)}
                  </Pie>
                  <Legend formatter={(v) => <span className="text-xs text-foreground/80">{v}</span>} />
                  <RTooltip cursor={{ stroke: "var(--border)" }} />
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
                  <CartesianGrid strokeDasharray="3 3" stroke="var(--border)" horizontal={false} />
                  <XAxis type="number" stroke="var(--muted)" tick={{ fontSize: 12 }} tickLine={false} axisLine={false} allowDecimals={false} tickFormatter={formatCompact} />
                  <YAxis type="category" dataKey="name" stroke="var(--muted)" tick={{ fontSize: 11 }} tickLine={false} axisLine={false} width={100} />
                  <RTooltip cursor={{ stroke: "var(--border)" }} formatter={(v: number) => [v.toLocaleString("en-US"), "Requests"]} />
                  <Bar dataKey="value" fill={CHART_COLORS[1]} radius={[0, 4, 4, 0]} barSize={18} />
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
              <SavingsCard label="Cache hits" value={formatCompact(savings.cache_hits)} sub="respostas do cache" full={savings.cache_hits.toLocaleString("en-US")} dot={CHART_COLORS[0]} />
              <SavingsCard label="Tokens (cache)" value={formatCompact(savings.cache_tokens_saved)} sub={formatCost(savings.cache_cost_saved)} full={savings.cache_tokens_saved.toLocaleString("en-US")} dot={CHART_COLORS[0]} />
              <SavingsCard label="Compressões RTK" value={formatCompact(savings.rtk_compressions)} sub="tool_results" full={savings.rtk_compressions.toLocaleString("en-US")} dot={CHART_COLORS[1]} />
              <SavingsCard label="Tokens (RTK)" value={formatCompact(savings.rtk_tokens_saved)} sub={formatCost(savings.rtk_cost_saved)} full={savings.rtk_tokens_saved.toLocaleString("en-US")} dot={CHART_COLORS[1]} />
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

      <Card>
        <Card.Content className="grid gap-5 py-4 md:grid-cols-[minmax(0,1fr)_auto_auto] md:items-center">
          <div className="min-w-0">
            <div className="mb-2 flex items-center justify-between gap-3">
              <span className="text-sm text-muted">Taxa de erro</span>
              <span className="tabular-nums text-lg font-semibold">{errorPct.toFixed(1)}%</span>
            </div>
            <ProgressBar
              aria-label="Taxa de erro"
              size="sm"
              color={errorPct > 5 ? "danger" : "success"}
              value={Math.min(errorPct, 100)}
            />
            <p className="mt-2 text-xs text-muted">
              {stats.error_requests.toLocaleString("en-US")} erros · {stats.successful_requests.toLocaleString("en-US")} sucesso
            </p>
          </div>
          <Separator orientation="vertical" className="hidden h-12 md:block" />
          <div className="grid grid-cols-2 gap-8 md:gap-6">
            <div>
              <p className="text-xs text-muted">Requests</p>
              <p className="mt-1 tabular-nums text-xl font-semibold">{stats.requests.toLocaleString("en-US")}</p>
            </div>
            <div>
              <p className="text-xs text-muted">Combos</p>
              <p className="mt-1 tabular-nums text-xl font-semibold">{stats.combo_requests.toLocaleString("en-US")}</p>
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
                  <CartesianGrid strokeDasharray="3 3" stroke="var(--border)" horizontal={false} />
                  <XAxis type="number" stroke="var(--muted)" tick={{ fontSize: 12 }} tickLine={false} axisLine={false} tickFormatter={formatCost} />
                  <YAxis type="category" dataKey="name" stroke="var(--muted)" tick={{ fontSize: 11 }} tickLine={false} axisLine={false} width={100} />
                  <RTooltip cursor={{ stroke: "var(--border)" }} formatter={(v: number) => [`$${v.toFixed(6)}`, "Custo"]} />
                  <Bar dataKey="value" fill={CHART_COLORS[2]} radius={[0, 4, 4, 0]} barSize={18} />
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
                  <CartesianGrid strokeDasharray="3 3" stroke="var(--border)" horizontal={false} />
                  <XAxis type="number" stroke="var(--muted)" tick={{ fontSize: 12 }} tickLine={false} axisLine={false} allowDecimals={false} tickFormatter={formatCompact} />
                  <YAxis type="category" dataKey="name" stroke="var(--muted)" tick={{ fontSize: 11 }} tickLine={false} axisLine={false} width={100} />
                  <RTooltip cursor={{ stroke: "var(--border)" }} formatter={(v: number) => [v.toLocaleString("en-US"), "Requests"]} />
                  <Bar dataKey="value" fill={CHART_COLORS[3]} radius={[0, 4, 4, 0]} barSize={18} />
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
                  <CartesianGrid strokeDasharray="3 3" stroke="var(--border)" horizontal={false} />
                  <XAxis type="number" stroke="var(--muted)" tick={{ fontSize: 12 }} tickLine={false} axisLine={false} allowDecimals={false} tickFormatter={formatCompact} />
                  <YAxis type="category" dataKey="name" stroke="var(--muted)" tick={{ fontSize: 11 }} tickLine={false} axisLine={false} width={100} />
                  <RTooltip cursor={{ stroke: "var(--border)" }} formatter={(v: number) => [formatCompact(v), "Tokens"]} />
                  <Bar dataKey="value" fill={CHART_COLORS[1]} radius={[0, 4, 4, 0]} barSize={18} />
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
                  <CartesianGrid strokeDasharray="3 3" stroke="var(--border)" horizontal={false} />
                  <XAxis type="number" stroke="var(--muted)" tick={{ fontSize: 12 }} tickLine={false} axisLine={false} allowDecimals={false} tickFormatter={formatCompact} />
                  <YAxis type="category" dataKey="name" stroke="var(--muted)" tick={{ fontSize: 11 }} tickLine={false} axisLine={false} width={100} />
                  <RTooltip cursor={{ stroke: "var(--border)" }} formatter={(v: number) => [v.toLocaleString("en-US"), "Requests"]} />
                  <Bar dataKey="value" fill={CHART_COLORS[0]} radius={[0, 4, 4, 0]} barSize={18} />
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
    <Card className={`p-5 ${borderClass}`}>
      <p className="text-xs text-muted uppercase tracking-wide font-medium">{label}</p>
      <p className="text-2xl font-bold mt-2 tabular-nums">{value}</p>
      <p className="text-xs text-muted mt-1">{sub}</p>
    </Card>
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
    <Card className="p-5">
      <div className="flex items-center gap-2">
        <span className="w-2 h-2 rounded-full" style={{ backgroundColor: dot }} />
        <p className="text-xs text-muted uppercase tracking-wide font-medium">{label}</p>
      </div>
      <p className="text-2xl font-bold mt-2 tabular-nums" title={full}>{value}</p>
      <p className="text-xs text-muted mt-1">{sub}</p>
    </Card>
  );
}

function formatDateRangeLabel(range: { start: { toString(): string }; end: { toString(): string } } | null): string {
  if (!range) return "Personalizado";
  const fmt = (s: string) => s.slice(11, 16) || s.slice(0, 10);
  return `${fmt(range.start.toString())} → ${fmt(range.end.toString())}`;
}
