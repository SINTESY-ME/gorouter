import { useEffect, useState } from "react";
import {
  Spinner, Select, ListBox, Popover, Button, ButtonGroup, Card, Tabs,
  Table,
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
import { ChartTooltip } from "../chartTooltip";
import { useTranslation } from "react-i18next";

const PIE_COLORS = ["var(--accent)", "var(--danger)", "var(--success)", "var(--warning)", "var(--default)", "var(--warning)", "var(--success)"];
const CHART_COLORS = ["var(--accent)", "var(--success)", "var(--warning)", "var(--danger)"];

const periods: { key: string; label: string }[] = [
  { key: "1h", label: "1h" },
  { key: "24h", label: "24h" },
  { key: "7d", label: "7d" },
  { key: "30d", label: "30d" },
  { key: "60d", label: "60d" },
];

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
  const { t } = useTranslation();

  const buckets: { key: string; label: string }[] = [
    { key: "", label: t("dashboard.buckets.auto") },
    { key: "minute", label: t("dashboard.buckets.minute") },
    { key: "5m", label: t("dashboard.buckets.min5") },
    { key: "30m", label: t("dashboard.buckets.min30") },
    { key: "hour", label: t("dashboard.buckets.hour") },
    { key: "day", label: t("dashboard.buckets.day") },
  ];

  const bucketLabel: Record<string, string> = {
    minute: t("dashboard.buckets.minuteLower"), "5m": t("dashboard.buckets.min5Lower"), "30m": t("dashboard.buckets.min30Lower"), hour: t("dashboard.buckets.hourLower"), day: t("dashboard.buckets.dayLower"),
  };

  const formatDateRangeLabel = (range: { start: { toString(): string }; end: { toString(): string } } | null): string => {
    if (!range) return t("dashboard.customPeriod");
    const fmt = (s: string) => s.slice(11, 16) || s.slice(0, 10);
    return `${fmt(range.start.toString())} → ${fmt(range.end.toString())}`;
  };

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
      {t("dashboard.empty")}
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
    { key: "requests", label: t("dashboard.chartRequests"), color: "var(--accent)", fmt: (v: number) => v.toLocaleString("en-US"), yFmt: formatCompact },
    { key: "tokens", label: t("dashboard.chartTokens"), color: "var(--success)", fmt: (v: number) => v.toLocaleString("en-US"), yFmt: formatCompact },
    { key: "cost", label: t("dashboard.chartCost"), color: "var(--warning)", fmt: (v: number) => `$${v.toFixed(6)}`, yFmt: formatCost },
    { key: "errors", label: t("dashboard.chartErrors"), color: "var(--danger)", fmt: (v: number) => v.toLocaleString("en-US"), yFmt: formatCompact },
    { key: "avg_tps", label: t("dashboard.chartTps"), color: "var(--default)", fmt: (v: number) => `${v.toFixed(2)} ${t("dashboard.tokPerSec")}`, yFmt: (v: number) => v.toFixed(1) },
  ];
  const activeMetric = chartMetrics.find((m) => m.key === chartMetric) || chartMetrics[0];

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between flex-wrap gap-3">
        <div>
          <h1 className="text-2xl font-bold tracking-tight">{t("dashboard.title")}</h1>
          <p className="text-sm text-muted mt-0.5">
            {t("dashboard.requestsInPeriod", { count: stats.requests.toLocaleString("en-US") })}
          </p>
        </div>
        <div className="flex items-center gap-2 flex-wrap">
          <ButtonGroup size="sm" variant="secondary">
            {periods.map((p) => (
              <Button
                key={p.key}
                variant={!customMode && period === p.key ? "primary" : "tertiary"}
                onPress={() => { setPeriod(p.key); setCustomMode(false); }}
              >
                {p.label}
              </Button>
            ))}
            <Popover>
              <Popover.Trigger>
                <Button variant={customMode ? "primary" : "tertiary"} size="sm" className="rounded-l-none" onPress={() => setCustomMode(true)}>
                  <ButtonGroup.Separator />
                  <IconCalendar className="w-4 h-4" />
                  {formatDateRangeLabel(customMode ? dateRange : null)}
                </Button>
              </Popover.Trigger>
            <Popover.Content placement="bottom" className="p-3">
              <div className="space-y-3 w-80">
                <DateRangePicker
                  aria-label={t("dashboard.customPeriod")}
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
                    <RangeCalendar aria-label={t("dashboard.customPeriod")}>
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
                <Button size="sm" variant="primary" className="w-full" onPress={() => setCustomMode(true)}>{t("dashboard.apply")}</Button>
                {customMode && (
                  <Button size="sm" variant="secondary" className="w-full" onPress={() => { setCustomMode(false); setDateRange(null); }}>
                    {t("dashboard.backToPresets")}
                  </Button>
                )}
              </div>
            </Popover.Content>
           </Popover>
          </ButtonGroup>
          {apiKeys.length > 0 && (
            <Select
              aria-label={t("dashboard.token")}
              selectedKey={selectedKeyId || null}
              onSelectionChange={(k) => setSelectedKeyId((k as string) ?? "")}
              className="w-44"
            >
              <Select.Trigger>
                <Select.Value>{selectedKeyId ? apiKeys.find((k) => k.id === selectedKeyId)?.name ?? t("dashboard.token") : t("dashboard.allTokens")}</Select.Value>
                <Select.Indicator />
              </Select.Trigger>
              <Select.Popover>
                <ListBox>
                  <ListBox.Item id="">{t("dashboard.allTokens")}</ListBox.Item>
                  {apiKeys.map((k) => <ListBox.Item key={k.id} id={k.id}>{k.name}</ListBox.Item>)}
                </ListBox>
              </Select.Popover>
            </Select>
          )}
        </div>
      </div>

      <div className="grid grid-cols-2 lg:grid-cols-4 gap-4">
        <StatCard label={t("dashboard.requests")} value={formatCompact(stats.requests)} sub={t("dashboard.requestsSub")} full={stats.requests.toLocaleString("en-US")} />
        <StatCard label={t("dashboard.tokens")} value={formatCompact(totalTokens)} sub={t("dashboard.tokensSub", { in: formatCompact(stats.prompt_tokens), out: formatCompact(stats.completion_tokens) })} full={totalTokens.toLocaleString("en-US")} />
        <StatCard label={t("dashboard.cost")} value={formatCost(stats.cost)} sub={stats.cost_per_request > 0 ? `${formatCost(stats.cost_per_request)}${t("dashboard.costPerReq")}` : "—"} full={`$${stats.cost.toFixed(6)}`} />
        <StatCard label={t("dashboard.savings")} value={formatCost(stats.cost_saved)} sub={t("dashboard.savingsSub", { count: formatCompact(stats.tokens_saved) })} full={`$${stats.cost_saved.toFixed(6)}`} />
      </div>

      <Card className="border border-border">
        <Card.Header className="flex items-center justify-between gap-3 flex-wrap pb-0">
          <div>
            <h3 className="font-semibold">{t("dashboard.volumeBy", { bucket: bucketLabel[activeBucket] || t("dashboard.buckets.period") })}</h3>
            <p className="text-xs text-muted">{t("dashboard.timeSeries")}</p>
          </div>
          <div className="flex items-center gap-2">
            <Select
              aria-label={t("dashboard.granularity")}
              selectedKey={bucket}
              onSelectionChange={(k) => setBucket((k as string) ?? "")}
              className="w-32"
            >
              <Select.Trigger>
                <Select.Value>{buckets.find((b) => b.key === bucket)?.label ?? t("dashboard.buckets.auto")}</Select.Value>
                <Select.Indicator />
              </Select.Trigger>
              <Select.Popover>
                <ListBox>
                  {buckets.map((b) => <ListBox.Item key={b.key} id={b.key}>{b.label}</ListBox.Item>)}
                </ListBox>
              </Select.Popover>
            </Select>
            <Tabs selectedKey={chartMetric} onSelectionChange={(k) => setChartMetric(k as string)} aria-label={t("dashboard.metric")}>
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
              <RTooltip content={<ChartTooltip />} cursor={{ stroke: "var(--border)", strokeWidth: 1 }} />
              <Area type="monotone" dataKey={chartMetric} stroke={activeMetric.color} strokeWidth={2} fill="url(#gradChart)" />
            </AreaChart>
          </ResponsiveContainer>
        </Card.Content>
      </Card>

      <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
        {byProvider.length > 0 && (
          <Card className="border border-border">
            <Card.Header><div><h3 className="font-semibold">{t("dashboard.distributionRequests")}</h3><p className="text-xs text-muted">{t("dashboard.byProvider")}</p></div></Card.Header>
            <Card.Content>
              <ResponsiveContainer width="100%" height={260}>
                <PieChart>
                  <Pie data={byProvider} dataKey="value" nameKey="name" cx="50%" cy="50%" innerRadius={55} outerRadius={90} paddingAngle={2}
                    label={(e: any) => <text x={e.x} y={e.y} fill="var(--muted)" fontSize={11} textAnchor={e.x > e.cx ? "start" : "end"} dominantBaseline="central">{e.name}</text>}
                    labelLine={{ stroke: "var(--muted)" }}>
                    {byProvider.map((_, i) => <Cell key={i} fill={PIE_COLORS[i % PIE_COLORS.length]} stroke="none" />)}
                  </Pie>
                  <Legend formatter={(v) => <span className="text-xs text-foreground/80">{v}</span>} />
                  <RTooltip content={<ChartTooltip />} cursor={{ fill: "var(--surface-secondary)" }} />
                </PieChart>
              </ResponsiveContainer>
            </Card.Content>
          </Card>
        )}

        {byModel.length > 0 && (
          <Card className="border border-border">
            <Card.Header><div><h3 className="font-semibold">{t("dashboard.requestsByModel")}</h3><p className="text-xs text-muted">{t("dashboard.byModel")}</p></div></Card.Header>
            <Card.Content>
              <ResponsiveContainer width="100%" height={Math.max(260, byModel.length * 26)}>
                <BarChart data={byModel} layout="vertical" margin={{ left: 20, right: 8, top: 8 }}>
                  <CartesianGrid strokeDasharray="3 3" stroke="var(--border)" horizontal={false} />
                  <XAxis type="number" stroke="var(--muted)" tick={{ fontSize: 12 }} tickLine={false} axisLine={false} allowDecimals={false} tickFormatter={formatCompact} />
                  <YAxis type="category" dataKey="name" stroke="var(--muted)" tick={{ fontSize: 11 }} tickLine={false} axisLine={false} width={100} />
                  <RTooltip content={<ChartTooltip />} cursor={{ fill: "var(--surface-secondary)" }} />
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
              <h3 className="font-semibold">{t("dashboard.savingsTitle")}</h3>
              <p className="text-xs text-muted">{t("dashboard.savingsSubtitle")}</p>
            </div>
          </Card.Header>
          <Card.Content className="space-y-5">
            <div className="grid grid-cols-2 md:grid-cols-4 gap-4">
              <SavingsCard label={t("dashboard.cacheHits")} value={formatCompact(savings.cache_hits)} sub={t("dashboard.cacheHitsSub")} full={savings.cache_hits.toLocaleString("en-US")} dot={CHART_COLORS[0]} />
              <SavingsCard label={t("dashboard.cacheTokens")} value={formatCompact(savings.cache_tokens_saved)} sub={formatCost(savings.cache_cost_saved)} full={savings.cache_tokens_saved.toLocaleString("en-US")} dot={CHART_COLORS[0]} />
              <SavingsCard label={t("dashboard.rtkComps")} value={formatCompact(savings.rtk_compressions)} sub={t("dashboard.rtkCompsSub")} full={savings.rtk_compressions.toLocaleString("en-US")} dot={CHART_COLORS[1]} />
              <SavingsCard label={t("dashboard.rtkTokens")} value={formatCompact(savings.rtk_tokens_saved)} sub={formatCost(savings.rtk_cost_saved)} full={savings.rtk_tokens_saved.toLocaleString("en-US")} dot={CHART_COLORS[1]} />
              {savings.semantic_hits ? (
                <SavingsCard label={t("dashboard.semanticHits")} value={formatCompact(savings.semantic_hits)} sub={t("dashboard.semanticHitsSub")} full={savings.semantic_hits.toLocaleString("en-US")} dot={CHART_COLORS[3]} />
              ) : null}
              {savings.semantic_tokens_saved ? (
                <SavingsCard label={t("dashboard.semanticTokens")} value={formatCompact(savings.semantic_tokens_saved)} sub={formatCost(savings.semantic_cost_saved || 0)} full={savings.semantic_tokens_saved.toLocaleString("en-US")} dot={CHART_COLORS[3]} />
              ) : null}
            </div>
            {(savings.cache_cost_saved + savings.rtk_cost_saved + (savings.semantic_cost_saved || 0)) > 0 && (
              <div className="flex items-baseline gap-8 pt-2 border-t border-border">
                <div className="flex items-baseline gap-2">
                  <span className="text-sm text-muted">{t("dashboard.totalSaved")}</span>
                  <span className="text-lg font-semibold tabular-nums">{formatCost(savings.cache_cost_saved + savings.rtk_cost_saved + (savings.semantic_cost_saved || 0))}</span>
                </div>
                <div className="flex items-baseline gap-2">
                  <span className="text-sm text-muted">{t("dashboard.tokens")}</span>
                  <span className="text-lg font-semibold tabular-nums">{formatCompact(savings.cache_tokens_saved + savings.rtk_tokens_saved + (savings.semantic_tokens_saved || 0))}</span>
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
              <h3 className="font-semibold">{t("dashboard.perfByModel")}</h3>
              <p className="text-xs text-muted">{t("dashboard.perfSub")}</p>
            </div>
          </Card.Header>
          <Card.Content>
            <Table>
              <Table.ScrollContainer>
                <Table.Content aria-label={t("dashboard.perfAria")} className="text-sm min-w-[640px]">
                  <Table.Header>
                    <Table.Column isRowHeader id="model">{t("dashboard.colModel")}</Table.Column>
                    <Table.Column id="requests" className="text-right">{t("dashboard.colRequests")}</Table.Column>
                    <Table.Column id="ttft" className="text-right">{t("dashboard.colTtft")}</Table.Column>
                    <Table.Column id="tps" className="text-right">{t("dashboard.colTps")}</Table.Column>
                    <Table.Column id="latency" className="text-right">{t("dashboard.colLatency")}</Table.Column>
                    <Table.Column id="costperreq" className="text-right">{t("dashboard.colCostPerReq")}</Table.Column>
                    <Table.Column id="costtotal" className="text-right">{t("dashboard.colCostTotal")}</Table.Column>
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
      <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
        {byModelCost.length > 0 && (
          <Card className="border border-border">
            <Card.Header><div><h3 className="font-semibold">{t("dashboard.spendUsd")}</h3><p className="text-xs text-muted">{t("dashboard.costByModel")}</p></div></Card.Header>
            <Card.Content>
              <ResponsiveContainer width="100%" height={Math.max(260, byModelCost.length * 26)}>
                <BarChart data={byModelCost} layout="vertical" margin={{ left: 20, right: 8, top: 8 }}>
                  <CartesianGrid strokeDasharray="3 3" stroke="var(--border)" horizontal={false} />
                  <XAxis type="number" stroke="var(--muted)" tick={{ fontSize: 12 }} tickLine={false} axisLine={false} tickFormatter={formatCost} />
                  <YAxis type="category" dataKey="name" stroke="var(--muted)" tick={{ fontSize: 11 }} tickLine={false} axisLine={false} width={100} />
                  <RTooltip content={<ChartTooltip />} cursor={{ fill: "var(--surface-secondary)" }} />
                  <Bar dataKey="value" fill={CHART_COLORS[2]} radius={[0, 4, 4, 0]} barSize={18} />
                </BarChart>
              </ResponsiveContainer>
            </Card.Content>
          </Card>
        )}

        {byCombo.length > 0 && (
          <Card className="border border-border">
            <Card.Header><div><h3 className="font-semibold">{t("dashboard.comboDistribution")}</h3><p className="text-xs text-muted">{t("dashboard.byCombo")}</p></div></Card.Header>
            <Card.Content>
              <ResponsiveContainer width="100%" height={Math.max(260, byCombo.length * 26)}>
                <BarChart data={byCombo} layout="vertical" margin={{ left: 20, right: 8, top: 8 }}>
                  <CartesianGrid strokeDasharray="3 3" stroke="var(--border)" horizontal={false} />
                  <XAxis type="number" stroke="var(--muted)" tick={{ fontSize: 12 }} tickLine={false} axisLine={false} allowDecimals={false} tickFormatter={formatCompact} />
                  <YAxis type="category" dataKey="name" stroke="var(--muted)" tick={{ fontSize: 11 }} tickLine={false} axisLine={false} width={100} />
                  <RTooltip content={<ChartTooltip />} cursor={{ fill: "var(--surface-secondary)" }} />
                  <Bar dataKey="value" fill={CHART_COLORS[3]} radius={[0, 4, 4, 0]} barSize={18} />
                </BarChart>
              </ResponsiveContainer>
            </Card.Content>
          </Card>
        )}

        {byComboTokens.length > 0 && (
          <Card className="border border-border">
            <Card.Header><div><h3 className="font-semibold">{t("dashboard.comboTokens")}</h3><p className="text-xs text-muted">{t("dashboard.promptPlusCompletion")}</p></div></Card.Header>
            <Card.Content>
              <ResponsiveContainer width="100%" height={Math.max(260, byComboTokens.length * 26)}>
                <BarChart data={byComboTokens} layout="vertical" margin={{ left: 20, right: 8, top: 8 }}>
                  <CartesianGrid strokeDasharray="3 3" stroke="var(--border)" horizontal={false} />
                  <XAxis type="number" stroke="var(--muted)" tick={{ fontSize: 12 }} tickLine={false} axisLine={false} allowDecimals={false} tickFormatter={formatCompact} />
                  <YAxis type="category" dataKey="name" stroke="var(--muted)" tick={{ fontSize: 11 }} tickLine={false} axisLine={false} width={100} />
                  <RTooltip content={<ChartTooltip />} cursor={{ fill: "var(--surface-secondary)" }} />
                  <Bar dataKey="value" fill={CHART_COLORS[1]} radius={[0, 4, 4, 0]} barSize={18} />
                </BarChart>
              </ResponsiveContainer>
            </Card.Content>
          </Card>
        )}

        {byApiKey.length > 0 && (
          <Card className="border border-border">
            <Card.Header><div><h3 className="font-semibold">{t("dashboard.reqByApiKey")}</h3><p className="text-xs text-muted">{t("dashboard.byToken")}</p></div></Card.Header>
            <Card.Content>
              <ResponsiveContainer width="100%" height={Math.max(260, byApiKey.length * 26)}>
                <BarChart data={byApiKey} layout="vertical" margin={{ left: 20, right: 8, top: 8 }}>
                  <CartesianGrid strokeDasharray="3 3" stroke="var(--border)" horizontal={false} />
                  <XAxis type="number" stroke="var(--muted)" tick={{ fontSize: 12 }} tickLine={false} axisLine={false} allowDecimals={false} tickFormatter={formatCompact} />
                  <YAxis type="category" dataKey="name" stroke="var(--muted)" tick={{ fontSize: 11 }} tickLine={false} axisLine={false} width={100} />
                  <RTooltip content={<ChartTooltip />} cursor={{ fill: "var(--surface-secondary)" }} />
                  <Bar dataKey="value" fill={CHART_COLORS[0]} radius={[0, 4, 4, 0]} barSize={18} />
                </BarChart>
              </ResponsiveContainer>
            </Card.Content>
          </Card>
        )}
      </div>

      {status && (
        <div className="grid grid-cols-2 md:grid-cols-3 xl:grid-cols-5 gap-4">
          <SystemCard label={t("dashboard.combos")} value={status.combos.total} sub={t("dashboard.combosSub")} />
          <SystemCard
            label={t("dashboard.connections")}
            value={status.connections.total}
            sub={t("dashboard.connectionsSub", {
              active: status.connections.active,
              rateLimited: status.connections.rate_limited > 0 ? t("dashboard.rateLimited", { count: status.connections.rate_limited }) : "",
            })}
          />
          <SystemCard
            label={t("dashboard.health")}
            value={`${status.health.healthy}/${status.health.total_keys}`}
            sub={t("dashboard.healthSub", { unhealthy: status.health.unhealthy, probing: status.health.probing })}
            variant={status.health.unhealthy > 0 ? "danger" : "success"}
          />
          <SystemCard
            label={t("dashboard.errorRate")}
            value={`${errorPct.toFixed(1)}%`}
            sub={t("dashboard.errorRateSub", { errors: stats.error_requests.toLocaleString("en-US"), success: stats.successful_requests.toLocaleString("en-US") })}
            variant={errorPct > 5 ? "danger" : "success"}
          />
          <SystemCard
            label={t("dashboard.tokensRegistered")}
            value={apiKeys.filter(k => k.is_active).length}
            sub={t("dashboard.tokensRegisteredSub", { count: apiKeys.length })}
          />
        </div>
      )}
    </div>
  );
}

function SystemCard({ label, value, sub, variant }: { label: string; value: string | number; sub: string; variant?: "danger" | "success" | "default" }) {
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
