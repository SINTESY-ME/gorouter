import { useEffect, useState, useCallback } from "react";
import {
  Table, TableHeader, TableColumn, TableBody, TableRow, TableCell,
  Chip, Pagination, Spinner, Input, Select, SelectItem, Button,
} from "@heroui/react";
import { api, type UsageEntry, type ApiKey } from "../api";
import { formatCompact, formatCost } from "../format";

const statusColor = (s: number): "success" | "warning" | "danger" => {
  if (s < 300) return "success";
  if (s < 500) return "warning";
  return "danger";
};

const costColor = (cost: number): string => {
  if (cost <= 0) return "text-default-400";
  if (cost < 0.001) return "text-success-600";
  if (cost < 0.01) return "text-default-600";
  return "text-danger";
};

function maskKey(k: string): string {
  if (!k) return "—";
  if (k.length <= 12) return k.slice(0, 3) + "..." + k.slice(-2);
  return k.slice(0, 6) + "..." + k.slice(-4);
}

export default function Logs() {
  const [items, setItems] = useState<UsageEntry[]>([]);
  const [apiKeys, setApiKeys] = useState<ApiKey[]>([]);
  const [loading, setLoading] = useState(true);
  const [page, setPage] = useState(1);
  const perPage = 25;

  // Filters
  const [fromDate, setFromDate] = useState("");
  const [toDate, setToDate] = useState("");
  const [modelFilter, setModelFilter] = useState("");
  const [comboFilter, setComboFilter] = useState("");
  const [keyFilter, setKeyFilter] = useState("");
  const [search, setSearch] = useState("");

  // Derive options from loaded data
  const modelOptions = [...new Set(items.map((e) => e.model).filter(Boolean))].sort();
  const comboOptions = [...new Set(items.flatMap((e) => e.combo_chain ?? []).filter(Boolean))].sort();

  const fetchLogs = useCallback(() => {
    setLoading(true);
    const params: Record<string, string | number> = { limit: 500 };
    if (fromDate) params.from = new Date(fromDate).toISOString();
    if (toDate) params.to = new Date(toDate).toISOString() ;
    if (modelFilter) params.model = modelFilter;
    if (comboFilter) params.combo = comboFilter;
    if (keyFilter) params.api_key = keyFilter;
    if (search) params.search = search;
    api.usage.history(params)
      .then(setItems)
      .catch(() => setItems([]))
      .finally(() => setLoading(false));
  }, [fromDate, toDate, modelFilter, comboFilter, keyFilter, search]);

  useEffect(() => {
    api.keys.list().then(setApiKeys).catch(() => {});
  }, []);

  useEffect(() => { fetchLogs(); }, [fetchLogs]);
  useEffect(() => { setPage(1); }, [fromDate, toDate, modelFilter, comboFilter, keyFilter, search]);

  const clearFilters = () => {
    setFromDate(""); setToDate(""); setModelFilter("");
    setComboFilter(""); setKeyFilter(""); setSearch("");
  };

  const hasFilters = fromDate || toDate || modelFilter || comboFilter || keyFilter || search;
  const paged = items.slice((page - 1) * perPage, page * perPage);

  return (
    <div className="space-y-5">
      <div>
        <h1 className="text-2xl font-bold tracking-tight">Logs de uso</h1>
        <p className="text-sm text-default-500 mt-0.5">
          {items.length} {hasFilters ? "registros filtrados" : "registros"}
        </p>
      </div>

      {/* Filters */}
      <div className="grid grid-cols-1 sm:grid-cols-2 md:grid-cols-3 lg:grid-cols-6 gap-3">
        <Input
          type="date"
          label="De"
          size="sm"
          value={fromDate}
          onValueChange={setFromDate}
        />
        <Input
          type="date"
          label="Até"
          size="sm"
          value={toDate}
          onValueChange={setToDate}
        />
        <Select
          label="Modelo"
          size="sm"
          selectedKeys={modelFilter ? [modelFilter] : [""]}
          onChange={(e) => setModelFilter(e.target.value)}
          disallowEmptySelection
        >
          {[<SelectItem key="">Todos</SelectItem>, ...modelOptions.map((m) => <SelectItem key={m}>{m}</SelectItem>)]}
        </Select>
        <Select
          label="Combo"
          size="sm"
          selectedKeys={comboFilter ? [comboFilter] : [""]}
          onChange={(e) => setComboFilter(e.target.value)}
          disallowEmptySelection
        >
          {[<SelectItem key="">Todos</SelectItem>, ...comboOptions.map((c) => <SelectItem key={c}>{c}</SelectItem>)]}
        </Select>
        <Select
          label="Token"
          size="sm"
          selectedKeys={keyFilter ? [keyFilter] : [""]}
          onChange={(e) => setKeyFilter(e.target.value)}
          disallowEmptySelection
        >
          {[<SelectItem key="">Todos</SelectItem>, ...apiKeys.map((k) => <SelectItem key={k.key}>{k.name}</SelectItem>)]}
        </Select>
        <Input
          label="Buscar"
          size="sm"
          placeholder="modelo, provider..."
          value={search}
          onValueChange={setSearch}
          isClearable
        />
      </div>

      {hasFilters && (
        <Button size="sm" variant="flat" onPress={clearFilters}>
          Limpar filtros
        </Button>
      )}

      <div className="bg-content1 rounded-2xl border border-default-100 overflow-hidden">
        {loading ? (
          <div className="p-10 flex justify-center"><Spinner /></div>
        ) : items.length === 0 ? (
          <div className="p-10 text-center text-default-500 text-sm">
            {hasFilters ? "Nenhum registro encontrado." : "Nenhum log ainda."}
          </div>
        ) : (
          <Table aria-label="logs" removeWrapper>
            <TableHeader>
              <TableColumn>TIMESTAMP</TableColumn>
              <TableColumn>COMBO</TableColumn>
              <TableColumn>PROVIDER</TableColumn>
              <TableColumn>MODELO</TableColumn>
              <TableColumn>ENDPOINT</TableColumn>
              <TableColumn>TOKENS</TableColumn>
              <TableColumn>CUSTO</TableColumn>
              <TableColumn>TPS</TableColumn>
              <TableColumn>TTFT</TableColumn>
              <TableColumn>LATÊNCIA</TableColumn>
              <TableColumn>STATUS</TableColumn>
              <TableColumn>CACHE</TableColumn>
            </TableHeader>
            <TableBody items={paged}>
              {(e) => {
                const totalTokens = e.prompt_tokens + e.completion_tokens;
                const lat = e.latency_ms || 0;
                const ttft = e.ttft_ms || 0;
                const genMs = ttft > 0 && lat > ttft ? lat - ttft : lat;
                const tps = genMs > 0 && e.completion_tokens > 0
                  ? (e.completion_tokens * 1000 / genMs).toFixed(1)
                  : null;
                return (
                <TableRow key={e.id}>
                  <TableCell><span className="text-xs text-default-500">{new Date(e.timestamp).toLocaleString()}</span></TableCell>
                  <TableCell>{e.combo_chain?.length ? <code className="text-xs">{e.combo_chain.join(" → ")}</code> : <span className="text-default-400">—</span>}</TableCell>
                  <TableCell>{e.provider}</TableCell>
                  <TableCell><code className="text-xs">{e.model}</code></TableCell>
                  <TableCell><code className="text-xs text-default-500">{e.endpoint}</code></TableCell>
                  <TableCell className="tabular-nums" title={totalTokens.toLocaleString("en-US")}>{formatCompact(totalTokens)}</TableCell>
                  <TableCell><span className={`tabular-nums text-xs ${costColor(e.cost)}`} title={`$${e.cost.toFixed(6)}`}>{e.cost > 0 ? formatCost(e.cost) : "—"}</span></TableCell>
                  <TableCell><span className="tabular-nums text-xs">{tps ? `${tps}` : "—"}</span></TableCell>
                  <TableCell><span className="tabular-nums text-xs">{ttft > 0 ? `${ttft}ms` : "—"}</span></TableCell>
                  <TableCell><span className="tabular-nums text-xs">{lat > 0 ? `${lat}ms` : "—"}</span></TableCell>
                  <TableCell><Chip size="sm" color={statusColor(e.status)} variant="flat">{e.status}</Chip></TableCell>
                  <TableCell>{e.cache_hit ? <Chip size="sm" color="success" variant="flat">hit</Chip> : <span className="text-default-400">—</span>}</TableCell>
                </TableRow>
                );
              }}
            </TableBody>
          </Table>
        )}
      </div>
      {!loading && items.length > perPage && (
        <div className="flex justify-center">
          <Pagination total={Math.ceil(items.length / perPage)} page={page} onChange={setPage} />
        </div>
      )}
    </div>
  );
}