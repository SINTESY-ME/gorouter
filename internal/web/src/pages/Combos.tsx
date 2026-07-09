import { useEffect, useState, useMemo } from "react";
import {
  Table, TableHeader, TableColumn, TableBody, TableRow, TableCell,
  Button, Modal, ModalContent, ModalHeader, ModalBody, ModalFooter,
  Input, Chip, useDisclosure, Select, SelectItem, Spinner,
} from "@heroui/react";
import { api, type Combo, type ModelEntry } from "../api";

const KIND_COLORS: Record<string, "primary" | "success" | "warning" | "secondary" | "danger" | "default"> = {
  llm: "primary", embedding: "success", image: "warning", tts: "secondary", stt: "danger",
  rerank: "default", ocr: "default", video: "default",
};

const empty = { name: "", models: [] as string[], strategy: "ordered_fallback" };

export default function Combos() {
  const [items, setItems] = useState<Combo[]>([]);
  const [loading, setLoading] = useState(true);
  const { isOpen, onOpen, onClose } = useDisclosure();
  const [form, setForm] = useState<{ name: string; models: string[]; strategy: string }>(empty);
  const [editId, setEditId] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);

  const load = () => {
    setLoading(true);
    api.combos.list().then(setItems).catch(() => setItems([])).finally(() => setLoading(false));
  };
  useEffect(load, []);

  const openNew = () => { setForm({ ...empty, models: [] }); setEditId(null); onOpen(); };
  const openEdit = (c: Combo) => {
    setForm({ name: c.name, models: [...c.models], strategy: c.strategy });
    setEditId(c.id); onOpen();
  };

  const submit = async () => {
    setSaving(true);
    try {
      const payload = { ...form, models: form.models };
      if (editId) await api.combos.update(editId, payload as any);
      else await api.combos.create(payload as any);
      onClose(); load();
    } finally { setSaving(false); }
  };

  const remove = async (id: string) => {
    if (confirm("Remover este combo?")) { await api.combos.remove(id); load(); }
  };

  return (
    <div className="space-y-5">
      <div className="flex justify-between items-center">
        <div>
          <h1 className="text-2xl font-bold tracking-tight">Combos</h1>
          <p className="text-sm text-default-500 mt-0.5">{items.length} combos cadastrados</p>
        </div>
        <Button color="primary" variant="bordered" onPress={openNew} startContent={<IconPlus />}>Novo combo</Button>
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
                  <TableCell><span className="font-semibold">{c.name}</span></TableCell>
                  <TableCell>
                    <div className="flex flex-wrap gap-1">
                      {c.models.map((m, i) => (
                        <Chip key={m + i} size="sm" variant="bordered">
                          <span className="text-default-400 mr-0.5">{i + 1}.</span>{m}
                        </Chip>
                      ))}
                    </div>
                  </TableCell>
                  <TableCell>
                    <Chip size="sm" variant="flat" color={c.strategy === "round-robin" ? "warning" : "secondary"}>
                      {c.strategy}
                    </Chip>
                  </TableCell>
                  <TableCell>
                    <div className="flex gap-1 justify-end">
                      <Button isIconOnly size="sm" variant="light" onPress={() => openEdit(c)} aria-label="editar"><IconPencil /></Button>
                      <Button isIconOnly size="sm" variant="light" color="danger" onPress={() => remove(c.id)} aria-label="excluir"><IconTrash /></Button>
                    </div>
                  </TableCell>
                </TableRow>
              )}
            </TableBody>
          </Table>
        )}
      </div>

      <Modal isOpen={isOpen} onClose={onClose} size="lg">
        <ModalContent>
          <ModalHeader>{editId ? "Editar combo" : "Novo combo"}</ModalHeader>
          <ModalBody className="gap-4">
            <Input label="Nome" placeholder="ex: smart, fast, balanced" value={form.name} onValueChange={(v) => setForm({ ...form, name: v })} />
            <ModelSelector
              selected={form.models}
              onChange={(models) => setForm({ ...form, models })}
            />
            <Select
              label="Estratégia"
              description="ordered_fallback: usa 1º, cai para 2º em caso de falha. round-robin: alterna a cada requisição."
              selectedKeys={[form.strategy]}
              onSelectionChange={(keys) => {
                const v = Array.from(keys)[0] as string;
                if (v) setForm({ ...form, strategy: v });
              }}
            >
              <SelectItem key="ordered_fallback">ordered_fallback</SelectItem>
              <SelectItem key="round-robin">round-robin</SelectItem>
            </Select>
          </ModalBody>
          <ModalFooter>
            <Button variant="flat" onPress={onClose}>Cancelar</Button>
            <Button color="primary" onPress={submit} isLoading={saving}>Salvar</Button>
          </ModalFooter>
        </ModalContent>
      </Modal>
    </div>
  );
}

