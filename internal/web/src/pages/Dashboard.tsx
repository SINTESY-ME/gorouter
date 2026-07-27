import { useEffect, useState } from "react";
import { Spinner, Select, SelectItem, Input, Button } from "@heroui/react";
import {
  ResponsiveContainer, AreaChart, Area, XAxis, YAxis, Tooltip, CartesianGrid,
  BarChart, Bar, PieChart, Pie, Cell, Legend,
} from "recharts";
import { api, type UsageStats, type SavingsStats } from "../api";
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

export default function Dashboard() {
  const [stats, setStats] = useState<UsageStats | null>(null);
  const [savings, setSavings] = useState<SavingsStats | null>(null);
  const [period, setPeriod] = useState("24h");
  const [bucket, setBucket] = useState(""); // "" = auto
  const [customMode, setCustomMode] = useState(false);
  const [fromDate, setFromDate] = useState("");
  const [toDate, setToDate] = useState("");
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    setLoading(true);
    const params: { period?: string; from?: string; to?: string; bucket?: string } = {};
    if (customMode && fromDate) {
      params.from = new Date(fromDate).toISOString();
      if (toDate) params.to = new Date(toDate).toISOString();
      if (bucket) params.bucket = bucket;
    } else {
      params.period = period;
      if (bucket) params.bucket = bucket;
    }
    Promise.all([
      api.usage.stats(params),
      api.savings.stats(customMode && fromDate ? "60d" : period).catch(() => null),
    ])
      .then(([s, sv]) => { setStats(s); setSavings(sv); })
      .catch(() => setStats(null))
      .finally(() => setLoading(false));
  }, [period, bucket, customMode, fromDate, toDate]);

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

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between flex-wrap gap-3">
        <div>
          <h1 className="text-2xl font-bold tracking-tight">Visão geral</h1>
          <p className="text-sm text-default-500 mt-0.5">
            Total de {stats.requests} requisições no período
          </p>
        </div>
        <div className="flex items-center gap-2 flex-wrap">
          {/* Period presets */}
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
            <button
              onClick={() => setCustomMode(true)}
              className={`px-3 py-1.5 text-sm rounded-md transition-colors ${
                customMode ? "bg-primary text-white" : "text-default-600 hover:bg-default-100"
              }`}
            >
              Personalizado
            </button>
          </div>
          {/* Bucket selector */}
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
        </div>
      </div>

      {/* Custom date range */}
      {customMode && (
        <div className="flex items-center gap-3 flex-wrap bg-content1 rounded-xl border border-default-100 p-4">
          <Input
            type="datetime-local"
            label="De"
            value={fromDate}
            onValueChange={setFromDate}
            size="sm"
            className="w-48"
          />
          <Input
            type="datetime-local"
            label="Até"
            value={toDate}
            onValueChange={setToDate}
            size="sm"
            className="w-48"
            placeholder="Agora"
          />
          <Button
            size="sm"
            variant="flat"
            onPress={() => {
              const now = new Date();
              setFromDate(new Date(now.getTime() - 60 * 60 * 1000).toISOString().slice(0, 16));
              setToDate(now.toISOString().slice(0, 16));
            }}
          >
            Última hora
          </Button>
        </div>
      )}

      <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4">
        <StatCard label="Requests" value={formatCompact(stats.requests)} sub="total no período" full={stats.requests.toLocaleString("en-US")} />
        <StatCard label="Prompt tokens" value={formatCompact(stats.prompt_tokens)} sub="tokens enviados" full={stats.prompt_tokens.toLocaleString("en-US")} />
        <StatCard label="Completion tokens" value={formatCompact(stats.completion_tokens)} sub="tokens gerados" full={stats.completion_tokens.toLocaleString("en-US")} />
        <StatCard label="Custo" value={formatCost(stats.cost)} sub="acumulado" full={`$${stats.cost.toFixed(6)}`} />
      </div>

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
          {(savings.cache_cost_saved + savings.rtk_cost_saved) > 0 && (
            <div className="mt-4 flex items-center gap-6 text-sm">
              <div className="flex items-center gap-2">
                <span className="text-default-500">Tokens economizados:</span>
                <span className="text-lg font-bold text-success">
                  {formatCompact(savings.cache_tokens_saved + savings.rtk_tokens_saved)} tokens
                </span>
              </div>
              <div className="flex items-center gap-2">
                <span className="text-default-500">Custo poupado:</span>
                <span className="text-lg font-bold text-success">
                  {formatCost(savings.cache_cost_saved + savings.rtk_cost_saved)}
                </span>
              </div>
            </div>
          )}
        </div>
      )}

      <div className="bg-content1 rounded-2xl border border-default-100 p-6">
        <div className="flex items-center justify-between mb-4">
          <div>
            <h3 className="font-semibold">Requisições por {bucketLabel[activeBucket] || "período"}</h3>
            <p className="text-xs text-default-500 mt-0.5">Volume de chamadas ({bucketLabel[activeBucket] || "dia"})</p>
          </div>
        </div>
        <ResponsiveContainer width="100%" height={280}>
          <AreaChart data={daily} margin={{ left: -16, right: 8, top: 8 }}>
            <defs>
              <linearGradient id="gradReq" x1="0" y1="0" x2="0" y2="1">
                <stop offset="5%" stopColor="#00C2A8" stopOpacity={0.6} />
                <stop offset="95%" stopColor="#00C2A8" stopOpacity={0.02} />
              </linearGradient>
            </defs>
            <CartesianGrid strokeDasharray="3 3" stroke="#2a2a2a" vertical={false} />
            <XAxis dataKey="label" stroke="#666" tick={{ fontSize: 11 }} tickLine={false} axisLine={false} interval="preserveStartEnd" minTickGap={20} />
            <YAxis stroke="#666" tick={{ fontSize: 12 }} tickLine={false} axisLine={false} allowDecimals={false} tickFormatter={(v: number) => formatCompact(v)} />
            <Tooltip contentStyle={chartTooltipStyle} itemStyle={chartItemStyle} labelStyle={{ color: "#888" }} formatter={(v: number) => [v.toLocaleString("en-US"), "Requests"]} />
            <Area type="monotone" dataKey="requests" stroke="#00C2A8" strokeWidth={2} fill="url(#gradReq)" />
          </AreaChart>
        </ResponsiveContainer>
      </div>

      <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
        <div className="bg-content1 rounded-2xl border border-default-100 p-6">
          <h3 className="font-semibold mb-1">Por provider</h3>
          <p className="text-xs text-default-500 mb-4">Distribuição de requisições</p>
          {byProvider.length === 0 ? (
            <EmptyChart />
          ) : (
            <ResponsiveContainer width="100%" height={260}>
              <PieChart>
                <Pie data={byProvider} dataKey="value" nameKey="name" cx="50%" cy="50%" innerRadius={60} outerRadius={95} paddingAngle={2} label={(entry: any) => <text x={entry.x} y={entry.y} fill="#aaa" fontSize={11} textAnchor={entry.x > entry.cx ? "start" : "end"} dominantBaseline="central">{entry.name}</text>} labelLine={{ stroke: "#666" }}>
                  {byProvider.map((_, i) => <Cell key={i} fill={PIE_COLORS[i % PIE_COLORS.length]} stroke="none" />)}
                </Pie>
                <Legend formatter={(v) => <span className="text-xs text-default-600">{v}</span>} />
                <Tooltip contentStyle={chartTooltipStyle} itemStyle={chartItemStyle} labelStyle={{ color: "#aaa" }} />
              </PieChart>
            </ResponsiveContainer>
          )}
        </div>
        <div className="bg-content1 rounded-2xl border border-default-100 p-6">
          <h3 className="font-semibold mb-1">Por modelo</h3>
          <p className="text-xs text-default-500 mb-4">Requisições por modelo</p>
          {byModel.length === 0 ? (
            <EmptyChart />
          ) : (
            <ResponsiveContainer width="100%" height={260}>
              <BarChart data={byModel} layout="vertical" margin={{ left: 20, right: 8, top: 8 }}>
                <CartesianGrid strokeDasharray="3 3" stroke="#2a2a2a" horizontal={false} />
                <XAxis type="number" stroke="#666" tick={{ fontSize: 12 }} tickLine={false} axisLine={false} allowDecimals={false} tickFormatter={(v: number) => formatCompact(v)} />
                <YAxis type="category" dataKey="name" stroke="#666" tick={{ fontSize: 11 }} tickLine={false} axisLine={false} width={90} />
                <Tooltip contentStyle={chartTooltipStyle} itemStyle={chartItemStyle} cursor={{ fill: "#ffffff10" }} formatter={(v: number) => [v.toLocaleString("en-US"), "Requests"]} />
                <Bar dataKey="value" fill="#4DA3FF" radius={[0, 4, 4, 0]} barSize={20} />
              </BarChart>
            </ResponsiveContainer>
          )}
        </div>
        <div className="bg-content1 rounded-2xl border border-default-100 p-6">
          <h3 className="font-semibold mb-1">Custo por modelo</h3>
          <p className="text-xs text-default-500 mb-4">Gasto em USD por modelo</p>
          {byModelCost.length === 0 ? (
            <EmptyChart />
          ) : (
            <ResponsiveContainer width="100%" height={260}>
              <BarChart data={byModelCost} layout="vertical" margin={{ left: 20, right: 8, top: 8 }}>
                <CartesianGrid strokeDasharray="3 3" stroke="#2a2a2a" horizontal={false} />
                <XAxis type="number" stroke="#666" tick={{ fontSize: 12 }} tickLine={false} axisLine={false} tickFormatter={(v: number) => formatCost(v)} />
                <YAxis type="category" dataKey="name" stroke="#666" tick={{ fontSize: 11 }} tickLine={false} axisLine={false} width={90} />
                <Tooltip contentStyle={chartTooltipStyle} itemStyle={chartItemStyle} cursor={{ fill: "#ffffff10" }} formatter={(v: number) => [`$${v.toFixed(6)}`, "Custo"]} />
                <Bar dataKey="value" fill="#FFB347" radius={[0, 4, 4, 0]} barSize={20} />
              </BarChart>
            </ResponsiveContainer>
          )}
        </div>
      </div>
    </div>
  );
}

function StatCard({ label, value, sub, full }: { label: string; value: string | number; sub: string; full?: string }) {
  return (
    <div className="bg-content1 rounded-2xl border border-default-100 p-5 hover:border-default-200 transition-colors">
      <p className="text-xs text-default-500 uppercase tracking-wide font-medium">{label}</p>
      <p className="text-3xl font-bold mt-2 tabular-nums" title={full}>{value}</p>
      <p className="text-xs text-default-500 mt-1">{sub}</p>
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