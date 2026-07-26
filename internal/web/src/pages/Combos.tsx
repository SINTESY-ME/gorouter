import { useEffect, useState } from "react";
import {
  Table, TableHeader, TableColumn, TableBody, TableRow, TableCell,
  Button, Modal, ModalContent, ModalHeader, ModalBody, ModalFooter,
  Input, Chip, useDisclosure, Select, SelectItem, Spinner,
  Autocomplete, AutocompleteItem, Textarea,
} from "@heroui/react";
import { api, type Combo, type ModelEntry, type ComboModelMeta } from "../api";

const KIND_COLORS: Record<string, "primary" | "success" | "warning" | "secondary" | "danger" | "default"> = {
  llm: "primary", embedding: "success", image: "warning", tts: "secondary", stt: "danger",
  rerank: "default", ocr: "default", video: "default",
};

const STRATEGY_COLORS: Record<string, "primary" | "success" | "warning" | "secondary" | "danger" | "default"> = {
  ordered_fallback: "secondary",
  "round-robin": "warning",
  velocity: "success",
  intelligence: "primary",
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
  const { isOpen, onOpen, onClose } = useDisclosure();
  const [form, setForm] = useState<ComboForm>(empty);
  const [editId, setEditId] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);
  const [allCatalogModels, setAllCatalogModels] = useState<ModelEntry[]>([]);

  const load = () => {
    setLoading(true);
    api.combos.list().then(setItems).catch(() => setItems([])).finally(() => setLoading(false));
  };
  useEffect(load, []);

  // Fetch all models for the classifier model picker
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
    onOpen();
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
    onOpen();
  };

  const submit = async () => {
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
      onClose();
      load();
    } finally {
      setSaving(false);
    }
  };

  const remove = async (id: string) => {
    if (confirm("Remover este combo?")) {
      await api.combos.remove(id);
      load();
    }
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
          <p className="text-sm text-default-500 mt-0.5">{items.length} combos cadastrados</p>
        </div>
        <Button color="primary" variant="bordered" onPress={openNew} startContent={<IconPlus />}>
          Novo combo
        </Button>
      </div>

      <div className="bg-content1 rounded-2xl border border-default-100 overflow-hidden">
        {loading ? (
          <div className="p-10 text-center text-default-500 text-sm">Carregando...</div>
        ) : items.length === 0 ? (
          <div className="p-10 text-center text-default-500 text-sm">
            Nenhum combo ainda. Clique em <strong>Novo combo</strong>.
          </div>
        ) : (
          <Table aria-label="combos" removeWrapper>
            <TableHeader>
              <TableColumn>NOME</TableColumn>
              <TableColumn>MODELOS</TableColumn>
              <TableColumn>ESTRATÉGIA</TableColumn>
              <TableColumn align="end">AÇÕES</TableColumn>
            </TableHeader>
            <TableBody items={items}>
              {(c) => (
                <TableRow key={c.id}>
                  <TableCell>
                    <span className="font-semibold">{c.name}</span>
                  </TableCell>
                  <TableCell>
                    <div className="flex flex-wrap gap-1">
                      {c.models.map((m, i) => {
                        const meta = c.model_meta?.[m];
                        return (
                          <Chip key={m + i} size="sm" variant="bordered">
                            <span className="text-default-400 mr-0.5">{i + 1}.</span>
                            {m}
                            {c.strategy === "intelligence" && meta?.weight != null && (
                              <span className="ml-1 text-[10px] bg-primary-100 text-primary-700 dark:bg-primary-950 dark:text-primary-300 px-1 rounded font-mono">
                                peso {meta.weight}
                              </span>
                            )}
                          </Chip>
                        );
                      })}
                    </div>
                  </TableCell>
                  <TableCell>
                    <div className="flex flex-col gap-0.5 items-start">
                      <Chip size="sm" variant="flat" color={STRATEGY_COLORS[c.strategy] ?? "default"}>
                        {c.strategy}
                      </Chip>
                      {c.strategy === "intelligence" && c.classifier_model && (
                        <span className="text-[11px] text-default-400 font-mono">
                          classificador: {c.classifier_model}
                        </span>
                      )}
                    </div>
                  </TableCell>
                  <TableCell>
                    <div className="flex gap-1 justify-end">
                      <Button isIconOnly size="sm" variant="light" onPress={() => openEdit(c)} aria-label="editar">
                        <IconPencil />
                      </Button>
                      <Button isIconOnly size="sm" variant="light" color="danger" onPress={() => remove(c.id)} aria-label="excluir">
                        <IconTrash />
                      </Button>
                    </div>
                  </TableCell>
                </TableRow>
              )}
            </TableBody>
          </Table>
        )}
      </div>

      <Modal isOpen={isOpen} onClose={onClose} size="xl" scrollBehavior="inside">
        <ModalContent>
          <ModalHeader>{editId ? "Editar combo" : "Novo combo"}</ModalHeader>
          <ModalBody className="gap-4">
            <Input
              label="Nome"
              placeholder="ex: smart, fast, balanced"
              value={form.name}
              onValueChange={(v) => setForm({ ...form, name: v })}
            />

            <ModelSelector
              selected={form.models}
              onChange={(models) => setForm({ ...form, models })}
            />

            <Select
              label="Estratégia"
              description="Forma como o Gorouter seleciona entre os modelos declarados."
              selectedKeys={[form.strategy]}
              onSelectionChange={(keys) => {
                const v = Array.from(keys)[0] as string;
                if (v) setForm({ ...form, strategy: v });
              }}
            >
              <SelectItem key="ordered_fallback" description="Usa o 1º modelo da lista e cai para os seguintes em caso de falha.">
                ordered_fallback (Fallback em ordem)
              </SelectItem>
              <SelectItem key="round-robin" description="Alterna circularmente a cada requisição para distribuir a carga.">
                round-robin (Alternância simples)
              </SelectItem>
              <SelectItem key="velocity" description="Roteia para o modelo dos escolhidos com a maior taxa de tokens/seg (TPS) observada.">
                velocity (Maior velocidade / TPS)
              </SelectItem>
              <SelectItem key="intelligence" description="Classifica a complexidade do prompt via IA e roteia para o modelo com o peso ideal.">
                intelligence (Classificação de complexidade)
              </SelectItem>
            </Select>

            {form.strategy === "intelligence" && (
              <div className="space-y-4 p-3.5 bg-content2/50 rounded-xl border border-default-100">
                <div className="text-xs font-semibold text-primary uppercase tracking-wide flex items-center gap-1.5">
                  <IconSparkles /> Configurações da Estratégia Intelligence
                </div>

                <Autocomplete
                  label="Modelo Classificador"
                  placeholder="Selecione o modelo que vai classificar a complexidade (ex: openai/gpt-4o-mini)..."
                  selectedKey={form.classifier_model || null}
                  onSelectionChange={(key) => setForm({ ...form, classifier_model: (key as string) ?? "" })}
                >
                  {allCatalogModels.map((m) => (
                    <AutocompleteItem key={m.id} textValue={m.id}>
                      <div className="flex justify-between items-center w-full">
                        <span className="font-mono text-xs">{m.id}</span>
                        <Chip size="sm" variant="flat" color={KIND_COLORS[m.kind] ?? "default"} className="text-[10px]">
                          {m.kind}
                        </Chip>
                      </div>
                    </AutocompleteItem>
                  ))}
                </Autocomplete>

                {form.models.length > 0 && (
                  <div className="space-y-3">
                    <label className="text-xs font-medium text-default-600 uppercase tracking-wide">
                      Pesos e Capacidades dos Modelos (escala 1 a 10)
                    </label>
                    {form.models.map((m) => {
                      const meta = form.model_meta[m] ?? { weight: 5, description: "" };
                      return (
                        <div key={m} className="bg-content1 p-3 rounded-lg border border-default-200 space-y-2">
                          <div className="flex items-center justify-between gap-2">
                            <code className="text-xs font-mono font-semibold">{m}</code>
                            <div className="flex items-center gap-2">
                              <span className="text-xs text-default-500 font-medium">Peso (1..10):</span>
                              <Input
                                type="number"
                                size="sm"
                                className="w-20"
                                min={1}
                                max={10}
                                value={String(meta.weight ?? 5)}
                                onValueChange={(v) => updateMeta(m, { weight: Math.max(1, Math.min(10, parseInt(v) || 1)) })}
                              />
                            </div>
                          </div>
                          <Textarea
                            size="sm"
                            placeholder="Descrição/Capacidades (ex: tarefas complexas de lógica e código, ou respostas simples e rápidas)..."
                            minRows={1}
                            maxRows={3}
                            value={meta.description ?? ""}
                            onValueChange={(v) => updateMeta(m, { description: v })}
                          />
                        </div>
                      );
                    })}
                  </div>
                )}
              </div>
            )}
          </ModalBody>
          <ModalFooter>
            <Button variant="flat" onPress={onClose}>
              Cancelar
            </Button>
            <Button color="primary" onPress={submit} isLoading={saving}>
              Salvar
            </Button>
          </ModalFooter>
        </ModalContent>
      </Modal>
    </div>
  );
}