// ModelSelector fetches all active models from the catalog (database) and
// lets the user pick multiple. When the first model is selected, the Kind is
// fixed and only models of the same Kind are shown.
function ModelSelector({ selected, onChange }: { selected: string[]; onChange: (m: string[]) => void }) {
  const [allModels, setAllModels] = useState<ModelEntry[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    (async () => {
      setLoading(true);
      try {
        const ps = await api.providers.list();
        const active = ps.filter((p) => p.is_active);
        const results = await Promise.allSettled(
          active.map((p) => api.providers.models(p.id))
        );
        if (cancelled) return;
        const models: ModelEntry[] = [];
        results.forEach((r) => {
          if (r.status === "fulfilled") {
            r.value.forEach((m) => { if (m.is_active) models.push(m); });
          }
        });
        setAllModels(models);
      } catch (e: any) {
        if (!cancelled) setError(e?.message ?? "erro");
      } finally {
        if (!cancelled) setLoading(false);
      }
    })();
    return () => { cancelled = true; };
  }, []);

  // Determine the Kind of the first selected model.
  const fixedKind = selected.length > 0
    ? allModels.find((m) => m.id === selected[0])?.kind
    : undefined;

  // Available models: active, not yet selected, same Kind as first (if any).
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
          Selecione os models do combo. A ordem importa para ordered_fallback.
          {fixedKind && <> Tipo fixado: <Chip size="sm" variant="flat" color={KIND_COLORS[fixedKind] ?? "default"}>{fixedKind}</Chip></>}
        </p>
        {loading ? (
          <div className="flex items-center gap-2 py-2 text-sm text-default-500"><Spinner size="sm" /> Carregando models...</div>
        ) : error && allModels.length === 0 ? (
          <div className="text-sm text-danger py-2">Erro: {error}</div>
        ) : available.length === 0 ? (
          <div className="text-sm text-default-400 py-2">
            {fixedKind ? `Nenhum model disponível do tipo ${fixedKind}.` : "Nenhum model disponível."}
          </div>
        ) : (
          <Select
            label="Modelos disponíveis"
            placeholder="Selecione um model para adicionar"
            selectedKeys={[]}
            onChange={(e) => { if (e.target.value) toggleModel(e.target.value); }}
          >
            {available.map((m) => (
              <SelectItem key={m.id}>
                {m.id} ({m.kind})
              </SelectItem>
            ))}
          </Select>
        )}
      </div>

      {selected.length > 0 && (
        <div className="space-y-1.5">
          <p className="text-xs text-default-500 uppercase tracking-wide font-medium">Ordem do fallback</p>
          {selected.map((id, i) => {
            const entry = allModels.find((m) => m.id === id);
            return (
              <div key={id + i} className="flex items-center gap-2 bg-content2 rounded-lg px-3 py-2">
                <span className="text-xs text-default-400 w-5 tabular-nums">{i + 1}.</span>
                <code className="text-xs flex-1 truncate">{id}</code>
                {entry && <Chip size="sm" variant="flat" color={KIND_COLORS[entry.kind] ?? "default"}>{entry.kind}</Chip>}
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

function IconPlus() { return <svg className="w-4 h-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><path d="M5 12h14M12 5v14"/></svg>; }
function IconPencil() { return <svg className="w-4 h-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><path d="M12 20h9"/><path d="M16.5 3.5a2.12 2.12 0 0 1 3 3L7 19l-4 1 1-4Z"/></svg>; }
function IconTrash() { return <svg className="w-4 h-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><polyline points="3 6 5 6 21 6"/><path d="M19 6l-1.5 14a2 2 0 0 1-2 2H8.5a2 2 0 0 1-2-2L5 6"/><path d="M10 11v6M14 11v6"/></svg>; }
function IconArrow({ dir }: { dir: "up" | "down" }) {
  return <svg className={`w-3.5 h-3.5 ${dir === "down" ? "rotate-180" : ""}`} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><polyline points="18 15 12 9 6 15"/></svg>;
}
function IconX() { return <svg className="w-3.5 h-3.5" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><path d="M18 6 6 18M6 6l12 12"/></svg>; }