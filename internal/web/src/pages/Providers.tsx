import { useEffect, useState, useCallback } from "react";
import {
  Table, TableHeader, TableColumn, TableBody, TableRow, TableCell,
  Button, Modal, ModalContent, ModalHeader, ModalBody, ModalFooter,
  Input, Select, SelectItem, Chip, useDisclosure, Spinner,
} from "@heroui/react";
import { api, type Provider, type ModelEntry } from "../api";

const FORMATS = ["auto", "openai", "anthropic", "gemini", "responses"];
const AUTHS = ["bearer", "x-api-key", "none"];

const empty = {
  provider_id: "", name: "", api_key: "", base_url: "",
  format: "auto", auth: "bearer",
};

export default function Providers() {
  const [items, setItems] = useState<Provider[]>([]);
  const [loading, setLoading] = useState(true);
  const { isOpen, onOpen, onClose } = useDisclosure();
  const [form, setForm] = useState<Record<string, string>>(empty);
  const [editId, setEditId] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);

  // Selected provider for the models panel below the table.
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [modelsCache, setModelsCache] = useState<Record<string, ModelEntry[]>>({});
  const [modelErrors, setModelErrors] = useState<Record<string, string>>({});
  const [loadingModels, setLoadingModels] = useState<string | null>(null);

  const load = () => {
    setLoading(true);
    api.providers.list().then(setItems).catch(() => setItems([])).finally(() => setLoading(false));
  };
  useEffect(load, []);

  const openNew = () => { setForm(empty); setEditId(null); onOpen(); };
  const openEdit = (p: Provider) => {
    setForm({ provider_id: p.provider_id, name: p.name, api_key: p.api_key, base_url: p.base_url, format: p.format, auth: p.auth });
    setEditId(p.id); onOpen();
  };

  const submit = async () => {
    setSaving(true);
    try {
      if (editId) await api.providers.update(editId, form as any);
      else await api.providers.create(form as any);
      onClose(); load();
    } finally { setSaving(false); }
  };

  const remove = async (id: string) => {
    if (confirm("Remover este provider?")) {
      await api.providers.remove(id);
      if (selectedId === id) setSelectedId(null);
      load();
    }
  };

  const selectProvider = useCallback(async (p: Provider) => {
    if (selectedId === p.id) {
      setSelectedId(null);
      return;
    }
    setSelectedId(p.id);
    if (modelsCache[p.id] || modelErrors[p.id]) return;
    setLoadingModels(p.id);
    try {
      const entries = await api.providers.models(p.id);
      setModelsCache((c) => ({ ...c, [p.id]: entries }));
    } catch (e: any) {
      setModelErrors((m) => ({ ...m, [p.id]: e.message }));
    } finally {
      setLoadingModels(null);
    }
  }, [selectedId, modelsCache, modelErrors]);

  const syncProviderModels = async (p: Provider) => {
    setLoadingModels(p.id);
    try {
      const entries = await api.providers.syncModels(p.id);
      setModelsCache((c) => ({ ...c, [p.id]: entries }));
    } catch (e: any) {
      setModelErrors((m) => ({ ...m, [p.id]: e.message }));
    } finally {
      setLoadingModels(null);
    }
  };

  const selected = items.find((p) => p.id === selectedId);

  return (
    <div className="space-y-5">
      <div className="flex justify-between items-center">
        <div>
          <h1 className="text-2xl font-bold tracking-tight">Providers</h1>
          <p className="text-sm text-default-500 mt-0.5">{items.length} conexões cadastradas</p>
        </div>
        <Button color="primary" variant="bordered" onPress={openNew} startContent={<IconPlus />}>Novo provider</Button>
      </div>
      <div className="bg-content1 rounded-2xl border border-default-100 overflow-hidden">
        {loading ? (
          <div className="p-10 text-center text-default-500 text-sm">Carregando...</div>
        ) : items.length === 0 ? (
          <div className="p-10 text-center text-default-500 text-sm">
            Nenhum provider ainda. Clique em <strong>Novo provider</strong>.
          </div>
        ) : (
          <Table aria-label="providers" removeWrapper>
            <TableHeader>
              <TableColumn width={40}>{""}</TableColumn>
              <TableColumn>PROVIDER</TableColumn>
              <TableColumn>NOME</TableColumn>
              <TableColumn>BASE URL</TableColumn>
              <TableColumn>FORMATO</TableColumn>
              <TableColumn>STATUS</TableColumn>
              <TableColumn align="end">AÇÕES</TableColumn>
            </TableHeader>
            <TableBody items={items}>
              {(p) => (
                <TableRow
                  key={p.id}
                  className={`cursor-pointer hover:bg-default-100 transition-colors ${selectedId === p.id ? "bg-primary/10" : ""}`}
                  onClick={() => selectProvider(p)}
                >
                  <TableCell>
                    <IconChevron expanded={selectedId === p.id} />
                  </TableCell>
                  <TableCell><span className="font-medium">{p.provider_id}</span></TableCell>
                  <TableCell>{p.name}</TableCell>
                  <TableCell><code className="text-xs text-default-500">{p.base_url}</code></TableCell>
                  <TableCell><Chip size="sm" variant="flat" color="primary">{p.format}</Chip></TableCell>
                  <TableCell>
                    <Chip size="sm" variant="flat" color={p.is_active ? "success" : "default"}>
                      {p.is_active ? "ativo" : "inativo"}
                    </Chip>
                  </TableCell>
                  <TableCell onClick={(e) => e.stopPropagation()}>
                    <div className="flex gap-1 justify-end">
                      <Button isIconOnly size="sm" variant="light" onPress={() => openEdit(p)} aria-label="editar"><IconPencil /></Button>
                      <Button isIconOnly size="sm" variant="light" color="danger" onPress={() => remove(p.id)} aria-label="excluir"><IconTrash /></Button>
                    </div>
                  </TableCell>
                </TableRow>
              )}
            </TableBody>
          </Table>
        )}
      </div>

      {selected && (
        <div className="bg-content1 rounded-2xl border border-default-100 p-5">
          <div className="flex items-center justify-between mb-3">
            <div>
              <h3 className="font-semibold">Models de {selected.provider_id}/{selected.name}</h3>
              <p className="text-xs text-default-500 mt-0.5">Catálogo sincronizado do provider</p>
            </div>
            <div className="flex gap-2 items-center">
              <Button size="sm" variant="flat" onPress={() => syncProviderModels(selected)} isLoading={loadingModels === selected.id}>
                Sincronizar
              </Button>
              <Button isIconOnly size="sm" variant="light" onPress={() => setSelectedId(null)} aria-label="fechar"><IconX /></Button>
            </div>
          </div>
          <ModelsPanel
            loading={loadingModels === selected.id}
            models={modelsCache[selected.id]}
            error={modelErrors[selected.id]}
          />
        </div>
      )}

      <Modal isOpen={isOpen} onClose={onClose} size="lg">
        <ModalContent>
          <ModalHeader>{editId ? "Editar provider" : "Novo provider"}</ModalHeader>
          <ModalBody className="gap-4">
            <div className="grid grid-cols-2 gap-4">
              <Input label="Provider ID" placeholder="ex: openai, anthropic, mock" value={form.provider_id} onValueChange={(v) => setForm({ ...form, provider_id: v })} isDisabled={!!editId} />
              <Input label="Nome" placeholder="ex: conta-1" value={form.name} onValueChange={(v) => setForm({ ...form, name: v })} />
            </div>
            <Input label="Base URL" placeholder="https://api.openai.com" value={form.base_url} onValueChange={(v) => setForm({ ...form, base_url: v })} />
            <Input label="API Key" type="password" placeholder="sk-..." value={form.api_key} onValueChange={(v) => setForm({ ...form, api_key: v })} />
            <div className="grid grid-cols-2 gap-4">
              <Select label="Formato" selectedKeys={[form.format]} onChange={(e) => setForm({ ...form, format: e.target.value })}>
                {FORMATS.map((f) => <SelectItem key={f}>{f}</SelectItem>)}
              </Select>
              <Select label="Auth" selectedKeys={[form.auth]} onChange={(e) => setForm({ ...form, auth: e.target.value })}>
                {AUTHS.map((a) => <SelectItem key={a}>{a}</SelectItem>)}
              </Select>
            </div>
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

function ModelsPanel({ loading, models, error }: { loading: boolean; models?: ModelEntry[]; error?: string }) {
  if (loading) {
    return <div className="py-4 flex items-center gap-2 text-sm text-default-500"><Spinner size="sm" /> Sincronizando...</div>;
  }
  if (error) {
    return <div className="py-4 text-sm text-danger">Erro: {error}</div>;
  }
  if (!models || models.length === 0) {
    return <div className="py-4 text-sm text-default-500">Nenhum model no catálogo. Clique em Sincronizar.</div>;
  }
  return (
    <div className="space-y-1">
      {models.map((m) => (
        <div key={m.id} className="flex items-center gap-2 bg-content2 rounded-lg px-3 py-2">
          <code className="text-xs flex-1 truncate">{m.model_id}</code>
          <Chip size="sm" variant="flat" color="primary">{m.kind}</Chip>
          <Chip size="sm" variant="bordered">{m.source}</Chip>
          <Chip size="sm" variant={m.is_active ? "flat" : "flat"} color={m.is_active ? "success" : "default"}>
            {m.is_active ? "ativo" : "inativo"}
          </Chip>
        </div>
      ))}
    </div>
  );
}

function IconPlus() { return <svg className="w-4 h-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><path d="M5 12h14M12 5v14"/></svg>; }
function IconPencil() { return <svg className="w-4 h-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><path d="M12 20h9"/><path d="M16.5 3.5a2.12 2.12 0 0 1 3 3L7 19l-4 1 1-4Z"/></svg>; }
function IconTrash() { return <svg className="w-4 h-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><polyline points="3 6 5 6 21 6"/><path d="M19 6l-1.5 14a2 2 0 0 1-2 2H8.5a2 2 0 0 1-2-2L5 6"/><path d="M10 11v6M14 11v6"/></svg>; }
function IconX() { return <svg className="w-4 h-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><path d="M18 6 6 18M6 6l12 12"/></svg>; }
function IconChevron({ expanded }: { expanded: boolean }) {
  return (
    <svg
      className={`w-4 h-4 text-default-400 transition-transform ${expanded ? "rotate-90" : ""}`}
      viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"
    >
      <polyline points="9 18 15 12 9 6" />
    </svg>
  );
}