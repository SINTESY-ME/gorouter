import { useEffect, useState, useCallback, useMemo } from "react";
import {
  Table, TableHeader, TableColumn, TableBody, TableRow, TableCell,
  Button, Modal, ModalContent, ModalHeader, ModalBody, ModalFooter,
  Input, Select, SelectItem, Chip, useDisclosure, Spinner,
} from "@heroui/react";
import { api, type Provider, type ModelEntry, type ProviderDef, type ProviderConfig } from "../api";

const FORMATS = ["auto", "openai", "anthropic", "gemini", "responses"];
const AUTHS = ["bearer", "x-api-key", "none"];

const empty = {
  provider_id: "", name: "", api_key: "", base_url: "",
  format: "auto", auth: "bearer", template_id: "",
};

export default function Providers() {
  const [items, setItems] = useState<Provider[]>([]);
  const [configs, setConfigs] = useState<ProviderConfig[]>([]);
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
  const [search, setSearch] = useState("");
  const [savingConfig, setSavingConfig] = useState<string | null>(null);

  const POPULAR = ["openai", "anthropic", "openrouter", "gemini", "groq", "deepseek", "mistral", "together", "ollama", "opencode", "deepinfra", "openadapter"];

  const [selectedProviderId, setSelectedProviderId] = useState<string | null>(null);
  const [modelsCache, setModelsCache] = useState<Record<string, ModelEntry[]>>({});
  const [modelErrors, setModelErrors] = useState<Record<string, string>>({});
  const [loadingModels, setLoadingModels] = useState<string | null>(null);

  const load = () => {
    setLoading(true);
    api.providers.list().then(setItems).catch(() => setItems([])).finally(() => setLoading(false));
  };
  const loadConfigs = () => {
    api.providerConfigs.list().then(setConfigs).catch(() => setConfigs([]));
  };
  useEffect(() => { load(); loadConfigs(); }, []);

  const openNew = () => {
    setForm(empty);
    setEditId(null);
    setStep("pick");
    setError("");
    setOauthCode("");
    setOauthState("");
    setOauthAuthURL("");
    setSearch("");
    api.providers.catalog().then(setCatalog).catch(() => setCatalog([]));
    api.oauth.list().then(setOauthProviders).catch(() => setOauthProviders([]));
    onOpen();
  };
  const openNewKey = (providerId: string, baseUrl: string, format: string, auth: string) => {
    setForm({
      provider_id: providerId, name: "", api_key: "", base_url: baseUrl,
      format: format, auth: auth, template_id: "",
    });
    setEditId(null);
    setStep("form");
    setError("");
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

  const sortedCatalog = [...catalog].sort((a, b) => {
    const ai = POPULAR.indexOf(a.id);
    const bi = POPULAR.indexOf(b.id);
    if (ai !== -1 && bi !== -1) return ai - bi;
    if (ai !== -1) return -1;
    if (bi !== -1) return 1;
    return a.display.name.localeCompare(b.display.name);
  });
  const filteredCatalog = sortedCatalog.filter((t) => {
    const q = search.trim().toLowerCase();
    if (!q) return true;
    return (
      t.id.toLowerCase().includes(q) ||
      t.display.name.toLowerCase().includes(q) ||
      t.category?.toLowerCase().includes(q) ||
      t.capabilities?.some((c) => c.toLowerCase().includes(q))
    );
  });

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
      loadConfigs(); // config may be auto-created
    } catch (e: any) {
      setError(e?.message ?? "falha ao salvar");
    } finally {
      setSaving(false);
    }
  };

  const remove = async (id: string, e?: React.MouseEvent) => {
    if (e) e.stopPropagation();
    if (confirm("Remover esta conexão?")) {
      await api.providers.remove(id);
      load();
    }
  };

  const toggleProvider = useCallback(async (providerId: string) => {
    if (selectedProviderId === providerId) {
      setSelectedProviderId(null);
      return;
    }
    setSelectedProviderId(providerId);
    if (modelsCache[providerId] || modelErrors[providerId]) return;
    setLoadingModels(providerId);
    try {
      // Find the first connection ID for this provider to fetch models
      // models are synced per provider_id, we can pass any valid conn ID or the provider_id itself.
      // Wait, the API endpoint is /api/providers/{id}/models where {id} is the CONNECTION ID!
      const conn = items.find(c => c.provider_id === providerId);
      if (!conn) {
        setLoadingModels(null);
        return;
      }
      const entries = await api.providers.models(conn.id);
      setModelsCache((c) => ({ ...c, [providerId]: entries }));
    } catch (e: any) {
      setModelErrors((m) => ({ ...m, [providerId]: e.message }));
    } finally {
      setLoadingModels(null);
    }
  }, [selectedProviderId, modelsCache, modelErrors, items]);

  const syncProviderModels = async (providerId: string) => {
    const conn = items.find(c => c.provider_id === providerId);
    if (!conn) return;
    setLoadingModels(providerId);
    try {
      const entries = await api.providers.syncModels(conn.id);
      setModelsCache((c) => ({ ...c, [providerId]: entries }));
      setModelErrors((m) => {
        const n = { ...m };
        delete n[providerId];
        return n;
      });
    } catch (e: any) {
      setModelErrors((m) => ({ ...m, [providerId]: e.message }));
    } finally {
      setLoadingModels(null);
    }
  };

  const updateLoadBalance = async (configId: string, lb: string) => {
    setSavingConfig(configId);
    try {
      await api.providerConfigs.update(configId, { load_balance: lb });
      loadConfigs();
    } catch (e: any) {
    } finally {
      setSavingConfig(null);
    }
  };

  // Group connections by provider_id
  const grouped = useMemo(() => {
    const groups: Record<string, Provider[]> = {};
    items.forEach(c => {
      if (!groups[c.provider_id]) groups[c.provider_id] = [];
      groups[c.provider_id].push(c);
    });
    return groups;
  }, [items]);

  // Merge with configs to get all providers
  const allProviderIds = useMemo(() => {
    const ids = new Set<string>();
    configs.forEach(c => ids.add(c.id));
    Object.keys(grouped).forEach(id => ids.add(id));
    return Array.from(ids).sort();
  }, [configs, grouped]);

  return (
    <div className="space-y-5">
      <div className="flex justify-between items-center">
        <div>
          <h1 className="text-2xl font-bold tracking-tight">Providers</h1>
          <p className="text-sm text-default-500 mt-0.5">{allProviderIds.length} providers cadastrados</p>
        </div>
        <Button color="primary" variant="bordered" onPress={openNew} startContent={<IconPlus />}>Novo provider</Button>
      </div>
      
      {loading ? (
        <div className="p-10 text-center text-default-500 text-sm bg-content1 rounded-2xl border border-default-100">Carregando...</div>
      ) : allProviderIds.length === 0 ? (
        <div className="p-10 text-center text-default-500 text-sm bg-content1 rounded-2xl border border-default-100">
          Nenhum provider ainda. Clique em <strong>Novo provider</strong> e escolha um preset ou custom.
        </div>
      ) : (
        <div className="space-y-4">
          {allProviderIds.map((pid) => {
            const config = configs.find(c => c.id === pid);
            const conns = grouped[pid] || [];
            const isExpanded = selectedProviderId === pid;
            const activeCount = conns.filter(c => c.is_active).length;
            const totalCount = conns.length;
            const baseConn = conns[0];
            
            return (
              <div key={pid} className="bg-content1 rounded-2xl border border-default-100 overflow-hidden">
                {/* Header / Summary */}
                <div 
                  className={`flex items-center justify-between p-4 cursor-pointer hover:bg-default-100 transition-colors ${isExpanded ? "bg-primary/5" : ""}`}
                  onClick={() => toggleProvider(pid)}
                >
                  <div className="flex items-center gap-3">
                    <IconChevron expanded={isExpanded} />
                    <div>
                      <div className="font-semibold">{pid}</div>
                      <div className="text-xs text-default-500">
                        {totalCount} {totalCount === 1 ? 'conexão' : 'conexões'} ({activeCount} ativas)
                      </div>
                    </div>
                  </div>
                  
                  <div className="flex items-center gap-4">
                    {config?.load_balance && (
                      <Chip size="sm" variant="flat" color="default" className="hidden sm:flex">
                        {config.load_balance}
                      </Chip>
                    )}
                    <Button 
                      size="sm" 
                      variant="flat" 
                      color="primary"
                      className="opacity-0 group-hover:opacity-100 transition-opacity"
                      onPress={(e) => { e.stopPropagation(); if (baseConn) openNewKey(pid, baseConn.base_url, baseConn.format, baseConn.auth); else openNew(); }}
                      startContent={<IconPlus />}
                    >
                      Add Chave
                    </Button>
                  </div>
                </div>
                
                {/* Expanded Content */}
                {isExpanded && (
                  <div className="border-t border-default-100 p-4 bg-content1">
                    
                    <div className="flex flex-col md:flex-row gap-6 mb-6">
                      <div className="flex-1 bg-content2 p-4 rounded-xl">
                        <div className="text-sm font-semibold mb-3">Balanceamento de Carga</div>
                        <Select
                          selectedKeys={[config?.load_balance || "failover"]}
                          onChange={(e) => updateLoadBalance(pid, e.target.value)}
                          size="sm"
                          isDisabled={savingConfig === pid}
                          className="max-w-xs"
                        >
                          <SelectItem key="failover">Failover (prioriza a primeira chave ativa)</SelectItem>
                          <SelectItem key="round-robin">Round-robin (distribui entre as chaves)</SelectItem>
                        </Select>
                        <p className="text-xs text-default-500 mt-2">
                          {config?.load_balance === "round-robin" 
                            ? "Requisições são distribuídas sequencialmente entre todas as chaves ativas deste provider." 
                            : "Sempre usa a primeira chave da lista; só tenta as outras se a primeira falhar."}
                        </p>
                      </div>
                      
                      <div className="flex-1 bg-content2 p-4 rounded-xl">
                        <div className="flex justify-between items-center mb-3">
                          <div className="text-sm font-semibold">Modelos do Provider</div>
                          <Button size="sm" variant="light" color="primary" onPress={() => syncProviderModels(pid)} isLoading={loadingModels === pid}>
                            Sincronizar
                          </Button>
                        </div>
                        <div className="max-h-[140px] overflow-y-auto pr-2">
                          <ModelsPanel
                            loading={loadingModels === pid}
                            models={modelsCache[pid]}
                            error={modelErrors[pid]}
                          />
                        </div>
                      </div>
                    </div>

                    <div className="mb-2 text-sm font-semibold flex justify-between items-center">
                      Conexões (Chaves API)
                      <Button size="sm" variant="flat" onPress={() => { if (baseConn) openNewKey(pid, baseConn.base_url, baseConn.format, baseConn.auth); else openNew(); }} startContent={<IconPlus />}>Add Chave</Button>
                    </div>
                    {conns.length === 0 ? (
                      <div className="text-sm text-default-400 py-2">Nenhuma conexão cadastrada.</div>
                    ) : (
                      <Table aria-label="connections" removeWrapper className="bg-content2/50 rounded-xl">
                        <TableHeader>
                          <TableColumn>NOME</TableColumn>
                          <TableColumn>URL</TableColumn>
                          <TableColumn>FORMATO</TableColumn>
                          <TableColumn>STATUS</TableColumn>
                          <TableColumn align="end">AÇÕES</TableColumn>
                        </TableHeader>
                        <TableBody items={conns}>
                          {(p) => (
                            <TableRow key={p.id}>
                              <TableCell className="font-medium">{p.name}</TableCell>
                              <TableCell><code className="text-xs text-default-500 truncate max-w-[200px] inline-block">{p.base_url}</code></TableCell>
                              <TableCell><Chip size="sm" variant="flat">{p.format}</Chip></TableCell>
                              <TableCell>
                                <Chip size="sm" variant="flat" color={p.is_active ? "success" : "default"}>
                                  {p.is_active ? "ativo" : "inativo"}
                                </Chip>
                              </TableCell>
                              <TableCell>
                                <div className="flex gap-1 justify-end">
                                  <Button isIconOnly size="sm" variant="light" onPress={() => openEdit(p)} aria-label="editar"><IconPencil /></Button>
                                  <Button isIconOnly size="sm" variant="light" color="danger" onPress={(e) => remove(p.id, e as any)} aria-label="excluir"><IconTrash /></Button>
                                </div>
                              </TableCell>
                            </TableRow>
                          )}
                        </TableBody>
                      </Table>
                    )}
                  </div>
                )}
              </div>
            );
          })}
        </div>
      )}

      <Modal isOpen={isOpen} onClose={onClose} size="lg">
        <ModalContent>
          <ModalHeader>
            {editId ? "Editar conexão" : step === "pick" ? "Escolher provider" : "Configurar conexão"}
          </ModalHeader>
          <ModalBody className="gap-4">
            {!editId && step === "pick" && (
              <>
                <Input
                  isClearable
                  value={search}
                  onValueChange={setSearch}
                  placeholder="Buscar provider..."
                  variant="bordered"
                  className="mb-2"
                  startContent={<IconSearch />}
                  autoFocus
                />
                <div className="grid grid-cols-2 sm:grid-cols-3 gap-2 max-h-80 overflow-y-auto">
                  {filteredCatalog.map((t) => {
                    const isOauth = oauthProviders.includes(t.id);
                    const isPopular = POPULAR.includes(t.id);
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
                        {isPopular && <Chip size="sm" color="primary" variant="flat" className="h-5 text-[10px]">Popular</Chip>}
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
                {!editId && form.template_id === "" && form.provider_id === "" && (
                  <Button size="sm" variant="light" className="self-start" onPress={() => setStep("pick")}>← voltar</Button>
                )}
                <div className="grid grid-cols-2 gap-4">
                  <Input label="Provider ID" placeholder="ex: openai" value={form.provider_id} onValueChange={(v) => setForm({ ...form, provider_id: v })} isDisabled={!!editId || !!form.template_id || (form.provider_id !== "" && step === "form" && form.template_id === "")} />
                  <Input label="Nome (opcional)" placeholder="ex: conta-1" value={form.name} onValueChange={(v) => setForm({ ...form, name: v })} />
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
    return <div className="py-2 flex items-center gap-2 text-sm text-default-500"><Spinner size="sm" /> Sincronizando...</div>;
  }
  if (error) {
    return <div className="py-2 text-sm text-danger">Erro: {error}</div>;
  }
  if (!models || models.length === 0) {
    return <div className="py-2 text-sm text-default-500">Nenhum model no catálogo. Clique em Sincronizar.</div>;
  }
  return (
    <div className="space-y-1">
      {models.map((m) => (
        <div key={m.id} className="flex items-center gap-2 px-2 py-1.5 hover:bg-default-100 rounded-md transition-colors">
          <code className="text-xs flex-1 truncate font-medium">{m.id}</code>
          {m.kind && <Chip size="sm" variant="flat" color="primary" className="h-5 text-[10px]">{m.kind}</Chip>}
          <Chip size="sm" variant="bordered" className="h-5 text-[10px] border-default-200">{m.source}</Chip>
        </div>
      ))}
    </div>
  );
}

function IconPlus() { return <svg className="w-4 h-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><path d="M5 12h14M12 5v14"/></svg>; }
function IconSearch() { return <svg className="w-4 h-4 text-default-400" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><circle cx="11" cy="11" r="8"/><path d="m21 21-4.3-4.3"/></svg>; }
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