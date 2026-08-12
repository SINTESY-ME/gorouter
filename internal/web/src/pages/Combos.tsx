import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import {
  Table, Button, Modal, Input, Chip, Select, ListBox, Spinner, TextArea, TextField, Label, AlertDialog, cn,
} from "@heroui/react";
import {
  DndContext, closestCenter, KeyboardSensor, PointerSensor, useSensor, useSensors,
  type DragEndEvent,
} from "@dnd-kit/core";
import { restrictToVerticalAxis } from "@dnd-kit/modifiers";
import {
  SortableContext, sortableKeyboardCoordinates, useSortable, verticalListSortingStrategy,
} from "@dnd-kit/sortable";
import { CSS } from "@dnd-kit/utilities";
import { ModelComboBox, type ModelComboBoxItem } from "../components/ModelComboBox";
import { api, type Combo, type ModelEntry, type ComboModelMeta, type Provider, type MCPClient } from "../api";
import { IconPlus, IconPencil, IconTrash, IconArrow, IconX, IconStack, IconGrip } from "../icons";

const KIND_COLORS: Record<string, "accent" | "success" | "warning" | "default" | "danger"> = {
  llm: "accent", embedding: "success", image: "warning", tts: "default", stt: "danger",
  rerank: "default", ocr: "default", video: "default",
};

const STRATEGY_COLORS: Record<string, "accent" | "success" | "warning" | "default" | "danger"> = {
  ordered_fallback: "default",
  "round-robin": "default",
  velocity: "default",
  intelligence: "default",
  weighted: "default",
};

interface ComboForm {
  name: string;
  models: string[];
  strategy: string;
  classifier_model: string;
  model_meta: Record<string, ComboModelMeta>;
  mcp_clients: string[];
}

const empty: ComboForm = {
  name: "",
  models: [],
  strategy: "ordered_fallback",
  classifier_model: "",
  model_meta: {},
  mcp_clients: [],
};

