import { useEffect, useState } from "react";
import {
  Table, Button, Modal, Input, Chip, Select, ListBox, Spinner, TextArea, TextField, Label, AlertDialog,
} from "@heroui/react";
import { ModelComboBox, type ModelComboBoxItem } from "../components/ModelComboBox";
import { api, type Combo, type ModelEntry, type ComboModelMeta, type Provider } from "../api";
import { IconPlus, IconPencil, IconTrash, IconArrow, IconX, IconSparkles, IconStack, IconGauge } from "../icons";

const KIND_COLORS: Record<string, "accent" | "success" | "warning" | "default" | "danger"> = {
  llm: "accent", embedding: "success", image: "warning", tts: "default", stt: "danger",
  rerank: "default", ocr: "default", video: "default",
};

const STRATEGY_COLORS: Record<string, "accent" | "success" | "warning" | "default" | "danger"> = {
  ordered_fallback: "default",
  "round-robin": "warning",
  velocity: "success",
  intelligence: "accent",
  weighted: "danger",
};

interface ComboForm {
  name: string;
  models: string[];
  strategy: string;
  classifier_model: string;
  model_meta: Record<string, ComboModelMeta>;
}

const empty: ComboForm = {
  name: "",
  models: [],
  strategy: "ordered_fallback",
  classifier_model: "",
  model_meta: {},
};

