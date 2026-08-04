import { useEffect, useState, useCallback, useMemo } from "react";
import type { Selection } from "@heroui/react";
import {
  Table, Chip, Pagination, Spinner, Input, Select, ListBox, Button, cn,
  DateRangePicker, DateField, RangeCalendar,
} from "@heroui/react";
import type { CalendarDate } from "@internationalized/date";
import { Icon } from "@iconify/react";
import { api, type UsageEntry, type ApiKey } from "../api";
import { formatCompact, formatCost } from "../format";

const statusColor = (s: number): "success" | "warning" | "danger" | "default" => {
  if (s === 0) return "default";
  if (s < 300) return "success";
  if (s < 500) return "warning";
  return "danger";
};

const costColor = (cost: number): string => {
  if (cost <= 0) return "text-muted";
  if (cost < 0.001) return "text-success";
  if (cost < 0.01) return "text-foreground/80";
  return "text-danger";
};

interface LogRow {
  id: string;
  label: string;
  isCombo: boolean;
  timestamp: string;
  provider: string;
  model: string;
  tokens: number;
  cost: number;
  status: number;
  latency: number;
  ttft: number;
  tps: string | null;
  cacheHit: boolean;
  attempt: number;
  error?: string;
  children: LogRow[];
}

function toRow(e: UsageEntry, key: string): LogRow {
  const tokens = e.prompt_tokens + e.completion_tokens;
  const lat = e.latency_ms || 0;
  const ttft = e.ttft_ms || 0;
  const genMs = ttft > 0 && lat > ttft ? lat - ttft : lat;
  const tps = genMs > 0 && e.completion_tokens > 0 ? (e.completion_tokens * 1000 / genMs).toFixed(1) : null;
  return {
    id: `${key}-${e.id}`,
    label: e.combo_chain?.length ? `${e.combo_chain.join(" → ")} → ${e.model || "?"}` : (e.model || "—"),
    isCombo: !!e.combo_chain?.length,
    timestamp: e.timestamp,
    provider: e.provider || "—",
    model: e.model || "—",
    tokens,
    cost: e.cost,
    status: e.status,
    latency: lat,
    ttft,
    tps,
    cacheHit: !!e.cache_hit,
    attempt: e.attempt || 0,
    error: e.error,
    children: [],
  };
}

const PER_PAGE = 25;

