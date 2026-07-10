import { useEffect, useState, useCallback } from "react";
import {
  Table, TableHeader, TableColumn, TableBody, TableRow, TableCell,
  Button, Modal, ModalContent, ModalHeader, ModalBody, ModalFooter,
  Input, Select, SelectItem, Chip, useDisclosure, Spinner,
} from "@heroui/react";
import { api, type Provider, type ModelEntry, type ProviderDef } from "../api";

const FORMATS = ["auto", "openai", "anthropic", "gemini", "responses"];
const AUTHS = ["bearer", "x-api-key", "none"];

const empty = {
  provider_id: "", name: "", api_key: "", base_url: "",
  format: "auto", auth: "bearer", template_id: "",
};

export default function Providers() {
  const [items, setItems] = useState<Provider[]>([]);
  const [catalog, setCatalog] = useState<ProviderDef[]>([]);
  const [loading, setLoading] = useState(true);
  const { isOpen, onOpen, onClose } = useDisclosure();
  const [form, setForm] = useState<Record<string, string>>(empty);
  const [editId, setEditId] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);
  const [step, setStep] = useState<"pick" | "form" | "oauth">("pick");
  const [error, setError] = useState("");
  const [oauthProviders, setOauthProviders] = useState<string[]>([]);
  const [oauthState, setOauthState] = useState("");
  const [oauthCode, setOauthCode] = useState("");
  const [oauthProvider, setOauthProvider] = useState("");
  const [oauthAuthURL, setOauthAuthURL] = useState("");

  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [modelsCache, setModelsCache] = useState<Record<string, ModelEntry[]>>({});
  const [modelErrors, setModelErrors] = useState<Record<string, string>>({});
  const [loadingModels, setLoadingModels] = useState<string | null>(null);

  const load = () => {
    setLoading(true);
    api.providers.list().then(setItems).catch(() => setItems([])).finally(() => setLoading(false));
  };
  useEffect(load, []);

  const openNew = () => {
    setForm(empty);
    setEditId(null);
    setStep("pick");
    setError("");
    setOauthCode("");
    setOauthState("");
    setOauthAuthURL("");
    api.providers.catalog().then(setCatalog).catch(() => setCatalog([]));
    api.oauth.list().then(setOauthProviders).catch(() => setOauthProviders([]));
    onOpen();
  };
  const openEdit = (p: Provider) => {
    setForm({
      provider_id: p.provider_id, name: p.name, api_key: "", base_url: p.base_url,
      format: p.format, auth: p.auth, template_id: "",
    });
    setEditId(p.id);
    setStep("form");
    setError("");
    onOpen();
  };

  const pickTemplate = async (t: ProviderDef) => {
    // OAuth providers with a registered flow use Connect instead of API key.
    if ((t.category === "oauth" || t.category === "free") && oauthProviders.includes(t.id)) {
      setOauthProvider(t.id);
      setError("");
      try {
        const res = await api.oauth.start(t.id);
        setOauthState(res.state);
        setOauthAuthURL(res.auth_url);
        setStep("oauth");
        window.open(res.auth_url, "_blank", "noopener,noreferrer");
      } catch (e: any) {
        setError(e?.message ?? "oauth start failed");
      }
      return;
    }
    setForm({
      provider_id: t.id,
      name: t.display.name,
      api_key: t.no_auth ? "public" : "",
      base_url: t.transport.base_url,
      format: t.transport.format || "openai",
      auth: t.no_auth ? "bearer" : (t.transport.auth || "bearer"),
      template_id: t.id,
    });
    setStep("form");
  };

  const completeOAuth = async () => {
    setSaving(true);
    setError("");
    try {
      await api.oauth.complete(oauthProvider, { state: oauthState, code: oauthCode });
      onClose();
      load();
    } catch (e: any) {
      setError(e?.message ?? "oauth complete failed");
    } finally {
      setSaving(false);
    }
  };

  const openCustom = () => {
    setForm(empty);
    setStep("form");
  };

  const submit = async () => {
    setSaving(true);
    setError("");
    try {
      const payload: Record<string, unknown> = {
        provider_id: form.provider_id,
        name: form.name,
        api_key: form.api_key,
        base_url: form.base_url,
        format: form.format,
        auth: form.auth,
      };
      if (form.template_id) payload.template_id = form.template_id;
      if (editId) await api.providers.update(editId, payload as any);
      else await api.providers.create(payload as any);
      onClose();
      load();
    } catch (e: any) {
      setError(e?.message ?? "falha ao salvar");
    } finally {
      setSaving(false);
    }
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
      setModelErrors((m) => {
        const n = { ...m };
        delete n[p.id];
        return n;
      });
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
            Nenhum provider ainda. Clique em <strong>Novo provider</strong> e escolha um preset ou custom.
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
          <ModalHeader>
            {editId ? "Editar provider" : step === "pick" ? "Escolher provider" : "Configurar conexão"}
          </ModalHeader>
          <ModalBody className="gap-4">
            {!editId && step === "pick" && (
              <>
                <div className="grid grid-cols-2 sm:grid-cols-3 gap-2 max-h-72 overflow-y-auto">
                  {catalog.map((t) => {
                    const isOauth = oauthProviders.includes(t.id);
                    return (
                    <button
                      key={t.id}
                      type="button"
                      onClick={() => pickTemplate(t)}
                      className="text-left rounded-xl border border-default-100 p-3 hover:border-primary/50 hover:bg-default-50 transition-colors"
                    >
                      <div className="flex items-center gap-2 mb-1">
                        <span className="w-2.5 h-2.5 rounded-full shrink-0" style={{ background: t.display.color || "#888" }} />
                        <span className="font-medium text-sm truncate">{t.display.name}</span>
                      </div>
                      <p className="text-[11px] text-default-400 font-mono truncate">{t.id}</p>
                      <div className="flex flex-wrap gap-1 mt-2">
                        {isOauth && <Chip size="sm" color="secondary" variant="flat" className="h-5 text-[10px]">OAuth</Chip>}
                        {t.capabilities?.slice(0, 2).map((c) => (
                          <Chip key={c} size="sm" variant="flat" className="h-5 text-[10px]">{c}</Chip>
                        ))}
                      </div>
                    </button>
                    );
                  })}
                </div>
                <Button variant="flat" onPress={openCustom}>Custom / OpenAI-compatible</Button>
                {error && <p className="text-sm text-danger">{error}</p>}
              </>
            )}
            {step === "oauth" && (
              <>
                <Button size="sm" variant="light" className="self-start" onPress={() => setStep("pick")}>← voltar</Button>
                <div className="bg-primary/10 rounded-lg p-3 text-sm space-y-1">
                  <p className="font-medium">Conectando <strong>{oauthProvider}</strong></p>
                  <p className="text-default-600">
                    1. Uma janela de login foi aberta no seu navegador<br/>
                    2. Após autorizar, o navegador vai tentar abrir <code className="text-xs">localhost</code> e mostrar um erro<br/>
                    3. Isso é esperado! Copie a URL completa da barra de endereços<br/>
                    4. Cole abaixo e clique em <strong>Conectar</strong>
                  </p>
                </div>
                {oauthAuthURL && (
                  <a href={oauthAuthURL} target="_blank" rel="noreferrer" className="text-sm text-primary underline break-all">
                    Abrir login novamente
                  </a>
                )}
                <Input
                  label="URL de callback (cole a URL inteira que apareceu no browser)"
                  placeholder="http://localhost:1/callback?code=4/0A...&state=..."
                  value={oauthCode}
                  onValueChange={setOauthCode}
                  description="Ou cole apenas o valor do parâmetro code"
                />
                {error && <p className="text-sm text-danger">{error}</p>}
              </>
            )}
            {(editId || step === "form") && (
              <>
                {!editId && (
                  <Button size="sm" variant="light" className="self-start" onPress={() => setStep("pick")}>← voltar</Button>
                )}
                <div className="grid grid-cols-2 gap-4">
                  <Input label="Provider ID" placeholder="ex: openai" value={form.provider_id} onValueChange={(v) => setForm({ ...form, provider_id: v })} isDisabled={!!editId || !!form.template_id} />
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
                {error && <p className="text-sm text-danger">{error}</p>}
              </>
            )}
          </ModalBody>
          <ModalFooter>
            <Button variant="flat" onPress={onClose}>Cancelar</Button>
            {(editId || step === "form") && (
              <Button color="primary" onPress={submit} isLoading={saving}>Salvar</Button>
            )}
            {step === "oauth" && (
              <Button color="primary" onPress={completeOAuth} isLoading={saving} isDisabled={!oauthCode.trim()}>
                Conectar
              </Button>
            )}
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
          <code className="text-xs flex-1 truncate">{m.id}</code>
          <Chip size="sm" variant="flat" color="primary">{m.kind}</Chip>
          <Chip size="sm" variant="bordered">{m.source}</Chip>
          <Chip size="sm" variant="flat" color={m.is_active ? "success" : "default"}>
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
