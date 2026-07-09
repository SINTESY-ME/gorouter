import { useEffect, useMemo, useState, useCallback } from "react";
import {
  Input, Spinner, Chip, Button, Modal, ModalContent, ModalHeader,
  ModalBody, ModalFooter, Select, SelectItem, Switch, useDisclosure,
} from "@heroui/react";
import { api, type ModelEntry, type Provider } from "../api";

const KINDS = ["llm", "embedding", "image", "tts", "stt", "rerank", "ocr", "video"];

const kindColor = (k: string): "primary" | "success" | "warning" | "danger" | "secondary" | "default" => {
  switch (k) {
    case "llm": return "primary";
    case "embedding": return "success";
    case "image": return "warning";
    case "tts": return "secondary";
    case "stt": return "danger";
    default: return "default";
  }
};

export default function Models() {
  const [items, setItems] = useState<ModelEntry[]>([]);
  const [providers, setProviders] = useState<Provider[]>([]);
  const [loading, setLoading] = useState(true);
  const [query, setQuery] = useState("");
  const [syncing, setSyncing] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const { isOpen, onOpen, onClose } = useDisclosure();
  const [addProviderId, setAddProviderId] = useState<string>("");
  const [addForm, setAddForm] = useState({ model_id: "", name: "", kind: "llm", context: 0 });

  const load = useCallback(() => {
    setLoading(true);
    setError(null);
    Promise.all([
      api.providers.list().catch(() => []),
      Promise.all(
        (items.length === 0 ? [] : Array.from(new Set(items.map((m) => m.provider_id)))).map(
          (pid) => api.providers.models(pid).catch(() => [] as ModelEntry[])
        )
      ),
    ]).then(([ps]) => {
      setProviders(ps as Provider[]);
      // Load models for all active providers
      const active = (ps as Provider[]).filter((p) => p.is_active);
      Promise.all(active.map((p) => api.providers.models(p.id).catch(() => [] as ModelEntry[])))
        .then((results) => {
          const all: ModelEntry[] = [];
          results.forEach((r) => all.push(...r));
          setItems(all);
        })
        .finally(() => setLoading(false));
    }).catch((e) => {
      setError(e?.message ?? "falha ao carregar");
      setLoading(false);
    });
  }, [items.length]);

  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    api.providers.list().then((ps) => {
      if (cancelled) return;
      const active = ps.filter((p) => p.is_active);
      setProviders(ps);
      return Promise.all(active.map((p) => api.providers.models(p.id).catch(() => [] as ModelEntry[])))
        .then((results) => {
          if (cancelled) return;
          const all: ModelEntry[] = [];
          results.forEach((r) => all.push(...r));
          setItems(all);
        });
    }).catch((e) => setError(e?.message ?? "falha"))
      .finally(() => setLoading(false));
    return () => { cancelled = true; };
  }, []);

  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase();
    if (!q) return items;
    return items.filter((m) =>
      m.id.toLowerCase().includes(q) ||
      m.provider_id.toLowerCase().includes(q) ||
      m.kind.toLowerCase().includes(q)
    );
  }, [items, query]);

  const groups = useMemo(() => {
    const order: string[] = [];
    const map: Record<string, ModelEntry[]> = {};
    for (const m of filtered) {
      const key = m.provider_id;
      if (!map[key]) { map[key] = []; order.push(key); }
      map[key].push(m);
    }
    for (const k of order) map[k].sort((a, b) => a.model_id.localeCompare(b.model_id));
    order.sort();
    return order.map((k) => ({ providerId: k, models: map[k] }));
  }, [filtered]);

  const sync = async (providerId: string) => {
    const p = providers.find((x) => x.provider_id === providerId);
    if (!p) return;
    setSyncing(p.id);
    try {
      const entries = await api.providers.syncModels(p.id);
      setItems((prev) => {
        const without = prev.filter((m) => m.provider_id !== providerId);
        return [...without, ...entries];
      });
    } catch (e: any) {
      setError(e?.message ?? "sync falhou");
    } finally {
      setSyncing(null);
    }
  };

  const toggleActive = async (m: ModelEntry) => {
    try {
      await api.models.update(m.id, { is_active: !m.is_active });
      setItems((prev) => prev.map((x) => x.id === m.id ? { ...x, IsActive: !x.is_active } : x));
    } catch (e: any) { setError(e?.message); }
  };

  const removeModel = async (m: ModelEntry) => {
    if (!confirm(`Excluir ${m.id}?`)) return;
    try {
      await api.models.remove(m.id);
      setItems((prev) => prev.filter((x) => x.id !== m.id));
    } catch (e: any) { setError(e?.message); }
  };

  const openAdd = (providerId: string) => {
    const p = providers.find((x) => x.provider_id === providerId);
    if (!p) return;
    setAddProviderId(p.id);
    setAddForm({ model_id: "", name: "", kind: "llm", context: 0 });
    onOpen();
  };

  const submitAdd = async () => {
    try {
      const entry = await api.providers.addModel(addProviderId, {
        model_id: addForm.model_id,
        name: addForm.name || undefined,
        kind: addForm.kind,
        context: addForm.context || undefined,
      });
      setItems((prev) => [...prev, entry]);
      onClose();
    } catch (e: any) { setError(e?.message); }
  };

  if (loading) {
    return <div className="flex justify-center py-20"><Spinner label="Carregando modelos..." /></div>;
  }

  return (
    <div className="space-y-5">
      <div className="flex justify-between items-end gap-4 flex-wrap">
        <div>
          <h1 className="text-2xl font-bold tracking-tight">Models</h1>
          <p className="text-sm text-default-500 mt-0.5">{items.length} modelos · {items.filter(m => m.is_active).length} ativos</p>
        </div>
        <Input
          isClearable
          value={query}
          onValueChange={setQuery}
          placeholder="Buscar modelo, provider, tipo..."
          className="max-w-xs"
          variant="bordered"
          startContent={<IconSearch />}
          aria-label="buscar modelos"
        />
      </div>

      {error && (
        <div className="bg-danger-50 border border-danger-200 text-danger-600 rounded-xl p-4 text-sm">{error}</div>
      )}

      {groups.length === 0 && (
        <div className="text-center py-20 text-default-500 text-sm">
          Nenhum modelo {query ? "corresponde à busca" : "disponível ainda"}. {!query && "Crie um provider e sincronize."}
        </div>
      )}

      <div className="space-y-6">
        {groups.map((g) => (
          <div key={g.providerId}>
            <div className="flex items-center gap-2 mb-2">
              <Chip size="sm" variant="flat" color="default" className="font-mono">{g.providerId}</Chip>
              <span className="text-xs text-default-400">{g.models.length} modelo{g.models.length === 1 ? "" : "s"}</span>
              <div className="flex gap-1 ml-auto">
                <Button size="sm" variant="flat" onPress={() => sync(g.providerId)} isLoading={syncing === providers.find(p => p.provider_id === g.providerId)?.id}>
                  Sincronizar
                </Button>
                <Button size="sm" variant="flat" color="primary" onPress={() => openAdd(g.providerId)}>+ Model</Button>
              </div>
            </div>
            <div className="space-y-1">
              {g.models.map((m) => (
                <div key={m.id} className="flex items-center gap-2 bg-content1 border border-default-100 rounded-lg px-3 py-2">
                  <code className="text-sm font-mono flex-1 truncate">{m.model_id}</code>
                  <Chip size="sm" variant="flat" color={kindColor(m.kind)}>{m.kind}</Chip>
                  <Chip size="sm" variant="bordered">{m.source}</Chip>
                  <Switch size="sm" isSelected={m.is_active} onChange={() => toggleActive(m)} />
                  <Button isIconOnly size="sm" variant="light" color="danger" onPress={() => removeModel(m)} aria-label="excluir">
                    <IconTrash />
                  </Button>
                </div>
              ))}
            </div>
          </div>
        ))}
      </div>

      <Modal isOpen={isOpen} onClose={onClose}>
        <ModalContent>
          <ModalHeader>Adicionar modelo</ModalHeader>
          <ModalBody className="gap-4">
            <Input label="Model ID" placeholder="ex: gpt-4o, whisper-1" value={addForm.model_id} onValueChange={(v) => setAddForm({ ...addForm, model_id: v })} />
            <Input label="Nome (opcional)" placeholder="nome display" value={addForm.name} onValueChange={(v) => setAddForm({ ...addForm, name: v })} />
            <Select label="Tipo" selectedKeys={[addForm.kind]} onChange={(e) => setAddForm({ ...addForm, kind: e.target.value })}>
              {KINDS.map((k) => <SelectItem key={k}>{k}</SelectItem>)}
            </Select>
            <Input type="number" label="Context (opcional)" value={String(addForm.context)} onValueChange={(v) => setAddForm({ ...addForm, context: parseInt(v) || 0 })} />
          </ModalBody>
          <ModalFooter>
            <Button variant="flat" onPress={onClose}>Cancelar</Button>
            <Button color="primary" onPress={submitAdd} isDisabled={!addForm.model_id}>Adicionar</Button>
          </ModalFooter>
        </ModalContent>
      </Modal>
    </div>
  );
}

function IconSearch() {
  return <svg className="w-4 h-4 text-default-400" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><circle cx="11" cy="11" r="8" /><path d="m21 21-4.3-4.3" /></svg>;
}
function IconTrash() {
  return <svg className="w-3.5 h-3.5" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><polyline points="3 6 5 6 21 6" /><path d="M19 6l-1.5 14a2 2 0 0 1-2 2H8.5a2 2 0 0 1-2-2L5 6" /></svg>;
}