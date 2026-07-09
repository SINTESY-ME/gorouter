import { useEffect, useState } from "react";
import { Spinner } from "@heroui/react";
import {
  ResponsiveContainer, AreaChart, Area, XAxis, YAxis, Tooltip, CartesianGrid,
  BarChart, Bar, PieChart, Pie, Cell, Legend,
} from "recharts";
import { api, type UsageStats } from "../api";

const PIE_COLORS = ["#00C2A8", "#FF6B6B", "#4DA3FF", "#FFB347", "#B266FF", "#FFD93D", "#6BCB77"];

const periods: { key: string; label: string }[] = [
  { key: "24h", label: "24 horas" },
  { key: "7d", label: "7 dias" },
  { key: "30d", label: "30 dias" },
  { key: "60d", label: "60 dias" },
];

const chartTooltipStyle = {
  backgroundColor: "#1a1a1a",
  border: "1px solid #333",
  borderRadius: "8px",
  fontSize: "12px",
  color: "#eee",
};

export default function Dashboard() {
  const [stats, setStats] = useState<UsageStats | null>(null);
  const [period, setPeriod] = useState("7d");
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    setLoading(true);
    api.usage.stats(period)
      .then(setStats)
      .catch(() => setStats(null))
      .finally(() => setLoading(false));
  }, [period]);

  if (loading) return (
    <div className="flex justify-center py-20"><Spinner label="Carregando..." /></div>
  );
  if (!stats) return (
    <div className="text-center py-20 text-default-500">
      Não há dados de uso ainda. Crie um provider e faça uma requisição.
    </div>
  );

  const daily = stats.daily.map((d) => ({ ...d, date: d.date.slice(5) }));
  const byProvider = Object.entries(stats.by_provider).map(([name, value]) => ({ name, value }));
  const byModel = Object.entries(stats.by_model).map(([name, value]) => ({ name, value }));

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold tracking-tight">Visão geral</h1>
          <p className="text-sm text-default-500 mt-0.5">
            Total de {stats.requests} requisições no período
          </p>
        </div>
        <div className="flex bg-content1 rounded-lg p-0.5 border border-default-100">
          {periods.map((p) => (
            <button
              key={p.key}
              onClick={() => setPeriod(p.key)}
              className={`px-3 py-1.5 text-sm rounded-md transition-colors ${
                period === p.key ? "bg-primary text-white" : "text-default-600 hover:bg-default-100"
              }`}
            >
              {p.label}
            </button>
          ))}
        </div>
      </div>

      <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4">
        <StatCard label="Requests" value={stats.requests} sub="total no período" />
        <StatCard label="Prompt tokens" value={stats.prompt_tokens} sub="tokens enviados" />
        <StatCard label="Completion tokens" value={stats.completion_tokens} sub="tokens gerados" />
        <StatCard label="Custo" value={`$${stats.cost.toFixed(4)}`} sub="acumulado" />
      </div>

      <div className="bg-content1 rounded-2xl border border-default-100 p-6">
        <div className="flex items-center justify-between mb-4">
          <div>
            <h3 className="font-semibold">Requisições por dia</h3>
            <p className="text-xs text-default-500 mt-0.5">Volume diário de chamadas</p>
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
            <XAxis dataKey="date" stroke="#666" tick={{ fontSize: 12 }} tickLine={false} axisLine={false} />
            <YAxis stroke="#666" tick={{ fontSize: 12 }} tickLine={false} axisLine={false} allowDecimals={false} />
            <Tooltip contentStyle={chartTooltipStyle} labelStyle={{ color: "#888" }} />
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
                <Pie data={byProvider} dataKey="value" nameKey="name" cx="50%" cy="50%" innerRadius={60} outerRadius={95} paddingAngle={2} label={({ name }) => name}>
                  {byProvider.map((_, i) => <Cell key={i} fill={PIE_COLORS[i % PIE_COLORS.length]} stroke="none" />)}
                </Pie>
                <Legend formatter={(v) => <span className="text-xs text-default-600">{v}</span>} />
                <Tooltip contentStyle={chartTooltipStyle} />
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
                <XAxis type="number" stroke="#666" tick={{ fontSize: 12 }} tickLine={false} axisLine={false} allowDecimals={false} />
                <YAxis type="category" dataKey="name" stroke="#666" tick={{ fontSize: 11 }} tickLine={false} axisLine={false} width={90} />
                <Tooltip contentStyle={chartTooltipStyle} cursor={{ fill: "#ffffff10" }} />
                <Bar dataKey="value" fill="#4DA3FF" radius={[0, 4, 4, 0]} barSize={20} />
              </BarChart>
            </ResponsiveContainer>
          )}
        </div>
      </div>
    </div>
  );
}

function StatCard({ label, value, sub }: { label: string; value: string | number; sub: string }) {
  return (
    <div className="bg-content1 rounded-2xl border border-default-100 p-5 hover:border-default-200 transition-colors">
      <p className="text-xs text-default-500 uppercase tracking-wide font-medium">{label}</p>
      <p className="text-3xl font-bold mt-2 tabular-nums">{value}</p>
      <p className="text-xs text-default-500 mt-1">{sub}</p>
    </div>
  );
}

function EmptyChart() {
  return <div className="h-[260px] flex items-center justify-center text-sm text-default-400">Sem dados</div>;
}