function ModelSelector({ selected, onChange }: { selected: string[]; onChange: (m: string[]) => void }) {
  const [allModels, setAllModels] = useState<ModelEntry[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [searchValue, setSearchValue] = useState("");

  useEffect(() => {
    let cancelled = false;
    (async () => {
      setLoading(true);
      try {
        const ps = await api.providers.list();
        const results = await Promise.allSettled(ps.map((p) => api.providers.models(p.id)));
        if (cancelled) return;
        const models: ModelEntry[] = [];
        results.forEach((r) => {
          if (r.status === "fulfilled") {
            r.value.forEach((m) => models.push(m));
          }
        });
        setAllModels(models);
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

  const fixedKind = selected.length > 0 ? allModels.find((m) => m.id === selected[0])?.kind : undefined;

  const available = allModels.filter((m) => {
    if (selected.includes(m.id)) return false;
    if (fixedKind && m.kind !== fixedKind) return false;
    return true;
  });

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
        <label className="text-sm text-default-500">Modelos</label>
        <p className="text-xs text-default-400 mt-0.5 mb-2">
          Selecione os modelos do combo.
          {fixedKind && (
            <>
              {" "}
              Tipo fixado:{" "}
              <Chip size="sm" variant="flat" color={KIND_COLORS[fixedKind] ?? "default"}>
                {fixedKind}
              </Chip>
            </>
          )}
        </p>
        {loading ? (
          <div className="flex items-center gap-2 py-2 text-sm text-default-500">
            <Spinner size="sm" /> Carregando models...
          </div>
        ) : error && allModels.length === 0 ? (
          <div className="text-sm text-danger py-2">Erro: {error}</div>
        ) : available.length === 0 ? (
          <div className="text-sm text-default-400 py-2">
            {fixedKind ? `Nenhum model disponível do tipo ${fixedKind}.` : "Nenhum model disponível."}
          </div>
        ) : (
          <div className="space-y-2">
            <Autocomplete
              label="Modelos disponíveis"
              placeholder="Buscar model (ou digite um personalizado)..."
              selectedKey={null}
              inputValue={searchValue}
              onInputChange={setSearchValue}
              onSelectionChange={(key) => {
                if (key) {
                  toggleModel(key as string);
                  setSearchValue("");
                }
              }}
              maxListHeight={300}
            >
              {available.map((m) => (
                <AutocompleteItem key={m.id} textValue={m.id}>
                  <div className="flex items-center justify-between w-full gap-2">
                    <span className="font-mono text-xs">{m.id}</span>
                    <div className="flex items-center gap-1">
                      {!m.is_active && (
                        <Chip size="sm" variant="dot" color="warning" className="text-[10px]">
                          inativo
                        </Chip>
                      )}
                      <Chip size="sm" variant="flat" color={KIND_COLORS[m.kind] ?? "default"} className="text-[10px]">
                        {m.kind}
                      </Chip>
                    </div>
                  </div>
                </AutocompleteItem>
              ))}
            </Autocomplete>
            {searchValue.trim() && !available.some((m) => m.id === searchValue.trim()) && (
              <Button
                size="sm"
                variant="flat"
                color="primary"
                className="w-full font-mono text-xs justify-start"
                onPress={() => {
                  toggleModel(searchValue.trim());
                  setSearchValue("");
                }}
              >
                + Adicionar modelo personalizado: "{searchValue.trim()}"
              </Button>
            )}
          </div>
        )}
      </div>

      {selected.length > 0 && (
        <div className="space-y-1.5">
          <p className="text-xs text-default-500 uppercase tracking-wide font-medium">Modelos Selecionados</p>
          {selected.map((id, i) => {
            const entry = allModels.find((m) => m.id === id);
            return (
              <div key={id + i} className="flex items-center gap-2 bg-content2 rounded-lg px-3 py-2">
                <span className="text-xs text-default-400 w-5 tabular-nums">{i + 1}.</span>
                <code className="text-xs flex-1 truncate">{id}</code>
                {entry && (
                  <Chip size="sm" variant="flat" color={KIND_COLORS[entry.kind] ?? "default"}>
                    {entry.kind}
                  </Chip>
                )}
                <div className="flex gap-0.5">
                  <Button isIconOnly size="sm" variant="light" isDisabled={i === 0} onPress={() => move(i, -1)} aria-label="subir">
                    <IconArrow dir="up" />
                  </Button>
                  <Button isIconOnly size="sm" variant="light" isDisabled={i === selected.length - 1} onPress={() => move(i, 1)} aria-label="descer">
                    <IconArrow dir="down" />
                  </Button>
                  <Button isIconOnly size="sm" variant="light" color="danger" onPress={() => removeAt(i)} aria-label="remover">
                    <IconX />
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

function IconPlus() {
  return (
    <svg className="w-4 h-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
      <path d="M5 12h14M12 5v14" />
    </svg>
  );
}
function IconPencil() {
  return (
    <svg className="w-4 h-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
      <path d="M12 20h9" />
      <path d="M16.5 3.5a2.12 2.12 0 0 1 3 3L7 19l-4 1 1-4Z" />
    </svg>
  );
}
function IconTrash() {
  return (
    <svg className="w-4 h-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
      <polyline points="3 6 5 6 21 6" />
      <path d="M19 6l-1.5 14a2 2 0 0 1-2 2H8.5a2 2 0 0 1-2-2L5 6" />
      <path d="M10 11v6M14 11v6" />
    </svg>
  );
}
function IconArrow({ dir }: { dir: "up" | "down" }) {
  return (
    <svg className={`w-3.5 h-3.5 ${dir === "down" ? "rotate-180" : ""}`} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
      <polyline points="18 15 12 9 6 15" />
    </svg>
  );
}
function IconX() {
  return (
    <svg className="w-3.5 h-3.5" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
      <path d="M18 6 6 18M6 6l12 12" />
    </svg>
  );
}
function IconSparkles() {
  return (
    <svg className="w-4 h-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
      <path d="m12 3-1.9 5.8a2 2 0 0 1-1.3 1.3L3 12l5.8 1.9a2 2 0 0 1 1.3 1.3L12 21l1.9-5.8a2 2 0 0 1 1.3-1.3L21 12l-5.8-1.9a2 2 0 0 1-1.3-1.3Z" />
    </svg>
  );
}