export default function Logs() {
  const [items, setItems] = useState<UsageEntry[]>([]);
  const [apiKeys, setApiKeys] = useState<ApiKey[]>([]);
  const [loading, setLoading] = useState(true);
  const [page, setPage] = useState(1);
  const [expandedKeys, setExpandedKeys] = useState<Selection>(new Set());
  const [total, setTotal] = useState(0);
  const [hasMore, setHasMore] = useState(false);

  type DateRangeValue = { start: CalendarDate; end: CalendarDate } | null;
  const [dateRange, setDateRange] = useState<DateRangeValue>(null);
  const [modelFilter, setModelFilter] = useState("");
  const [comboFilter, setComboFilter] = useState("");
  const [keyFilter, setKeyFilter] = useState("");
  const [search, setSearch] = useState("");
  const [filterOptions, setFilterOptions] = useState<{ models: string[]; combos: string[] }>({ models: [], combos: [] });

  const modelOptions = filterOptions.models;
  const comboOptions = filterOptions.combos;

  const PER_PAGE = 25;

  const fetchLogs = useCallback(() => {
    setLoading(true);
    const params: Record<string, string | number> = { page, per_page: PER_PAGE };
    if (dateRange) {
      params.from = new Date(dateRange.start.toString()).toISOString();
      params.to = new Date(dateRange.end.toString()).toISOString();
    }
    if (modelFilter) params.model = modelFilter;
    if (comboFilter) params.combo = comboFilter;
    if (keyFilter) params.api_key = keyFilter;
    if (search) params.search = search;
    api.usage.history(params)
      .then((res) => {
        setItems(res.data ?? []);
        setTotal(res.total);
        setHasMore(res.has_more);
      })
      .catch(() => { setItems([]); setTotal(0); })
      .finally(() => setLoading(false));
  }, [page, dateRange, modelFilter, comboFilter, keyFilter, search]);

  useEffect(() => {
    api.keys.list().then(setApiKeys).catch(() => {});
    api.usage.filters().then((filters) => setFilterOptions({ models: filters.models, combos: filters.combos })).catch(() => {});
  }, []);

  useEffect(() => { fetchLogs(); }, [fetchLogs]);
  useEffect(() => { setPage(1); setExpandedKeys(new Set()); }, [dateRange, modelFilter, comboFilter, keyFilter, search]);

  const clearFilters = () => {
    setDateRange(null); setModelFilter("");
    setComboFilter(""); setKeyFilter(""); setSearch("");
  };

  const hasFilters = !!(dateRange || modelFilter || comboFilter || keyFilter || search);

  const rows: LogRow[] = useMemo(() => {
    const map = new Map<string, UsageEntry[]>();
    for (const e of items) {
      const key = e.request_id || String(e.id);
      if (!map.has(key)) map.set(key, []);
      map.get(key)!.push(e);
    }
    const groups: LogRow[] = [];
    for (const [key, entries] of map) {
      entries.sort((a, b) => (a.attempt ?? 0) - (b.attempt ?? 0));
      const primary = entries[entries.length - 1];
      const row = toRow(primary, key);
      if (entries.length > 1) {
        row.children = entries.slice(0, -1).reverse().map((e) => toRow(e, key));
      }
      groups.push(row);
    }
    groups.sort((a, b) => new Date(b.timestamp).getTime() - new Date(a.timestamp).getTime());
    return groups;
  }, [items]);

  const totalPages = Math.max(1, Math.ceil(total / PER_PAGE));
  const start = total === 0 ? 0 : (page - 1) * PER_PAGE + 1;
  const end = Math.min(page * PER_PAGE, total);

  // Sliding window of page numbers: always show the first and last page,
  // plus the neighbors of the current page, with ellipses for the gaps.
  // This keeps the pagination compact (max ~7 items) regardless of the
  // total page count, instead of rendering every page and overflowing.
  const getPageNumbers = (): (number | "ellipsis")[] => {
    const pages: (number | "ellipsis")[] = [];
    if (totalPages <= 7) {
      for (let i = 1; i <= totalPages; i++) pages.push(i);
      return pages;
    }
    pages.push(1);
    if (page > 3) pages.push("ellipsis");
    const startPage = Math.max(2, page - 1);
    const endPage = Math.min(totalPages - 1, page + 1);
    for (let i = startPage; i <= endPage; i++) pages.push(i);
    if (page < totalPages - 2) pages.push("ellipsis");
    pages.push(totalPages);
    return pages;
  };

  const renderRow = (item: LogRow) => (
    <Table.Row id={item.id} textValue={item.label}>
      <Table.Cell textValue={item.label}>
        {({ hasChildItems, isExpanded }: { hasChildItems?: boolean; isExpanded?: boolean }) => (
          <span className="flex items-center gap-1.5">
            {hasChildItems ? (
              <Button isIconOnly aria-label="Alternar" size="sm" slot="chevron" variant="ghost" className="size-5 min-w-0">
                <Icon
                  aria-hidden
                  icon="gravity-ui:chevron-right"
                  className={cn("size-3.5 text-muted transition-transform duration-150", isExpanded ? "rotate-90" : "")}
                />
              </Button>
            ) : (
              <span className="inline-block w-5" />
            )}
            <code className="text-xs">{item.label}</code>
            {item.attempt > 1 && (
              <Chip size="sm" color="warning" variant="soft" className="h-4 px-1 text-[9px] shrink-0" title={`${item.attempt} tentativas (retries/fallback)`}>
                {item.attempt - 1} fallback
              </Chip>
            )}
          </span>
        )}
      </Table.Cell>
      <Table.Cell><span className="text-xs text-muted">{new Date(item.timestamp).toLocaleString()}</span></Table.Cell>
      <Table.Cell><span className="text-xs">{item.provider}</span></Table.Cell>
      <Table.Cell className="tabular-nums" textValue={String(item.tokens)}>{formatCompact(item.tokens)}</Table.Cell>
      <Table.Cell textValue={String(item.cost)}>
        <span className={cn("tabular-nums text-xs", costColor(item.cost))} title={`$${item.cost.toFixed(6)}`}>
          {item.cost > 0 ? formatCost(item.cost) : "—"}
        </span>
      </Table.Cell>
      <Table.Cell textValue={String(item.status)}>
        <span className="flex items-center gap-1.5">
          <Chip size="sm" color={statusColor(item.status)} variant="soft">{item.status || "err"}</Chip>
          {item.error && <span className="text-[11px] text-danger truncate max-w-[160px]" title={item.error}>{item.error}</span>}
        </span>
      </Table.Cell>
      <Table.Cell><span className="tabular-nums text-xs">{item.latency > 0 ? `${item.latency}ms` : "—"}</span></Table.Cell>
      <Table.Collection items={item.children}>{renderRow}</Table.Collection>
    </Table.Row>
  );

  return (
    <div className="space-y-5">
      <div>
        <h1 className="text-2xl font-bold tracking-tight">Logs de uso</h1>
        <p className="text-sm text-muted mt-0.5">
          {rows.length} {hasFilters ? "registros filtrados" : "registros"}
        </p>
      </div>

      <div className="grid grid-cols-1 sm:grid-cols-2 md:grid-cols-3 lg:grid-cols-6 gap-3">
        <DateRangePicker
          aria-label="Filtrar por data"
          className="w-full lg:col-span-2"
          startName="startDate"
          endName="endDate"
          value={dateRange}
          onChange={setDateRange}
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
            <RangeCalendar aria-label="Filtrar por data">
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
        <FilterSelect label="Modelo" value={modelFilter} onChange={setModelFilter} options={modelOptions} />
        <FilterSelect label="Combo" value={comboFilter} onChange={setComboFilter} options={comboOptions} />
        <Select
          aria-label="Token"
          selectedKey={keyFilter || null}
          onSelectionChange={(k) => setKeyFilter((k as string) ?? "")}
        >
          <Select.Trigger><Select.Value>{keyFilter ? apiKeys.find((k) => k.key === keyFilter)?.name ?? "Token" : "Todos"}</Select.Value><Select.Indicator /></Select.Trigger>
          <Select.Popover>
            <ListBox>
              <ListBox.Item id="">Todos</ListBox.Item>
              {apiKeys.map((k) => <ListBox.Item key={k.key} id={k.key}>{k.name}</ListBox.Item>)}
            </ListBox>
          </Select.Popover>
        </Select>
        <Input aria-label="Buscar" placeholder="modelo, provider..." value={search} onChange={(e) => setSearch(e.target.value)} variant="secondary" />
      </div>

      {hasFilters && (
        <Button size="sm" variant="secondary" onPress={clearFilters}>Limpar filtros</Button>
      )}

      <Table>
        <Table.ScrollContainer>
          <Table.Content
            aria-label="Logs de uso"
            className="min-w-[820px]"
            expandedKeys={expandedKeys}
            treeColumn="label"
            onExpandedChange={setExpandedKeys}
          >
            <Table.Header>
              <Table.Column isRowHeader id="label">Registro</Table.Column>
              <Table.Column id="timestamp">Timestamp</Table.Column>
              <Table.Column id="provider">Provider</Table.Column>
              <Table.Column id="tokens">Tokens</Table.Column>
              <Table.Column id="cost">Custo</Table.Column>
              <Table.Column id="status">Status</Table.Column>
              <Table.Column id="latency">Latência</Table.Column>
            </Table.Header>
            <Table.Body items={rows} renderEmptyState={() => (
              <div className="p-10 text-center text-muted text-sm">
                {hasFilters ? "Nenhum registro encontrado." : "Nenhum log ainda."}
              </div>
            )}>
              {loading ? () => (
                <Table.Row id="loading"><Table.Cell colSpan={7}><div className="p-10 flex justify-center"><Spinner /></div></Table.Cell></Table.Row>
              ) : renderRow}
            </Table.Body>
          </Table.Content>
        </Table.ScrollContainer>
        {!loading && totalPages > 1 && (
          <Table.Footer>
            <Pagination size="sm">
              <Pagination.Summary>{start} a {end} de {total}</Pagination.Summary>
              <Pagination.Content>
                <Pagination.Item>
                  <Pagination.Previous isDisabled={page === 1} onPress={() => setPage((p) => Math.max(1, p - 1))}>
                    <Pagination.PreviousIcon />
                  </Pagination.Previous>
                </Pagination.Item>
                {getPageNumbers().map((p, i) =>
                  p === "ellipsis" ? (
                    <Pagination.Item key={`ellipsis-${i}`}>
                      <Pagination.Ellipsis />
                    </Pagination.Item>
                  ) : (
                    <Pagination.Item key={p}>
                      <Pagination.Link isActive={p === page} onPress={() => setPage(p)}>{p}</Pagination.Link>
                    </Pagination.Item>
                  ),
                )}
                <Pagination.Item>
                  <Pagination.Next isDisabled={page === totalPages} onPress={() => setPage((p) => Math.min(totalPages, p + 1))}>
                    <Pagination.NextIcon />
                  </Pagination.Next>
                </Pagination.Item>
              </Pagination.Content>
            </Pagination>
          </Table.Footer>
        )}
      </Table>
    </div>
  );
}

function FilterSelect({ label, value, onChange, options }: { label: string; value: string; onChange: (v: string) => void; options: string[] }) {
  return (
    <Select
      aria-label={label}
      selectedKey={value || null}
      onSelectionChange={(k) => onChange((k as string) ?? "")}
    >
      <Select.Trigger>
        <Select.Value>{value || "Todos"}</Select.Value>
        <Select.Indicator />
      </Select.Trigger>
      <Select.Popover>
        <ListBox>
          <ListBox.Item id="">Todos</ListBox.Item>
          {options.map((o) => <ListBox.Item key={o} id={o}>{o}</ListBox.Item>)}
        </ListBox>
      </Select.Popover>
    </Select>
  );
}