export default function Combos() {
  const { t } = useTranslation();
  const [items, setItems] = useState<Combo[]>([]);
  const [loading, setLoading] = useState(true);
  const [open, setOpen] = useState(false);
  const [form, setForm] = useState<ComboForm>(empty);
  const [editId, setEditId] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);
  const [triedSubmit, setTriedSubmit] = useState(false);
  const [allCatalogModels, setAllCatalogModels] = useState<ModelEntry[]>([]);
  const [mcpClients, setMcpClients] = useState<MCPClient[]>([]);
  const [confirmId, setConfirmId] = useState<string | null>(null);

  const load = () => {
    setLoading(true);
    api.combos.list().then(setItems).catch(() => setItems([])).finally(() => setLoading(false));
  };
  useEffect(load, []);

  useEffect(() => {
    api.mcpClients.list().then(setMcpClients).catch(() => setMcpClients([]));
  }, []);

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
    setForm({ ...empty, models: [], model_meta: {}, mcp_clients: [] });
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
      mcp_clients: c.mcp_clients ? [...c.mcp_clients] : [],
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
        mcp_clients: form.mcp_clients,
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

  const strategyOptions = [
    { id: "ordered_fallback", label: t("combos.stratOrdered") },
    { id: "round-robin", label: t("combos.stratRoundRobin") },
    { id: "velocity", label: t("combos.stratVelocity") },
    { id: "intelligence", label: t("combos.stratIntelligence") },
    { id: "weighted", label: t("combos.stratWeighted") },
  ];

  return (
    <div className="space-y-5">
      <div className="flex justify-between items-center">
        <div>
          <h1 className="text-2xl font-bold tracking-tight">{t("combos.title")}</h1>
          <p className="text-sm text-muted mt-0.5">{t("combos.subtitle", { count: items.length })}</p>
        </div>
        <Button variant="outline" onPress={openNew}><IconPlus className="w-4 h-4" /> {t("combos.new")}</Button>
      </div>

      {loading ? (
        <div className="p-10 text-center text-muted text-sm">{t("combos.loading")}</div>
      ) : items.length === 0 ? (
        <div className="p-10 text-center text-muted text-sm">
          {t("combos.empty")} <strong>{t("combos.new")}</strong>.
        </div>
      ) : (
        <Table>
          <Table.ScrollContainer>
            <Table.Content aria-label={t("combos.tableAria")} className="min-w-[560px]">
              <Table.Header>
                <Table.Column isRowHeader id="name">{t("combos.colName")}</Table.Column>
                <Table.Column id="models">{t("combos.colModels")}</Table.Column>
                <Table.Column id="strategy">{t("combos.colStrategy")}</Table.Column>
                <Table.Column id="actions">{t("combos.colActions")}</Table.Column>
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
                            {t("combos.classifier", { model: c.classifier_model })}
                          </span>
                        )}
                        {c.mcp_clients && c.mcp_clients.length > 0 && (
                          <div className="flex flex-wrap gap-1 pt-0.5">
                            {c.mcp_clients.map((id) => {
                              const name = mcpClients.find((x) => x.id === id)?.name ?? id;
                              return (
                                <Chip key={id} size="sm" variant="flat" color="accent" className="text-[10px]">
                                  {name}
                                </Chip>
                              );
                            })}
                          </div>
                        )}
                      </div>
                    </Table.Cell>
                    <Table.Cell>
                      <div className="flex gap-1 justify-end">
                        <Button isIconOnly size="sm" variant="ghost" onPress={() => openEdit(c)} aria-label={t("combos.editAria")}>
                          <IconPencil className="w-4 h-4" />
                        </Button>
                        <Button isIconOnly size="sm" variant="ghost" className="text-danger" onPress={() => setConfirmId(c.id)} aria-label={t("combos.deleteAria")}>
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
              <Modal.Header><Modal.Heading>{editId ? t("combos.editModal") : t("combos.createModal")}</Modal.Heading></Modal.Header>
              <Modal.Body className="flex flex-col gap-4 overflow-y-auto">
                <TextField value={form.name} onChange={(v) => setForm({ ...form, name: v })}>
                  <Label>{t("combos.name")}</Label>
                  <Input variant="secondary" placeholder={t("combos.namePlaceholder")} />
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
                  <div className="flex items-baseline justify-between gap-3">
                    <Label>{t("combos.strategy")}</Label>
                    <span className="text-xs text-muted text-right">{t("combos.stratHelper")}</span>
                  </div>
                  <Select
                    aria-label={t("combos.strategyAria")}
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
                    <Select.Trigger className="bg-surface-secondary"><Select.Value /></Select.Trigger>
                    <Select.Popover>
                      <ListBox>
                        {strategyOptions.map((o) => (
                          <ListBox.Item key={o.id} id={o.id} textValue={o.id}>
                            <span className="font-medium">{o.id}</span>
                            <span className="text-xs text-muted">{o.label}</span>
                          </ListBox.Item>
                        ))}
                      </ListBox>
                    </Select.Popover>
                  </Select>
                  <p className="text-xs text-muted">{t(`combos.stratDescription${form.strategy === "ordered_fallback" ? "Ordered" : form.strategy === "round-robin" ? "RoundRobin" : form.strategy === "velocity" ? "Velocity" : form.strategy === "intelligence" ? "Intelligence" : "Weighted"}`)}</p>
                </div>

                {form.strategy === "intelligence" && (
                  <div className="space-y-4 p-3.5 bg-surface-secondary/50 rounded-xl border border-border">

                    <div className="flex flex-col gap-1">
                      <Label>{t("combos.intelClassifier")}</Label>
                      <Select
                        aria-label={t("combos.intelClassifierAria")}
                        selectedKey={form.classifier_model || null}
                        onSelectionChange={(key) => setForm({ ...form, classifier_model: (key as string) ?? "" })}
                      >
<Select.Trigger className="bg-surface-secondary">
                           <Select.Value>{form.classifier_model || t("combos.intelClassifierPlaceholder")}</Select.Value>
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
                          {t("combos.intelCapDesc")} <span className="text-danger">*</span>
                        </Label>
                        <p className="text-[11px] text-muted">
                          {t("combos.intelHelper")}
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
                                  <span className="text-xs text-muted font-medium">{t("combos.intelCap")}</span>
                            <Input
                             variant="secondary"
                             type="number"
                             className="w-20"
                                    min={1}
                                    max={10}
                                    aria-label={t("combos.intelCapAria")}
                                    value={String(meta.weight ?? 5)}
                                    onChange={(e) => updateMeta(m, { weight: Math.max(1, Math.min(10, parseInt(e.target.value) || 1)) })}
                                  />
                                </div>
                              </div>
                              <TextArea
                                 variant="secondary"
                                 placeholder={t("combos.intelDescPlaceholder")}
                                rows={2}
                                value={meta.description ?? ""}
                                onChange={(e) => updateMeta(m, { description: e.target.value })}
                                className="text-sm"
                              />
                              {showError && (
                                <p className="text-[11px] text-danger">{t("combos.intelErr")}</p>
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
                    <p className="text-[11px] text-muted">
                      {t("combos.weightedHelper")}
                    </p>
                    {form.models.map((m) => {
                      const meta = form.model_meta[m] ?? { weight: 1, description: "" };
                      return (
                        <div key={m} className="flex items-center gap-2 bg-surface p-3 rounded-lg border border-border">
                          <code className="text-xs font-mono font-semibold flex-1 truncate">{m}</code>
                          <span className="text-xs text-muted font-medium shrink-0">{t("combos.weightedWeight")}</span>
                           <Input
                             variant="secondary"
                             type="number"
                             className="w-24"
                            min={1}
                            aria-label={t("combos.weightedWeightAria", { model: m })}
                            value={String(meta.weight ?? 1)}
                            onChange={(e) => updateMeta(m, { weight: Math.max(1, Math.min(100, parseInt(e.target.value) || 1)) })}
                          />
                        </div>
                      );
                    })}
                  </div>
                )}
                <div className="flex flex-col gap-2">
                  <div className="flex items-baseline justify-between gap-3">
                    <Label>{t("combos.mcps")}</Label>
                    <span className="text-xs text-muted text-right">{t("combos.mcpsHelper")}</span>
                  </div>
                  <Select
                    aria-label={t("combos.mcpsAria")}
                    selectedKeys={new Set(form.mcp_clients)}
                    onSelectionChange={(keys) => {
                      const selected = Array.from(keys as Set<string>);
                      setForm({ ...form, mcp_clients: selected });
                    }}
                  >
                    <Select.Trigger className="bg-surface-secondary">
                      <Select.Value>
                        {form.mcp_clients.length
                          ? form.mcp_clients.map((id) => mcpClients.find((c) => c.id === id)?.name ?? id).join(", ")
                          : t("combos.mcpsPlaceholder")}
                      </Select.Value>
                      <Select.Indicator />
                    </Select.Trigger>
                    <Select.Popover>
                      <ListBox selectionMode="multiple">
                        {mcpClients.length === 0 && (
                          <ListBox.Item id="__empty__" isDisabled>
                            {t("combos.mcpsEmpty")}
                          </ListBox.Item>
                        )}
                        {mcpClients.map((c) => (
                          <ListBox.Item key={c.id} id={c.id} textValue={c.name}>
                            <div className="flex items-center justify-between w-full gap-2">
                              <span className="text-sm font-medium">{c.name}</span>
                              <Chip size="sm" variant="soft" color={c.enabled ? "success" : "default"} className="text-[10px]">
                                {c.enabled ? t("combos.mcpsEnabled") : t("combos.mcpsDisabled")}
                              </Chip>
                            </div>
                          </ListBox.Item>
                        ))}
                      </ListBox>
                    </Select.Popover>
                  </Select>
                  {form.mcp_clients.length > 0 && (
                    <div className="flex flex-wrap gap-1.5">
                      {form.mcp_clients.map((id) => {
                        const c = mcpClients.find((x) => x.id === id);
                        return (
                          <Chip key={id} size="sm" variant="flat" color="accent"
                            onClose={() => setForm({ ...form, mcp_clients: form.mcp_clients.filter((x) => x !== id) })}>
                            {c?.name ?? id}
                          </Chip>
                        );
                      })}
                    </div>
                  )}
                </div>
              </Modal.Body>
              <Modal.Footer>
                <Button variant="secondary" onPress={() => setOpen(false)}>{t("combos.cancel")}</Button>
                <Button variant="primary" onPress={submit} isDisabled={saving}>{t("combos.save")}</Button>
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
                <AlertDialog.Heading>{t("combos.removeTitle")}</AlertDialog.Heading>
              </AlertDialog.Header>
              <AlertDialog.Body>
                <p>{t("combos.removeBody")}</p>
              </AlertDialog.Body>
              <AlertDialog.Footer>
                <Button slot="close" variant="tertiary">{t("combos.cancel")}</Button>
                <Button slot="close" variant="danger" onPress={() => { if (confirmId) remove(confirmId); setConfirmId(null); }}>{t("combos.remove")}</Button>
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
  const { t } = useTranslation();
  const [allModels, setAllModels] = useState<ModelEntry[]>([]);
  const [allCombos, setAllCombos] = useState<Combo[]>([]);
  const [providers, setProviders] = useState<Provider[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [searchValue, setSearchValue] = useState("");

  const sensors = useSensors(
    useSensor(PointerSensor, { activationConstraint: { distance: 5 } }),
    useSensor(KeyboardSensor, { coordinateGetter: sortableKeyboardCoordinates }),
  );

  const dragModifiers = [restrictToVerticalAxis];

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
        if (!cancelled) setError(e?.message ?? t("combos.selectorErr"));
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

  const handleDragEnd = (event: DragEndEvent) => {
    const { active, over } = event;
    if (!over || active.id === over.id) return;
    const from = selected.indexOf(active.id as string);
    const to = selected.indexOf(over.id as string);
    if (from < 0 || to < 0) return;
    const next = [...selected];
    const [moved] = next.splice(from, 1);
    next.splice(to, 0, moved);
    onChange(next);
  };

  return (
    <div className="space-y-3">
      <div>
        <Label className="text-sm text-muted">{t("combos.selectorLabel")}</Label>
        <p className="text-xs text-muted mt-0.5 mb-2">
          {t("combos.selectorHelper")}
          {fixedKind && (
            <>
              {" "}
              {t("combos.selectorKindFixed")}
              <Chip size="sm" variant="soft" color={KIND_COLORS[fixedKind] ?? "default"}>
                {fixedKind}
              </Chip>
            </>
          )}
        </p>
        {loading ? (
          <div className="flex items-center gap-2 py-2 text-sm text-muted">
            <Spinner size="sm" /> {t("combos.selectorLoading")}
          </div>
        ) : error && allModels.length === 0 && allCombos.length === 0 ? (
          <div className="text-sm text-danger py-2">{t("combos.selectorError")}{error}</div>
        ) : (
          <div className="space-y-2">
              <ModelComboBox
                ariaLabel={t("combos.selectorAria")}
                selectionMode="multiple"
                selectedKeys={selected}
                onSelectedKeysChange={onChange}
                inputValue={searchValue}
                onInputChange={setSearchValue}
                items={listItems}
                inputPlaceholder={t("combos.selectorPlaceholder")}
                 inputVariant="secondary"
                 isDisabled={loading}
                className="w-full"
              />
            {available.length === 0 && !loading && (
              <div className="text-sm text-muted px-1 py-1">
                {fixedKind ? t("combos.selectorNoType", { kind: fixedKind }) : t("combos.selectorNoOptions")}
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
                {t("combos.selectorAddCustom")} "{searchValue.trim()}"
              </Button>
            )}
          </div>
        )}
      </div>

      {selected.length > 0 && (
        <div className="space-y-1.5">
          <p className="text-xs text-muted uppercase tracking-wide font-medium">{t("combos.selectorMembers")}</p>
          <DndContext sensors={sensors} collisionDetection={closestCenter} onDragEnd={handleDragEnd} modifiers={dragModifiers}>
            <SortableContext items={selected} strategy={verticalListSortingStrategy}>
              {selected.map((id, i) => (
                <SortableModelItem
                  key={id}
                  id={id}
                  index={i}
                  isCombo={allCombos.some((c) => c.name === id)}
                  kind={allModels.find((m) => m.id === id)?.kind ?? allCombos.find((c) => c.name === id)?.kind ?? "llm"}
                  canMoveUp={i > 0}
                  canMoveDown={i < selected.length - 1}
                  onMoveUp={() => move(i, -1)}
                  onMoveDown={() => move(i, 1)}
                  onRemove={() => removeAt(i)}
                  isComboLabel={t("combos.selectorCombo")}
                  upAria={t("combos.selectorUp")}
                  downAria={t("combos.selectorDown")}
                  removeAria={t("combos.selectorRemove")}
                />
              ))}
            </SortableContext>
          </DndContext>
        </div>
      )}
    </div>
  );
}

function SortableModelItem({
  id, index, isCombo, kind, canMoveUp, canMoveDown, onMoveUp, onMoveDown, onRemove,
  isComboLabel, upAria, downAria, removeAria,
}: {
  id: string;
  index: number;
  isCombo: boolean;
  kind: string;
  canMoveUp: boolean;
  canMoveDown: boolean;
  onMoveUp: () => void;
  onMoveDown: () => void;
  onRemove: () => void;
  isComboLabel: string;
  upAria: string;
  downAria: string;
  removeAria: string;
}) {
  const { attributes, listeners, setNodeRef, transform, transition, isDragging } = useSortable({ id });

  return (
    <div
      ref={setNodeRef}
      style={{ transform: CSS.Transform.toString(transform), transition }}
      className={cn(
        "flex items-center gap-2 bg-surface-secondary rounded-lg px-3 py-2",
        isDragging && "opacity-50 z-10 shadow-lg ring-2 ring-accent",
      )}
    >
      <button
        type="button"
        className="touch-none cursor-grab active:cursor-grabbing text-muted hover:text-foreground shrink-0"
        {...attributes}
        {...listeners}
        aria-label="Drag to reorder"
      >
        <IconGrip className="w-3.5 h-3.5 opacity-60" />
      </button>
      <span className="text-xs text-muted w-5 tabular-nums">{index + 1}.</span>
      {isCombo && <IconStack className="w-3 h-3 shrink-0 text-muted" />}
      <code className="text-xs flex-1 truncate">{id}</code>
      {isCombo && (
        <Chip size="sm" variant="soft" color="default" className="text-[10px]">{isComboLabel}</Chip>
      )}
      <Chip size="sm" variant="soft" color={KIND_COLORS[kind] ?? "default"} className="text-[10px]">
        {kind}
      </Chip>
      <div className="flex gap-0.5">
        <Button isIconOnly size="sm" variant="ghost" isDisabled={!canMoveUp} onPress={onMoveUp} aria-label={upAria}>
          <IconArrow dir="up" className="w-3.5 h-3.5" />
        </Button>
        <Button isIconOnly size="sm" variant="ghost" isDisabled={!canMoveDown} onPress={onMoveDown} aria-label={downAria}>
          <IconArrow dir="down" className="w-3.5 h-3.5" />
        </Button>
        <Button isIconOnly size="sm" variant="ghost" className="text-danger" onPress={onRemove} aria-label={removeAria}>
          <IconX className="w-3.5 h-3.5" />
        </Button>
      </div>
    </div>
  );
}
