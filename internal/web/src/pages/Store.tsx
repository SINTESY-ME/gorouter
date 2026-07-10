import { useEffect, useState } from "react";
import { Button, Chip, Input, Spinner } from "@heroui/react";
import { api, type StoreEntry } from "../api";

export default function Store() {
  const [items, setItems] = useState<StoreEntry[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [query, setQuery] = useState("");
  const [busy, setBusy] = useState<string | null>(null);

  const load = () => {
    setLoading(true);
    setError("");
    api.providers.store.list()
      .then(setItems)
      .catch((e) => setError(e?.message ?? "falha ao carregar loja"))
      .finally(() => setLoading(false));
  };
  useEffect(load, []);

  const install = async (id: string) => {
    setBusy(id);
    try {
      await api.providers.store.install(id);
      load();
    } catch (e: any) {
      setError(e?.message ?? "install failed");
    } finally {
      setBusy(null);
    }
  };

  const remove = async (id: string) => {
    if (!confirm(`Remover preset ${id}? (conexões existentes não são apagadas)`)) return;
    setBusy(id);
    try {
      await api.providers.store.remove(id);
      load();
    } catch (e: any) {
      setError(e?.message ?? "remove failed");
    } finally {
      setBusy(null);
    }
  };

  const q = query.trim().toLowerCase();
  const filtered = !q
    ? items
    : items.filter((i) =>
        i.id.toLowerCase().includes(q) ||
        i.name.toLowerCase().includes(q) ||
        i.category?.toLowerCase().includes(q) ||
        i.capabilities?.some((c) => c.toLowerCase().includes(q))
      );

  return (
    <div className="space-y-5">
      <div className="flex justify-between items-end gap-4 flex-wrap">
        <div>
          <h1 className="text-2xl font-bold tracking-tight">Store</h1>
          <p className="text-sm text-default-500 mt-0.5">
            Presets do repositório origin · instale e use em Providers
          </p>
        </div>
        <div className="flex gap-2">
          <Input
            isClearable
            value={query}
            onValueChange={setQuery}
            placeholder="Buscar..."
            className="max-w-xs"
            variant="bordered"
            size="sm"
          />
          <Button size="sm" variant="flat" onPress={load}>Atualizar</Button>
        </div>
      </div>

      {error && (
        <div className="bg-danger-50 border border-danger-200 text-danger-600 rounded-xl p-4 text-sm">{error}</div>
      )}

      {loading ? (
        <div className="flex justify-center py-20"><Spinner label="Carregando store..." /></div>
      ) : filtered.length === 0 ? (
        <div className="text-center py-20 text-default-500 text-sm">
          Nenhum provider na loja. Push YAMLs em <code>providers/</code> no repo.
        </div>
      ) : (
        <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-3">
          {filtered.map((e) => (
            <div key={e.id} className="bg-content1 rounded-2xl border border-default-100 p-4 flex flex-col gap-3">
              <div className="flex items-start justify-between gap-2">
                <div className="min-w-0">
                  <div className="flex items-center gap-2">
                    <span className="w-2.5 h-2.5 rounded-full shrink-0" style={{ background: e.color || "#888" }} />
                    <h3 className="font-semibold truncate">{e.name || e.id}</h3>
                  </div>
                  <p className="text-xs text-default-400 font-mono mt-0.5">{e.id}</p>
                </div>
                <Chip size="sm" variant="flat" color={e.installed ? "success" : "default"}>
                  {e.installed ? "instalado" : "disponível"}
                </Chip>
              </div>
              {e.category && (
                <Chip size="sm" variant="bordered">{e.category}</Chip>
              )}
              {e.capabilities && e.capabilities.length > 0 && (
                <div className="flex flex-wrap gap-1">
                  {e.capabilities.map((c) => (
                    <Chip key={c} size="sm" variant="flat" className="h-5 text-[10px]">{c}</Chip>
                  ))}
                </div>
              )}
              <div className="mt-auto pt-1">
                {e.installed ? (
                  <Button size="sm" variant="flat" color="danger" className="w-full" isLoading={busy === e.id} onPress={() => remove(e.id)}>
                    Remover
                  </Button>
                ) : (
                  <Button size="sm" color="primary" variant="bordered" className="w-full" isLoading={busy === e.id} onPress={() => install(e.id)}>
                    Instalar
                  </Button>
                )}
              </div>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
