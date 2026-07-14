import { useEffect, useMemo, useState, useCallback } from "react";
import {
  Input, Spinner, Chip, Button, Modal, ModalContent, ModalHeader,
  ModalBody, ModalFooter, Select, SelectItem, useDisclosure,
} from "@heroui/react";
import { api, type ModelEntry, type Provider, type ModelStat, type ModelPricing } from "../api";
import { formatCompact } from "../format";

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

// formatPrice converts a per-token price to $X.XX per 1M tokens.
// Returns null when the price is 0/missing.
const formatPricePer1M = (perToken: number | undefined): string | null => {
  if (!perToken || perToken <= 0) return null;
  const per1M = perToken * 1_000_000;
  if (per1M < 0.01) return `$${per1M.toFixed(4)}/1M`;
  return `$${per1M.toFixed(2)}/1M`;
};
const formatPricePerImage = (perImage: number | undefined): string | null => {
  if (!perImage || perImage <= 0) return null;
  return `$${perImage.toFixed(4)}/img`;
};

export default function Models() {
  const [items, setItems] = useState<ModelEntry[]>([]);
  const [stats, setStats] = useState<Record<string, ModelStat>>({});
  const [providers, setProviders] = useState<Provider[]>([]);
  const [loading, setLoading] = useState(true);
  const [query, setQuery] = useState("");
  const [syncing, setSyncing] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const { isOpen, onOpen, onClose } = useDisclosure();
  const [addProviderId, setAddProviderId] = useState<string>("");
  const [addForm, setAddForm] = useState({ model_id: "", name: "", kind: "llm", context: 0 });
  const { isOpen: pricingOpen, onOpen: onPricingOpen, onClose: onPricingClose } = useDisclosure();
  const [pricingModel, setPricingModel] = useState<ModelEntry | null>(null);
  const [pricingForm, setPricingForm] = useState({ inputPer1M: "", outputPer1M: "", perImage: "" });

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
          api.models.stats().then(setStats).catch(() => {});
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
    for (const k of order) map[k].sort((a, b) => a.id.localeCompare(b.id));
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
      setItems((prev) => prev.map((x) => x.id === m.id ? { ...x, is_active: !x.is_active } : x));
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

  const openPricing = (m: ModelEntry) => {
    setPricingModel(m);
    const p = m.pricing || {};
    setPricingForm({
      inputPer1M: p.input_cost_per_token ? String((p.input_cost_per_token * 1_000_000).toFixed(2)) : "",
      outputPer1M: p.output_cost_per_token ? String((p.output_cost_per_token * 1_000_000).toFixed(2)) : "",
      perImage: p.output_cost_per_image ? String(p.output_cost_per_image) : "",
    });
    onPricingOpen();
  };

  const submitPricing = async () => {
    if (!pricingModel) return;
    const pricing: ModelPricing = {
      input_cost_per_token: parseFloat(pricingForm.inputPer1M) ? parseFloat(pricingForm.inputPer1M) / 1_000_000 : 0,
      output_cost_per_token: parseFloat(pricingForm.outputPer1M) ? parseFloat(pricingForm.outputPer1M) / 1_000_000 : 0,
      output_cost_per_image: parseFloat(pricingForm.perImage) ? parseFloat(pricingForm.perImage) : 0,
    };
    try {
      const updated = await api.models.pricing(pricingModel.id, pricing);
      setItems((prev) => prev.map((x) => x.id === pricingModel.id ? { ...x, pricing: updated.pricing } : x));
      onPricingClose();
    } catch (e: any) { setError(e?.message); }
  };

  // Extract bare model id for stats lookup (strip provider prefix)
  const statKey = (m: ModelEntry) => {
    const parts = m.id.split("/");
    return parts.length > 1 ? parts[1] : m.id;
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
            <div className="flex items-center gap-2 mb-3">
              <Chip size="sm" variant="flat" color="default" className="font-mono">{g.providerId}</Chip>
              <span className="text-xs text-default-400">{g.models.length} modelo{g.models.length === 1 ? "" : "s"}</span>
              <div className="flex gap-1 ml-auto">
                <Button size="sm" variant="flat" onPress={() => sync(g.providerId)} isLoading={syncing === providers.find(p => p.provider_id === g.providerId)?.id}>
                  Sincronizar
                </Button>
                <Button size="sm" variant="flat" color="primary" onPress={() => openAdd(g.providerId)}>+ Model</Button>
              </div>
            </div>
            <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4 gap-2">
              {g.models.map((m) => {
                const st = stats[statKey(m)] || stats[m.id];
                return (
                  <div
                    key={m.id}
                    className="group relative bg-content1 border border-default-100 rounded-xl p-3 hover:border-default-200 transition-colors"
                  >
                    <div className="flex items-start justify-between gap-2">
                      <code className="text-sm font-mono truncate flex-1" title={m.id}>{m.id}</code>
                      <span
                        className={`w-2 h-2 rounded-full shrink-0 mt-1 ${m.is_active ? "bg-success" : "bg-default-300"}`}
                        title={m.is_active ? "ativo" : "inativo"}
                      />
                    </div>
                    <div className="flex items-center gap-1.5 mt-2">
                      <Chip size="sm" variant="flat" color={kindColor(m.kind)} className="h-5 text-[10px]">{m.kind}</Chip>
                      <span className="text-[10px] text-default-400">{m.source}</span>
                    </div>
                    {st && st.requests > 0 && (
                      <div className="flex items-center gap-3 mt-2 text-[10px] text-default-500">
                        <span className="tabular-nums">{st.avg_tps > 0 ? `${st.avg_tps.toFixed(1)} tok/s` : "—"}</span>
                        <span className="tabular-nums">{st.avg_latency_ms > 0 ? `${Math.round(st.avg_latency_ms)}ms` : "—"}</span>
                        <span className="tabular-nums">{st.requests > 999 ? formatCompact(st.requests) : `${st.requests}x`}</span>
                      </div>
                    )}
                    {(() => {
                      const p = m.pricing;
                      if (!p) return null;
                      const inPrice = formatPricePer1M(p.input_cost_per_token);
                      const outPrice = formatPricePer1M(p.output_cost_per_token);
                      const imgPrice = formatPricePerImage(p.output_cost_per_image);
                      if (!inPrice && !outPrice && !imgPrice) return null;
                      return (
                        <div className="flex items-center gap-2 mt-1.5 text-[10px]">
                          {inPrice && <span className="tabular-nums text-success-600">{inPrice}</span>}
                          {outPrice && <span className="tabular-nums text-primary">{outPrice}</span>}
                          {imgPrice && <span className="tabular-nums text-warning">{imgPrice}</span>}
                        </div>
                      );
                    })()}
                    {/* Hover actions */}
                    <div className="absolute top-2 right-2 opacity-0 group-hover:opacity-100 transition-opacity flex gap-0.5">
                      <button
                        onClick={() => openPricing(m)}
                        className="w-6 h-6 rounded-md hover:bg-default-100 flex items-center justify-center"
                        title="Editar preço"
                      >
                        <IconDollar />
                      </button>
                      <button
                        onClick={() => toggleActive(m)}
                        className="w-6 h-6 rounded-md hover:bg-default-100 flex items-center justify-center"
                        title={m.is_active ? "Desativar" : "Ativar"}
                      >
                        <IconPower active={m.is_active} />
                      </button>
                      <button
                        onClick={() => removeModel(m)}
                        className="w-6 h-6 rounded-md hover:bg-danger-100 text-danger flex items-center justify-center"
                        title="Excluir"
                      >
                        <IconTrash />
                      </button>
                    </div>
                  </div>
                );
              })}
            </div>
          </div>
        ))}
      </div>

      <Modal isOpen={pricingOpen} onClose={onPricingClose}>
        <ModalContent>
          <ModalHeader>Editar preço — {pricingModel?.id}</ModalHeader>
          <ModalBody className="gap-4">
            <Input
              type="number"
              label="Input ($ / 1M tokens)"
              placeholder="ex: 2.50"
              value={pricingForm.inputPer1M}
              onValueChange={(v) => setPricingForm({ ...pricingForm, inputPer1M: v })}
              step="0.01"
            />
            <Input
              type="number"
              label="Output ($ / 1M tokens)"
              placeholder="ex: 10.00"
              value={pricingForm.outputPer1M}
              onValueChange={(v) => setPricingForm({ ...pricingForm, outputPer1M: v })}
              step="0.01"
            />
            <Input
              type="number"
              label="Por imagem ($ — image gen only)"
              placeholder="ex: 0.04"
              value={pricingForm.perImage}
              onValueChange={(v) => setPricingForm({ ...pricingForm, perImage: v })}
              step="0.01"
            />
            <p className="text-xs text-default-500">
              Preços em USD por 1 milhão de tokens. Deixe em branco para zerar.
            </p>
          </ModalBody>
          <ModalFooter>
            <Button color="primary" onPress={submitPricing}>Salvar preço</Button>
          </ModalFooter>
        </ModalContent>
      </Modal>

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
function IconPower({ active }: { active: boolean }) {
  return (
    <svg className="w-3.5 h-3.5" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" style={{ color: active ? "#22c55e" : "#999" }}>
      <path d="M12 2v10" /><path d="M18.4 6.6a9 9 0 1 1-12.77.04" />
    </svg>
  );
}
function IconDollar() {
  return (
    <svg className="w-3.5 h-3.5" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
      <line x1="12" y1="1" x2="12" y2="23" /><path d="M17 5H9.5a3.5 3.5 0 0 0 0 7h5a3.5 3.5 0 0 1 0 7H6" />
    </svg>
  );
}