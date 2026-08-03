import { useEffect, useMemo, useState, useCallback } from "react";
import {
  Input, Spinner, Chip, Button, Card, Modal, Select, ListBox, TextField, Label,
} from "@heroui/react";
import { api, type ModelEntry, type Provider, type ModelStat, type ModelPricing } from "../api";
import { formatCompact } from "../format";
import { IconSearch, IconTrash, IconPower, IconDollar } from "../icons";

const KINDS = ["llm", "embedding", "image", "tts", "stt", "rerank", "ocr", "video"];

const kindColor = (k: string): "accent" | "success" | "warning" | "danger" | "default" => {
  switch (k) {
    case "embedding": return "success";
    case "image": return "warning";
    case "stt": return "danger";
    case "llm":
    case "tts":
    default: return "default";
  }
};

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

function useCopyToClipboard() {
  const [copiedId, setCopiedId] = useState<string | null>(null);
  const copy = useCallback((text: string, id: string) => {
    navigator.clipboard.writeText(text).then(() => {
      setCopiedId(id);
      setTimeout(() => setCopiedId(null), 1500);
    }).catch(() => {});
  }, []);
  return { copiedId, copy };
}

export default function Models() {
  const [items, setItems] = useState<ModelEntry[]>([]);
  const [stats, setStats] = useState<Record<string, ModelStat>>({});
  const [providers, setProviders] = useState<Provider[]>([]);
  const [loading, setLoading] = useState(true);
  const [query, setQuery] = useState("");
  const [syncing, setSyncing] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [addOpen, setAddOpen] = useState(false);
  const [addProviderId, setAddProviderId] = useState<string>("");
  const [addForm, setAddForm] = useState({ model_id: "", name: "", kind: "llm", context: 0 });
  const [pricingOpen, setPricingOpen] = useState(false);
  const [pricingModel, setPricingModel] = useState<ModelEntry | null>(null);
  const [pricingForm, setPricingForm] = useState({ inputPer1M: "", outputPer1M: "", perImage: "" });
  const { copiedId, copy } = useCopyToClipboard();

  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    api.providers.list().then((ps) => {
      if (cancelled) return;
      setProviders(ps);
      return Promise.all(ps.map((p) => api.providers.models(p.id).catch(() => [] as ModelEntry[])))
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
    const p = providers.find((x) => x.id === providerId);
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
    try {
      await api.models.remove(m.id);
      setItems((prev) => prev.filter((x) => x.id !== m.id));
    } catch (e: any) { setError(e?.message); }
  };

  const openAdd = (providerId: string) => {
    const p = providers.find((x) => x.id === providerId);
    if (!p) return;
    setAddProviderId(p.id);
    setAddForm({ model_id: "", name: "", kind: "llm", context: 0 });
    setAddOpen(true);
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
      setAddOpen(false);
    } catch (e: any) { setError(e?.message); }
  };

  const openPricing = (m: ModelEntry) => {
    setPricingModel(m);
    const p = (m.pricing || {}) as ModelPricing;
    setPricingForm({
      inputPer1M: p.input_cost_per_token ? String((p.input_cost_per_token * 1_000_000).toFixed(2)) : "",
      outputPer1M: p.output_cost_per_token ? String((p.output_cost_per_token * 1_000_000).toFixed(2)) : "",
      perImage: p.output_cost_per_image ? String(p.output_cost_per_image) : "",
    });
    setPricingOpen(true);
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
      setPricingOpen(false);
    } catch (e: any) { setError(e?.message); }
  };

  const statKey = (m: ModelEntry) => {
    const parts = m.id.split("/");
    return parts.length > 1 ? parts[1] : m.id;
  };

  if (loading) {
    return <div className="flex justify-center py-20"><Spinner /></div>;
  }

  return (
    <div className="space-y-5">
      <div className="flex justify-between items-end gap-4 flex-wrap">
        <div>
          <h1 className="text-2xl font-bold tracking-tight">Models</h1>
          <p className="text-sm text-muted mt-0.5">{items.length} modelos · {items.filter(m => m.is_active).length} ativos</p>
        </div>
        <div className="relative max-w-xs w-full">
          <span className="absolute left-3 top-1/2 -translate-y-1/2 pointer-events-none"><IconSearch className="w-4 h-4 text-muted" /></span>
          <Input
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder="Buscar modelo, provider, tipo..."
            className="pl-9"
            variant="secondary"
            aria-label="buscar modelos"
          />
        </div>
      </div>

      {error && (
        <div className="bg-danger-soft border border-danger/30 text-danger rounded-xl p-4 text-sm">{error}</div>
      )}

      {groups.length === 0 && (
        <div className="text-center py-20 text-muted text-sm">
          Nenhum modelo {query ? "corresponde à busca" : "disponível ainda"}. {!query && "Crie um provider e sincronize."}
        </div>
      )}

      <div className="space-y-6">
        {groups.map((g) => (
          <div key={g.providerId}>
            <div className="flex items-center gap-2 mb-3">
              <Chip size="sm" variant="soft" color="default" className="font-mono">{g.providerId}</Chip>
              <span className="text-xs text-muted">{g.models.length} modelo{g.models.length === 1 ? "" : "s"}</span>
              <div className="flex gap-1 ml-auto">
                <Button size="sm" variant="secondary" onPress={() => sync(g.providerId)} isDisabled={syncing === providers.find(p => p.id === g.providerId)?.id}>
                  Sincronizar
                </Button>
                <Button size="sm" variant="outline" onPress={() => openAdd(g.providerId)}>+ Model</Button>
              </div>
            </div>
            <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4 gap-2">
              {g.models.map((m) => {
                const st = stats[statKey(m)] || stats[m.id];
                return (
                  <Card
                    key={m.id}
                    className="group relative p-3 hover:border-border transition-colors"
                  >
                    <div className="flex items-start justify-between gap-2">
                      <code
                        className="text-sm font-mono truncate flex-1 cursor-pointer hover:text-accent transition-colors"
                        title={`${m.id} — clique para copiar`}
                        onClick={() => copy(m.id, m.id)}
                      >
                        {copiedId === m.id ? "copiado!" : m.id}
                      </code>
                      <span
                        className={`w-2 h-2 rounded-full shrink-0 mt-1 ${m.is_active ? "bg-success" : "bg-default-soft"}`}
                        title={m.is_active ? "ativo" : "inativo"}
                      />
                    </div>
                    <div className="flex items-center gap-1.5 mt-2">
                      <Chip size="sm" variant="soft" color={kindColor(m.kind)} className="h-5 text-[10px]">{m.kind}</Chip>
                      <span className="text-[10px] text-muted">{m.source}</span>
                    </div>
                    {st && st.requests > 0 && (
                      <div className="flex items-center gap-3 mt-2 text-[10px] text-muted">
                        <span className="tabular-nums">{st.avg_tps > 0 ? `${st.avg_tps.toFixed(1)} tok/s` : "—"}</span>
                        <span className="tabular-nums">{st.avg_ttft_ms && st.avg_ttft_ms > 0 ? `ttft ${Math.round(st.avg_ttft_ms)}ms` : ""}{st.avg_ttft_ms && st.avg_ttft_ms > 0 ? " · " : ""}{st.avg_latency_ms > 0 ? `${Math.round(st.avg_latency_ms)}ms` : "—"}</span>
                        <span className="tabular-nums">{st.requests > 999 ? formatCompact(st.requests) : `${st.requests}x`}</span>
                      </div>
                    )}
                    {(() => {
                      const p = m.pricing;
                      if (!p || (!p.source && !p.input_cost_per_token && !p.output_cost_per_token && !p.output_cost_per_image)) return null;
                      const inPrice = formatPricePer1M(p.input_cost_per_token);
                      const outPrice = formatPricePer1M(p.output_cost_per_token);
                      const imgPrice = formatPricePerImage(p.output_cost_per_image);
                      if (!inPrice && !outPrice && !imgPrice) {
                        return (
                          <div className="flex items-center gap-1 mt-1.5 text-[10px]">
                            <span className="tabular-nums text-muted">Free</span>
                          </div>
                        );
                      }
                      return (
                        <div className="flex items-center gap-2 mt-1.5 text-[10px]">
                          {inPrice && <span className="tabular-nums text-success">{inPrice}</span>}
                          {outPrice && <span className="tabular-nums text-accent">{outPrice}</span>}
                          {imgPrice && <span className="tabular-nums text-warning">{imgPrice}</span>}
                        </div>
                      );
                    })()}
                    <div className="absolute top-1.5 right-7 opacity-0 group-hover:opacity-100 transition-opacity flex gap-0.5">
                      <Button isIconOnly size="sm" variant="tertiary" onPress={() => openPricing(m)} aria-label="Editar preço">
                        <IconDollar className="w-3.5 h-3.5" />
                      </Button>
                      <Button isIconOnly size="sm" variant="tertiary" onPress={() => toggleActive(m)} aria-label={m.is_active ? "Desativar" : "Ativar"}>
                        <IconPower className={`w-3.5 h-3.5 ${m.is_active ? "text-success" : "text-muted"}`} />
                      </Button>
                      <Button isIconOnly size="sm" variant="tertiary" onPress={() => removeModel(m)} aria-label="Excluir" className="text-danger hover:bg-danger-soft">
                        <IconTrash className="w-3.5 h-3.5" />
                      </Button>
                    </div>
                  </Card>
                );
              })}
            </div>
          </div>
        ))}
      </div>

      <Modal isOpen={pricingOpen} onOpenChange={setPricingOpen}>
        <Modal.Backdrop>
          <Modal.Container>
            <Modal.Dialog>
              <Modal.Header><Modal.Heading>Editar preço — {pricingModel?.id}</Modal.Heading></Modal.Header>
              <Modal.Body className="gap-4">
                <TextField value={pricingForm.inputPer1M} onChange={(v) => setPricingForm({ ...pricingForm, inputPer1M: v })}>
                  <Label>Input ($ / 1M tokens)</Label>
                  <Input type="number" placeholder="ex: 2.50" step="0.01" />
                </TextField>
                <TextField value={pricingForm.outputPer1M} onChange={(v) => setPricingForm({ ...pricingForm, outputPer1M: v })}>
                  <Label>Output ($ / 1M tokens)</Label>
                  <Input type="number" placeholder="ex: 10.00" step="0.01" />
                </TextField>
                <TextField value={pricingForm.perImage} onChange={(v) => setPricingForm({ ...pricingForm, perImage: v })}>
                  <Label>Por imagem ($ — image gen only)</Label>
                  <Input type="number" placeholder="ex: 0.04" step="0.01" />
                </TextField>
                <p className="text-xs text-muted">
                  Preços em USD por 1 milhão de tokens. Deixe em branco para zerar.
                </p>
              </Modal.Body>
              <Modal.Footer>
                <Button variant="primary" onPress={submitPricing}>Salvar preço</Button>
              </Modal.Footer>
            </Modal.Dialog>
          </Modal.Container>
        </Modal.Backdrop>
      </Modal>

      <Modal isOpen={addOpen} onOpenChange={setAddOpen}>
        <Modal.Backdrop>
          <Modal.Container>
            <Modal.Dialog>
              <Modal.Header><Modal.Heading>Adicionar modelo</Modal.Heading></Modal.Header>
              <Modal.Body className="gap-4">
                <TextField value={addForm.model_id} onChange={(v) => setAddForm({ ...addForm, model_id: v })}>
                  <Label>Model ID</Label>
                  <Input placeholder="ex: gpt-4o, whisper-1" />
                </TextField>
                <TextField value={addForm.name} onChange={(v) => setAddForm({ ...addForm, name: v })}>
                  <Label>Nome (opcional)</Label>
                  <Input placeholder="nome display" />
                </TextField>
                <div className="flex flex-col gap-1">
                  <Label>Tipo</Label>
                  <Select aria-label="Tipo" selectedKey={addForm.kind} onSelectionChange={(k) => setAddForm({ ...addForm, kind: (k as string) ?? "llm" })}>
                    <Select.Trigger><Select.Value /></Select.Trigger>
                    <Select.Popover>
                      <ListBox>{KINDS.map((k) => <ListBox.Item key={k} id={k}>{k}</ListBox.Item>)}</ListBox>
                    </Select.Popover>
                  </Select>
                </div>
                <TextField value={String(addForm.context)} onChange={(v) => setAddForm({ ...addForm, context: parseInt(v) || 0 })}>
                  <Label>Context (opcional)</Label>
                  <Input type="number" />
                </TextField>
              </Modal.Body>
              <Modal.Footer>
                <Button variant="primary" onPress={submitAdd} isDisabled={!addForm.model_id}>Adicionar</Button>
              </Modal.Footer>
            </Modal.Dialog>
          </Modal.Container>
        </Modal.Backdrop>
      </Modal>
    </div>
  );
}