export default function Combos() {
  const [items, setItems] = useState<Combo[]>([]);
  const [loading, setLoading] = useState(true);
  const [open, setOpen] = useState(false);
  const [form, setForm] = useState<ComboForm>(empty);
  const [editId, setEditId] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);
  const [triedSubmit, setTriedSubmit] = useState(false);
  const [allCatalogModels, setAllCatalogModels] = useState<ModelEntry[]>([]);
  const [confirmId, setConfirmId] = useState<string | null>(null);

  const load = () => {
    setLoading(true);
    api.combos.list().then(setItems).catch(() => setItems([])).finally(() => setLoading(false));
  };
  useEffect(load, []);

  useEffect(() => {
    (async () => {
      try {
        const ps = await api.providers.list();
        const results = await Promise.allSettled(ps.map((p) => api.providers.models(p.id)));
        const models: ModelEntry[] = [];
        results.forEach((r) => {
          if (r.status === "fulfilled") r.value.forEach((m) => models.push(m));
        });
        setAllCatalogModels(models);
      } catch {}
    })();
  }, []);

  const openNew = () => {
    setForm({ ...empty, models: [], model_meta: {} });
    setEditId(null);
    setTriedSubmit(false);
    setOpen(true);
  };

  const openEdit = (c: Combo) => {
    setForm({
      name: c.name,
      models: [...c.models],
      strategy: c.strategy,
      classifier_model: c.classifier_model ?? "",
      model_meta: c.model_meta ? { ...c.model_meta } : {},
    });
    setEditId(c.id);
    setTriedSubmit(false);
    setOpen(true);
  };

  const submit = async () => {
    if (form.strategy === "intelligence") {
      const missing = form.models.some((m) => !(form.model_meta[m]?.description ?? "").trim());
      if (missing) {
        setTriedSubmit(true);
        return;
      }
    }
    setSaving(true);
    try {
      const payload = {
        name: form.name,
        models: form.models,
        strategy: form.strategy,
        classifier_model: form.strategy === "intelligence" ? form.classifier_model : "",
        model_meta: form.strategy === "intelligence" ? form.model_meta : {},
      };
      if (editId) await api.combos.update(editId, payload as any);
      else await api.combos.create(payload as any);
      setOpen(false);
      load();
    } finally {
      setSaving(false);
    }
  };

  const remove = async (id: string) => {
    await api.combos.remove(id);
    load();
  };

  const updateMeta = (modelId: string, patch: Partial<ComboModelMeta>) => {
    setForm((prev) => ({
      ...prev,
      model_meta: {
        ...prev.model_meta,
        [modelId]: { ...(prev.model_meta[modelId] ?? { weight: 5, description: "" }), ...patch },
      },
    }));
  };

  return (
    <div className="space-y-5">
      <div className="flex justify-between items-center">
        <div>
          <h1 className="text-2xl font-bold tracking-tight">Combos</h1>
          <p className="text-sm text-muted mt-0.5">{items.length} combos cadastrados</p>
        </div>
        <Button variant="outline" onPress={openNew}><IconPlus className="w-4 h-4" /> Novo combo</Button>
      </div>

      {loading ? (
        <div className="p-10 text-center text-muted text-sm">Carregando...</div>
      ) : items.length === 0 ? (
        <div className="p-10 text-center text-muted text-sm">
          Nenhum combo ainda. Clique em <strong>Novo combo</strong>.
        </div>
      ) : (
        <Table>
          <Table.ScrollContainer>
            <Table.Content aria-label="combos" className="min-w-[560px]">
              <Table.Header>
                <Table.Column isRowHeader id="name">Nome</Table.Column>
                <Table.Column id="models">Modelos</Table.Column>
                <Table.Column id="strategy">Estratégia</Table.Column>
                <Table.Column id="actions">Ações</Table.Column>
              </Table.Header>
              <Table.Body items={items}>
                {(c) => (
                  <Table.Row key={c.id} id={c.id}>
                    <Table.Cell><span className="font-semibold">{c.name}</span></Table.Cell>
                    <Table.Cell>
                      <div className="flex flex-wrap gap-1">
                        {c.models.map((m, i) => (
                          <Chip key={m + i} size="sm" variant="soft">
                            <span className="text-muted mr-0.5">{i + 1}.</span>
                            {m}
                          </Chip>
                        ))}
                      </div>
                    </Table.Cell>
                    <Table.Cell>
                      <div className="flex flex-col gap-0.5 items-start">
                        <Chip size="sm" variant="soft" color={STRATEGY_COLORS[c.strategy] ?? "default"}>
                          {c.strategy}
                        </Chip>
                        {c.strategy === "intelligence" && c.classifier_model && (
                          <span className="text-[11px] text-muted font-mono">
                            classificador: {c.classifier_model}
                          </span>
                        )}
                      </div>
                    </Table.Cell>
                    <Table.Cell>
                      <div className="flex gap-1 justify-end">
                        <Button isIconOnly size="sm" variant="ghost" onPress={() => openEdit(c)} aria-label="editar">
                          <IconPencil className="w-4 h-4" />
                        </Button>
                        <Button isIconOnly size="sm" variant="ghost" className="text-danger" onPress={() => setConfirmId(c.id)} aria-label="excluir">
                          <IconTrash className="w-4 h-4" />
                        </Button>
                      </div>
                    </Table.Cell>
                  </Table.Row>
                )}
              </Table.Body>
            </Table.Content>
          </Table.ScrollContainer>
        </Table>
      )}

      <Modal isOpen={open} onOpenChange={setOpen}>
        <Modal.Backdrop>
          <Modal.Container>
            <Modal.Dialog className="max-w-2xl max-h-[85vh]">
              <Modal.Header><Modal.Heading>{editId ? "Editar combo" : "Novo combo"}</Modal.Heading></Modal.Header>
              <Modal.Body className="flex flex-col gap-4 overflow-y-auto">
                <TextField value={form.name} onChange={(v) => setForm({ ...form, name: v })}>
                  <Label>Nome</Label>
                  <Input placeholder="ex: smart, fast, balanced" />
                </TextField>

                <ModelSelector
                  selected={form.models}
                  excludeName={editId ? form.name : undefined}
                  onChange={(models) => {
                    const defaultClassifier = form.classifier_model || models[0] || allCatalogModels[0]?.id || "";
                    setForm({
                      ...form,
                      models,
                      classifier_model: form.strategy === "intelligence" && !form.classifier_model ? defaultClassifier : form.classifier_model
                    });
                  }}
                />

                <div className="flex flex-col gap-1">
                  <Label>Estratégia</Label>
                  <Select
                    aria-label="Estratégia"
                    selectedKey={form.strategy}
                    onSelectionChange={(keys) => {
                      const v = (keys as string) ?? "";
                      if (v) {
                        const defaultClassifier = form.classifier_model || form.models[0] || allCatalogModels[0]?.id || "";
                        setForm({
                          ...form,
                          strategy: v,
                          classifier_model: v === "intelligence" ? defaultClassifier : form.classifier_model
                        });
                      }
                    }}
                  >
                    <Select.Trigger><Select.Value /></Select.Trigger>
                    <Select.Popover>
                      <ListBox>
                        <ListBox.Item id="ordered_fallback" textValue="ordered_fallback">
                          <span className="font-medium">ordered_fallback</span>
                          <span className="text-xs text-muted">Fallback em ordem</span>
                        </ListBox.Item>
                        <ListBox.Item id="round-robin" textValue="round-robin">
                          <span className="font-medium">round-robin</span>
                          <span className="text-xs text-muted">Alternância simples</span>
                        </ListBox.Item>
                        <ListBox.Item id="velocity" textValue="velocity">
                          <span className="font-medium">velocity</span>
                          <span className="text-xs text-muted">Maior velocidade / TPS</span>
                        </ListBox.Item>
                        <ListBox.Item id="intelligence" textValue="intelligence">
                          <span className="font-medium">intelligence</span>
                          <span className="text-xs text-muted">Classificação por IA</span>
                        </ListBox.Item>
                        <ListBox.Item id="weighted" textValue="weighted">
                          <span className="font-medium">weighted</span>
                          <span className="text-xs text-muted">Sorteio ponderado por peso</span>
                        </ListBox.Item>
                      </ListBox>
                    </Select.Popover>
                  </Select>
                  <p className="text-xs text-muted">Forma como o Gorouter seleciona entre os modelos declarados.</p>
                </div>

                {form.strategy === "intelligence" && (
                  <div className="space-y-4 p-3.5 bg-surface-secondary/50 rounded-xl border border-border">
                    <div className="text-xs font-semibold text-accent uppercase tracking-wide flex items-center gap-1.5">
                      <IconSparkles className="w-4 h-4" /> Configurações da Estratégia Intelligence
                    </div>

                    <div className="flex flex-col gap-1">
                      <Label>Modelo Classificador</Label>
                      <Select
                        aria-label="Modelo Classificador"
                        selectedKey={form.classifier_model || null}
                        onSelectionChange={(key) => setForm({ ...form, classifier_model: (key as string) ?? "" })}
                      >
                        <Select.Trigger>
                          <Select.Value>{form.classifier_model || "Selecione o modelo classificador..."}</Select.Value>
                          <Select.Indicator />
                        </Select.Trigger>
                        <Select.Popover>
                          <ListBox>
                            {allCatalogModels.map((m) => (
                              <ListBox.Item key={m.id} id={m.id} textValue={m.id}>
                                <div className="flex justify-between items-center w-full gap-2">
                                  <span className="font-mono text-xs">{m.id}</span>
                                  <Chip size="sm" variant="soft" color={KIND_COLORS[m.kind] ?? "default"} className="text-[10px]">
                                    {m.kind}
                                  </Chip>
                                </div>
                              </ListBox.Item>
                            ))}
                          </ListBox>
                        </Select.Popover>
                      </Select>
                    </div>

                    {form.models.length > 0 && (
                      <div className="space-y-3">
                        <Label className="text-xs font-medium text-foreground/80 uppercase tracking-wide flex items-center gap-1">
                          Capacidade e Descrição dos Modelos <span className="text-danger">*</span>
                        </Label>
                        <p className="text-[11px] text-muted">
                          Nível de capacidade (1-10) e descrição. O classificador usa isso para escolher o modelo mais simples que resolve a tarefa.
                        </p>
                        {form.models.map((m) => {
                          const meta = form.model_meta[m] ?? { weight: 5, description: "" };
                          const isEmpty = !(meta.description ?? "").trim();
                          const showError = triedSubmit && isEmpty;
                          return (
                            <div key={m} className={`bg-surface p-3 rounded-lg border space-y-2 ${showError ? "border-danger/40" : "border-border"}`}>
                              <div className="flex justify-between items-center gap-2">
                                <code className="text-xs font-mono font-semibold">{m}</code>
                                <div className="flex items-center gap-2">
                                  <span className="text-xs text-muted font-medium">Capacidade:</span>
                                  <Input
                                    type="number"
                                    className="w-20"
                                    min={1}
                                    max={10}
                                    aria-label="Capacidade"
                                    value={String(meta.weight ?? 5)}
                                    onChange={(e) => updateMeta(m, { weight: Math.max(1, Math.min(10, parseInt(e.target.value) || 1)) })}
                                  />
                                </div>
                              </div>
                              <TextArea
                                placeholder="Descreva para o que este modelo é bom (ex: resolver erros de código complexos, matemática, ou respostas simples e rápidas)..."
                                rows={2}
                                value={meta.description ?? ""}
                                onChange={(e) => updateMeta(m, { description: e.target.value })}
                                className="text-sm"
                              />
                              {showError && (
                                <p className="text-[11px] text-danger">Insira uma descrição para que o classificador saiba quando escolher este modelo</p>
                              )}
                            </div>
                          );
                        })}
                      </div>
                    )}
                  </div>
                )}
                {form.strategy === "weighted" && form.models.length > 0 && (
                  <div className="space-y-3 p-3.5 bg-surface-secondary/50 rounded-xl border border-border">
                    <div className="text-xs font-semibold text-danger uppercase tracking-wide flex items-center gap-1.5">
                      <IconGauge className="w-4 h-4" /> Pesos de roteamento
                    </div>
                    <p className="text-[11px] text-muted">
                      O modelo é sorteado como principal proporcionalmente ao peso. A soma de todos os pesos é tratada como 100%. Ex: 50/30/20 ou 1/1/1. O restante permanece como fallback na ordem configurada.
                    </p>
                    {form.models.map((m) => {
                      const meta = form.model_meta[m] ?? { weight: 1, description: "" };
                      return (
                        <div key={m} className="flex items-center gap-2 bg-surface p-3 rounded-lg border border-border">
                          <code className="text-xs font-mono font-semibold flex-1 truncate">{m}</code>
                          <span className="text-xs text-muted font-medium shrink-0">Peso:</span>
                          <Input
                            type="number"
                            className="w-24"
                            min={1}
                            aria-label={`Peso de ${m}`}
                            value={String(meta.weight ?? 1)}
                            onChange={(e) => updateMeta(m, { weight: Math.max(1, Math.min(100, parseInt(e.target.value) || 1)) })}
                          />
                        </div>
                      );
                    })}
                  </div>
                )}
              </Modal.Body>
              <Modal.Footer>
                <Button variant="secondary" onPress={() => setOpen(false)}>Cancelar</Button>
                <Button variant="primary" onPress={submit} isDisabled={saving}>Salvar</Button>
              </Modal.Footer>
            </Modal.Dialog>
          </Modal.Container>
        </Modal.Backdrop>
      </Modal>

      <AlertDialog>
        <AlertDialog.Backdrop isOpen={!!confirmId} onOpenChange={(o) => !o && setConfirmId(null)}>
          <AlertDialog.Container>
            <AlertDialog.Dialog className="sm:max-w-[400px]">
              <AlertDialog.CloseTrigger />
              <AlertDialog.Header>
                <AlertDialog.Icon status="danger" />
                <AlertDialog.Heading>Remover este combo?</AlertDialog.Heading>
              </AlertDialog.Header>
              <AlertDialog.Body>
                <p>Esta ação não pode ser desfeita.</p>
              </AlertDialog.Body>
              <AlertDialog.Footer>
                <Button slot="close" variant="tertiary">Cancelar</Button>
                <Button slot="close" variant="danger" onPress={() => { if (confirmId) remove(confirmId); setConfirmId(null); }}>Remover</Button>
              </AlertDialog.Footer>
            </AlertDialog.Dialog>
          </AlertDialog.Container>
        </AlertDialog.Backdrop>
      </AlertDialog>
    </div>
  );
}

