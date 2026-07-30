import { useEffect, useState, useCallback, useMemo } from "react";
import {
  Table, TableHeader, TableColumn, TableBody, TableRow, TableCell,
  Chip, Pagination, Spinner, Input, Select, SelectItem, Button,
} from "@heroui/react";
import { api, type UsageEntry, type ApiKey } from "../api";
import { formatCompact, formatCost } from "../format";

const statusColor = (s: number): "success" | "warning" | "danger" | "default" => {
  if (s === 0) return "default";
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

interface RequestGroup {
  key: string;
  entries: UsageEntry[];
  primary: UsageEntry;
}

export default function Logs() {
  const [items, setItems] = useState<UsageEntry[]>([]);
  const [apiKeys, setApiKeys] = useState<ApiKey[]>([]);
  const [loading, setLoading] = useState(true);
  const [page, setPage] = useState(1);
  const [expanded, setExpanded] = useState<Set<string>>(new Set());
  const perPage = 25;

  const [fromDate, setFromDate] = useState("");
  const [toDate, setToDate] = useState("");
  const [modelFilter, setModelFilter] = useState("");
  const [comboFilter, setComboFilter] = useState("");
  const [keyFilter, setKeyFilter] = useState("");
  const [search, setSearch] = useState("");

  const modelOptions = [...new Set(items.map((e) => e.model).filter(Boolean))].sort();
  const comboOptions = [...new Set(items.flatMap((e) => e.combo_chain ?? []).filter(Boolean))].sort();

  const fetchLogs = useCallback(() => {
    setLoading(true);
    const params: Record<string, string | number> = { limit: 500 };
    if (fromDate) params.from = new Date(fromDate).toISOString();
    if (toDate) params.to = new Date(toDate).toISOString();
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
  useEffect(() => { setPage(1); setExpanded(new Set()); }, [fromDate, toDate, modelFilter, comboFilter, keyFilter, search]);

  const clearFilters = () => {
    setFromDate(""); setToDate(""); setModelFilter("");
    setComboFilter(""); setKeyFilter(""); setSearch("");
  };

  const hasFilters = fromDate || toDate || modelFilter || comboFilter || keyFilter || search;

  const groups: RequestGroup[] = useMemo(() => {
    const map = new Map<string, UsageEntry[]>();
    for (const e of items) {
      const key = e.request_id || String(e.id);
      if (!map.has(key)) map.set(key, []);
      map.get(key)!.push(e);
    }
    const result: RequestGroup[] = [];
    for (const [key, entries] of map) {
      entries.sort((a, b) => (a.attempt ?? 0) - (b.attempt ?? 0));
      result.push({ key, entries, primary: entries[entries.length - 1] });
    }
    result.sort((a, b) => new Date(b.primary.timestamp).getTime() - new Date(a.primary.timestamp).getTime());
    return result;
  }, [items]);

  const paged = groups.slice((page - 1) * perPage, page * perPage);

  const toggleExpand = (key: string) => {
    setExpanded((prev) => {
      const next = new Set(prev);
      if (next.has(key)) next.delete(key);
      else next.add(key);
      return next;
    });
  };

  return (
    <div className="space-y-5">
      <div>
        <h1 className="text-2xl font-bold tracking-tight">Logs de uso</h1>
        <p className="text-sm text-default-500 mt-0.5">
          {groups.length} {hasFilters ? "registros filtrados" : "registros"}
        </p>
      </div>

      <div className="grid grid-cols-1 sm:grid-cols-2 md:grid-cols-3 lg:grid-cols-6 gap-3">
        <Input type="date" label="De" size="sm" value={fromDate} onValueChange={setFromDate} />
        <Input type="date" label="Até" size="sm" value={toDate} onValueChange={setToDate} />
        <Select label="Modelo" size="sm" selectedKeys={modelFilter ? [modelFilter] : [""]} onChange={(e) => setModelFilter(e.target.value)} disallowEmptySelection>
          {[<SelectItem key="">Todos</SelectItem>, ...modelOptions.map((m) => <SelectItem key={m}>{m}</SelectItem>)]}
        </Select>
        <Select label="Combo" size="sm" selectedKeys={comboFilter ? [comboFilter] : [""]} onChange={(e) => setComboFilter(e.target.value)} disallowEmptySelection>
          {[<SelectItem key="">Todos</SelectItem>, ...comboOptions.map((c) => <SelectItem key={c}>{c}</SelectItem>)]}
        </Select>
        <Select label="Token" size="sm" selectedKeys={keyFilter ? [keyFilter] : [""]} onChange={(e) => setKeyFilter(e.target.value)} disallowEmptySelection>
          {[<SelectItem key="">Todos</SelectItem>, ...apiKeys.map((k) => <SelectItem key={k.key}>{k.name}</SelectItem>)]}
        </Select>
        <Input label="Buscar" size="sm" placeholder="modelo, provider..." value={search} onValueChange={setSearch} isClearable />
      </div>

      {hasFilters && (
        <Button size="sm" variant="flat" onPress={clearFilters}>Limpar filtros</Button>
      )}

      <div className="bg-content1 rounded-2xl border border-default-100 overflow-hidden">
        {loading ? (
          <div className="p-10 flex justify-center"><Spinner /></div>
        ) : groups.length === 0 ? (
          <div className="p-10 text-center text-default-500 text-sm">
            {hasFilters ? "Nenhum registro encontrado." : "Nenhum log ainda."}
          </div>
        ) : (
          <Table aria-label="logs" removeWrapper>
            <TableHeader>
              <TableColumn> </TableColumn>
              <TableColumn>TIMESTAMP</TableColumn>
              <TableColumn>COMBO</TableColumn>
              <TableColumn>PROVIDER</TableColumn>
              <TableColumn>MODELO</TableColumn>
              <TableColumn>TOKENS</TableColumn>
              <TableColumn>CUSTO</TableColumn>
              <TableColumn>TPS</TableColumn>
              <TableColumn>TTFT</TableColumn>
              <TableColumn>LATÊNCIA</TableColumn>
              <TableColumn>STATUS</TableColumn>
              <TableColumn>CACHE</TableColumn>
            </TableHeader>
            <TableBody items={paged}>
              {(g) => {
                const e = g.primary;
                const totalTokens = e.prompt_tokens + e.completion_tokens;
                const lat = e.latency_ms || 0;
                const ttft = e.ttft_ms || 0;
                const genMs = ttft > 0 && lat > ttft ? lat - ttft : lat;
                const tps = genMs > 0 && e.completion_tokens > 0
                  ? (e.completion_tokens * 1000 / genMs).toFixed(1)
                  : null;
                const hasAttempts = g.entries.length > 1;
                const isOpen = expanded.has(g.key);
                return (
                <TableRow key={g.key}>
                  <TableCell>
                    {hasAttempts ? (
                      <button
                        onClick={() => toggleExpand(g.key)}
                        className="w-5 h-5 flex items-center justify-center rounded text-default-400 hover:text-default-600 hover:bg-default-100 transition-colors text-xs font-mono"
                      >
                        {isOpen ? "−" : "+"}
                      </button>
                    ) : null}
                  </TableCell>
                  <TableCell><span className="text-xs text-default-500">{new Date(e.timestamp).toLocaleString()}</span></TableCell>
                  <TableCell>{e.combo_chain?.length ? <code className="text-xs">{e.combo_chain.join(" → ")}</code> : <span className="text-default-400">—</span>}</TableCell>
                  <TableCell>{e.provider || <span className="text-default-400">—</span>}</TableCell>
                  <TableCell><code className="text-xs">{e.model || "—"}</code></TableCell>
                  <TableCell className="tabular-nums" title={totalTokens.toLocaleString("en-US")}>{formatCompact(totalTokens)}</TableCell>
                  <TableCell><span className={`tabular-nums text-xs ${costColor(e.cost)}`} title={`$${e.cost.toFixed(6)}`}>{e.cost > 0 ? formatCost(e.cost) : "—"}</span></TableCell>
                  <TableCell><span className="tabular-nums text-xs">{tps ? `${tps}` : "—"}</span></TableCell>
                  <TableCell><span className="tabular-nums text-xs">{ttft > 0 ? `${ttft}ms` : "—"}</span></TableCell>
                  <TableCell><span className="tabular-nums text-xs">{lat > 0 ? `${lat}ms` : "—"}</span></TableCell>
                  <TableCell><Chip size="sm" color={statusColor(e.status)} variant="flat">{e.status || "err"}</Chip></TableCell>
                  <TableCell>{e.cache_hit ? <Chip size="sm" color="success" variant="flat">hit</Chip> : <span className="text-default-400">—</span>}</TableCell>
                </TableRow>
                );
              }}
            </TableBody>
          </Table>
        )}
      </div>

      {!loading && paged.some((g) => expanded.has(g.key)) && (
        <div className="space-y-3">
          {paged.filter((g) => expanded.has(g.key)).map((g) => (
            <AttemptTree key={g.key} group={g} />
          ))}
        </div>
      )}

      {!loading && groups.length > perPage && (
        <div className="flex justify-center">
          <Pagination total={Math.ceil(groups.length / perPage)} page={page} onChange={setPage} />
        </div>
      )}
    </div>
  );
}

function AttemptTree({ group }: { group: RequestGroup }) {
  const { entries, primary } = group;
  return (
    <div className="bg-content1 rounded-2xl border border-default-100 p-4">
      <div className="flex items-center gap-2 mb-3">
        <span className="text-xs font-medium text-default-500">
          Tentativas — {new Date(primary.timestamp).toLocaleString()}
        </span>
        <Chip size="sm" variant="flat" color={statusColor(primary.status)}>{primary.status || "err"}</Chip>
      </div>
      <div className="space-y-1">
        {entries.map((e, i) => {
          const isLast = i === entries.length - 1;
          const ok = e.status > 0 && e.status < 400;
          const depth = (e.combo_chain?.length ?? 0);
          return (
            <div
              key={e.id}
              className="flex items-center gap-2 text-xs py-1 px-2 rounded-lg"
              style={{ paddingLeft: `${8 + depth * 16}px`, background: isLast ? "var(--heroui-default-100)" : "transparent" }}
            >
              <span className="text-default-400 font-mono w-4 shrink-0">{i + 1}.</span>
              {e.combo_chain?.length ? (
                <code className="text-default-500 shrink-0">{e.combo_chain.join(" → ")}</code>
              ) : null}
              <span className="text-default-300">→</span>
              <code className="font-medium shrink-0">{e.model || "combo"}</code>
              {e.provider && <span className="text-default-400">({e.provider})</span>}
              <Chip size="sm" variant="flat" color={statusColor(e.status)} className="shrink-0">
                {e.status || "err"}
              </Chip>
              {e.latency_ms ? <span className="text-default-400 tabular-nums">{e.latency_ms}ms</span> : null}
              {e.error && <span className="text-danger truncate max-w-[300px]" title={e.error}>{e.error}</span>}
              {ok && <span className="text-success">✓</span>}
            </div>
          );
        })}
      </div>
    </div>
  );
}
