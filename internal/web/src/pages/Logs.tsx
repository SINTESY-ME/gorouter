import { useEffect, useState } from "react";
import {
  Table, TableHeader, TableColumn, TableBody, TableRow, TableCell,
  Chip, Pagination, Spinner,
} from "@heroui/react";
import { api, type UsageEntry } from "../api";

const statusColor = (s: number): "success" | "warning" | "danger" => {
  if (s < 300) return "success";
  if (s < 500) return "warning";
  return "danger";
};

export default function Logs() {
  const [items, setItems] = useState<UsageEntry[]>([]);
  const [loading, setLoading] = useState(true);
  const [page, setPage] = useState(1);
  const perPage = 25;

  useEffect(() => {
    setLoading(true);
    api.usage.history(500)
      .then(setItems)
      .catch(() => setItems([]))
      .finally(() => setLoading(false));
  }, []);

  const paged = items.slice((page - 1) * perPage, page * perPage);

  return (
    <div className="space-y-5">
      <div>
        <h1 className="text-2xl font-bold tracking-tight">Logs de uso</h1>
        <p className="text-sm text-default-500 mt-0.5">{items.length} registros</p>
      </div>
      <div className="bg-content1 rounded-2xl border border-default-100 overflow-hidden">
        {loading ? (
          <div className="p-10 flex justify-center"><Spinner /></div>
        ) : items.length === 0 ? (
          <div className="p-10 text-center text-default-500 text-sm">Nenhum log ainda.</div>
        ) : (
          <Table aria-label="logs" removeWrapper>
            <TableHeader>
              <TableColumn>TIMESTAMP</TableColumn>
              <TableColumn>COMBO</TableColumn>
              <TableColumn>PROVIDER</TableColumn>
              <TableColumn>MODELO</TableColumn>
              <TableColumn>ENDPOINT</TableColumn>
              <TableColumn>TOKENS</TableColumn>
              <TableColumn>LATÊNCIA</TableColumn>
              <TableColumn>STATUS</TableColumn>
            </TableHeader>
            <TableBody items={paged}>
              {(e) => (
                <TableRow key={e.timestamp + e.model + Math.random()}>
                  <TableCell><span className="text-xs text-default-500">{new Date(e.timestamp).toLocaleString()}</span></TableCell>
                  <TableCell>{e.combo_name ? <code className="text-xs">{e.combo_name}</code> : <span className="text-default-400">—</span>}</TableCell>
                  <TableCell>{e.provider}</TableCell>
                  <TableCell><code className="text-xs">{e.model}</code></TableCell>
                  <TableCell><code className="text-xs text-default-500">{e.endpoint}</code></TableCell>
                  <TableCell className="tabular-nums">{e.prompt_tokens + e.completion_tokens}</TableCell>
                  <TableCell><span className="tabular-nums text-xs">{e.latency_ms ? `${e.latency_ms}ms` : "—"}</span></TableCell>
                  <TableCell><Chip size="sm" color={statusColor(e.status)} variant="flat">{e.status}</Chip></TableCell>
                </TableRow>
              )}
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