function ModelSelector({
  selected,
  onChange,
  excludeName,
}: {
  selected: string[];
  onChange: (m: string[]) => void;
  excludeName?: string;
}) {
  const [allModels, setAllModels] = useState<ModelEntry[]>([]);
  const [allCombos, setAllCombos] = useState<Combo[]>([]);
  const [providers, setProviders] = useState<Provider[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [searchValue, setSearchValue] = useState("");

  useEffect(() => {
    let cancelled = false;
    (async () => {
      setLoading(true);
      try {
        const ps = await api.providers.list();
        const [providerModels, combosList] = await Promise.all([
          Promise.allSettled(ps.map((p) => api.providers.models(p.id))),
          api.combos.list().catch(() => []),
        ]);
        if (cancelled) return;
        const models: ModelEntry[] = [];
        providerModels.forEach((r) => {
          if (r.status === "fulfilled") r.value.forEach((m) => models.push(m));
        });
        setAllModels(models);
        setAllCombos(combosList);
        setProviders(ps);
      } catch (e: any) {
        if (!cancelled) setError(e?.message ?? "erro");
      } finally {
        if (!cancelled) setLoading(false);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, []);

  const fixedKind = (() => {
    if (selected.length === 0) return undefined;
    const first = selected[0];
    const modelEntry = allModels.find((m) => m.id === first);
    if (modelEntry) return modelEntry.kind;
    const comboEntry = allCombos.find((c) => c.name === first);
    return comboEntry?.kind ?? "llm";
  })();

  type Option =
    | { kind: "model"; id: string; entry: ModelEntry }
    | { kind: "combo"; id: string; entry: Combo };

  const available: Option[] = [];
  for (const m of allModels) {
    if (selected.includes(m.id)) continue;
    if (fixedKind && m.kind !== fixedKind) continue;
    available.push({ kind: "model", id: m.id, entry: m });
  }
  for (const c of allCombos) {
    if (!c.name) continue;
    if (c.name === excludeName) continue;
    if (selected.includes(c.name)) continue;
    const ckind = c.kind || "llm";
    if (fixedKind && ckind !== fixedKind) continue;
    available.push({ kind: "combo", id: c.name, entry: c });
  }

  const listItems: ModelComboBoxItem[] = [...available]
    .sort((a, b) => Number(b.kind === "combo") - Number(a.kind === "combo"))
    .map((opt) => ({
      id: opt.id,
      itemType: opt.kind,
      kind: opt.kind === "model" ? opt.entry.kind : opt.entry.kind || "llm",
      isActive: opt.kind === "model" ? opt.entry.is_active : true,
    }));

  const toggleModel = (id: string) => {
    if (selected.includes(id)) {
      onChange(selected.filter((m) => m !== id));
    } else {
      onChange([...selected, id]);
    }
  };

  const move = (index: number, dir: -1 | 1) => {
    const newIndex = index + dir;
    if (newIndex < 0 || newIndex >= selected.length) return;
    const next = [...selected];
    [next[index], next[newIndex]] = [next[newIndex], next[index]];
    onChange(next);
  };

  const removeAt = (index: number) => {
    onChange(selected.filter((_, i) => i !== index));
  };

  return (
    <div className="space-y-3">
      <div>
        <Label className="text-sm text-muted">Modelos</Label>
        <p className="text-xs text-muted mt-0.5 mb-2">
          Selecione modelos ou outros combos como membros.
          {fixedKind && (
            <>
              {" "}
              Tipo fixado:{" "}
              <Chip size="sm" variant="soft" color={KIND_COLORS[fixedKind] ?? "default"}>
                {fixedKind}
              </Chip>
            </>
          )}
        </p>
        {loading ? (
          <div className="flex items-center gap-2 py-2 text-sm text-muted">
            <Spinner size="sm" /> Carregando models e combos...
          </div>
        ) : error && allModels.length === 0 && allCombos.length === 0 ? (
          <div className="text-sm text-danger py-2">Erro: {error}</div>
        ) : (
          <div className="space-y-2">
              <ModelComboBox
                ariaLabel="Modelos"
                selectionMode="multiple"
                selectedKeys={selected}
                onSelectedKeysChange={onChange}
                inputValue={searchValue}
                onInputChange={setSearchValue}
                items={listItems}
                inputPlaceholder="Buscar model ou combo..."
                inputVariant="secondary"
                valuePlaceholder="Nenhum modelo selecionado"
                isDisabled={loading}
                className="w-full"
              />
            {available.length === 0 && !loading && (
              <div className="text-sm text-muted px-1 py-1">
                {fixedKind ? `Nenhuma opção do tipo ${fixedKind}.` : "Nenhuma opção disponível."}
              </div>
            )}
            {searchValue.trim() && !available.some((opt) => opt.id === searchValue.trim()) && (
              <Button
                size="sm"
                variant="outline"
                className="w-full font-mono text-xs justify-start"
                onPress={() => {
                  toggleModel(searchValue.trim());
                  setSearchValue("");
                }}
              >
                + Adicionar personalizado: "{searchValue.trim()}"
              </Button>
            )}
          </div>
        )}
      </div>

      {selected.length > 0 && (
        <div className="space-y-1.5">
          <p className="text-xs text-muted uppercase tracking-wide font-medium">Membros do Combo</p>
          {selected.map((id, i) => {
            const isCombo = allCombos.some((c) => c.name === id);
            const modelEntry = allModels.find((m) => m.id === id);
            const comboEntry = allCombos.find((c) => c.name === id);
            const kind = modelEntry?.kind ?? comboEntry?.kind ?? "llm";
            return (
              <div key={id + i} className="flex items-center gap-2 bg-surface-secondary rounded-lg px-3 py-2">
                <span className="text-xs text-muted w-5 tabular-nums">{i + 1}.</span>
                {isCombo && <IconStack className="w-3 h-3 shrink-0 text-muted" />}
                <code className="text-xs flex-1 truncate">{id}</code>
                {isCombo && (
                  <Chip size="sm" variant="soft" color="default" className="text-[10px]">combo</Chip>
                )}
                <Chip size="sm" variant="soft" color={KIND_COLORS[kind] ?? "default"} className="text-[10px]">
                  {kind}
                </Chip>
                <div className="flex gap-0.5">
                  <Button isIconOnly size="sm" variant="ghost" isDisabled={i === 0} onPress={() => move(i, -1)} aria-label="subir">
                    <IconArrow dir="up" className="w-3.5 h-3.5" />
                  </Button>
                  <Button isIconOnly size="sm" variant="ghost" isDisabled={i === selected.length - 1} onPress={() => move(i, 1)} aria-label="descer">
                    <IconArrow dir="down" className="w-3.5 h-3.5" />
                  </Button>
                  <Button isIconOnly size="sm" variant="ghost" className="text-danger" onPress={() => removeAt(i)} aria-label="remover">
                    <IconX className="w-3.5 h-3.5" />
                  </Button>
                </div>
              </div>
            );
          })}
        </div>
      )}
    </div>
  );
}
