import { useEffect, useState, useCallback, useMemo } from "react";
import {
  Table, TableHeader, TableColumn, TableBody, TableRow, TableCell,
  Button, Modal, ModalContent, ModalHeader, ModalBody, ModalFooter,
  Input, Select, SelectItem, Chip, useDisclosure, Spinner,
} from "@heroui/react";
import { api, type Provider, type Connection, type ModelEntry, type ProviderDef } from "../api";

const FORMATS = ["auto", "openai", "anthropic", "gemini", "responses"];
const AUTHS = ["bearer", "x-api-key", "none"];

const emptyProvider = { id: "", name: "", base_url: "", format: "auto", auth: "bearer", description: "" };
const emptyConnection = { name: "", api_key: "" };

export default function Providers() {
  const [providers, setProviders] = useState<Provider[]>([]);
  const [connections, setConnections] = useState<Connection[]>([]);
  const [catalog, setCatalog] = useState<ProviderDef[]>([]);
  const [loading, setLoading] = useState(true);
  
  // Modal for Provider (Endpoint Config)
  const { isOpen: isProviderOpen, onOpen: onProviderOpen, onClose: onProviderClose } = useDisclosure();
  const [providerForm, setProviderForm] = useState<Record<string, string>>(emptyProvider);
  const [providerEditId, setProviderEditId] = useState<string | null>(null);
  const [providerStep, setProviderStep] = useState<"pick" | "form" | "oauth">("pick");
  
  // Modal for Connection (API Key)
  const { isOpen: isConnOpen, onOpen: onConnOpen, onClose: onConnClose } = useDisclosure();
  const [connForm, setConnForm] = useState<Record<string, string>>(emptyConnection);
  const [connEditId, setConnEditId] = useState<string | null>(null);
  const [connProviderId, setConnProviderId] = useState<string>("");

  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const [search, setSearch] = useState("");
  const [savingConfig, setSavingConfig] = useState<string | null>(null);

  // OAuth states
  const [oauthProviders, setOauthProviders] = useState<string[]>([]);
  const [oauthState, setOauthState] = useState("");
  const [oauthCode, setOauthCode] = useState("");
  const [oauthProviderId, setOauthProviderId] = useState("");
  const [oauthAuthURL, setOauthAuthURL] = useState("");

  const POPULAR = ["openai", "anthropic", "openrouter", "gemini", "groq", "deepseek", "mistral", "together", "ollama", "opencode", "deepinfra", "openadapter"];

  const [expandedProviderId, setExpandedProviderId] = useState<string | null>(null);
  const [modelsCache, setModelsCache] = useState<Record<string, ModelEntry[]>>({});
  const [modelErrors, setModelErrors] = useState<Record<string, string>>({});
  const [loadingModels, setLoadingModels] = useState<string | null>(null);

  const loadData = () => {
    setLoading(true);
    Promise.all([api.providers.list(), api.connections.list()])
      .then(([provs, conns]) => {
        setProviders(provs);
        setConnections(conns);
      })
      .catch(() => { setProviders([]); setConnections([]); })
      .finally(() => setLoading(false));
  };

  useEffect(() => { loadData(); }, []);

  // --- PROVIDER ACTIONS ---
  const openNewProvider = () => {
    setProviderForm(emptyProvider);
    setProviderEditId(null);
    setProviderStep("pick");
    setError("");
    setSearch("");
    api.providers.catalog().then(setCatalog).catch(() => setCatalog([]));
    api.oauth.list().then(setOauthProviders).catch(() => setOauthProviders([]));
    onProviderOpen();
  };

  const openEditProvider = (p: Provider, e?: React.MouseEvent) => {
    if (e) e.stopPropagation();
    setProviderForm({
      id: p.id, name: p.name, base_url: p.base_url, format: p.format, auth: p.auth, description: p.description || ""
    });
    setProviderEditId(p.id);
    setProviderStep("form");
    setError("");
    onProviderOpen();
  };

  const pickTemplate = async (t: ProviderDef) => {
    if ((t.category === "oauth" || t.category === "free") && oauthProviders.includes(t.id)) {
      setOauthProviderId(t.id);
      setError("");
      try {
        const res = await api.oauth.start(t.id);
        setOauthState(res.state);
        setOauthAuthURL(res.auth_url);
        setProviderStep("oauth");
        window.open(res.auth_url, "_blank", "noopener,noreferrer");
      } catch (e: any) {
        setError(e?.message ?? "oauth start failed");
      }
      return;
    }
    setProviderForm({
      id: t.id,
      name: t.display.name,
      base_url: t.transport.base_url,
      format: t.transport.format || "openai",
      auth: t.no_auth ? "bearer" : (t.transport.auth || "bearer"),
      description: "",
    });
    setProviderStep("form");
  };

  const completeOAuth = async () => {
    setSaving(true);
    setError("");
    try {
      await api.oauth.complete(oauthProviderId, { state: oauthState, code: oauthCode });
      onProviderClose();
      loadData();
    } catch (e: any) {
      setError(e?.message ?? "oauth complete failed");
    } finally {
      setSaving(false);
    }
  };

  const submitProvider = async () => {
    setSaving(true);
    setError("");
    try {
      const payload = {
        id: providerForm.id,
        name: providerForm.name,
        base_url: providerForm.base_url,
        format: providerForm.format,
        auth: providerForm.auth,
        description: providerForm.description,
      };
      if (providerEditId) {
        await api.providers.update(providerEditId, payload);
      } else {
        await api.providers.create(payload);
      }
      onProviderClose();
      loadData();
      
      // If creating a new provider, automatically prompt to add a key
      if (!providerEditId) {
        openNewConnection(providerForm.id);
        setExpandedProviderId(providerForm.id);
      }
    } catch (e: any) {
      setError(e?.message ?? "falha ao salvar provider");
    } finally {
      setSaving(false);
    }
  };

  const removeProvider = async (id: string, e?: React.MouseEvent) => {
    if (e) e.stopPropagation();
    if (confirm("Remover este provider e todas as suas conexões?")) {
      await api.providers.remove(id);
      if (expandedProviderId === id) setExpandedProviderId(null);
      loadData();
    }
  };

  const updateLoadBalance = async (providerId: string, lb: string) => {
    setSavingConfig(providerId);
    try {
      await api.providers.update(providerId, { load_balance: lb });
      setProviders(prev => prev.map(p => p.id === providerId ? { ...p, load_balance: lb } : p));
    } catch (e: any) {
      // surface error?
    } finally {
      setSavingConfig(null);
    }
  };

  // --- CONNECTION ACTIONS ---
  const openNewConnection = (providerId: string) => {
    setConnProviderId(providerId);
    setConnForm(emptyConnection);
    setConnEditId(null);
    setError("");
    onConnOpen();
  };

  const openEditConnection = (c: Connection, e?: React.MouseEvent) => {
    if (e) e.stopPropagation();
    setConnProviderId(c.provider_id);
    setConnForm({ name: c.name, api_key: "" }); // Never pre-fill API key for security
    setConnEditId(c.id);
    setError("");
    onConnOpen();
  };

  const submitConnection = async () => {
    setSaving(true);
    setError("");
    try {
      const payload = {
        provider_id: connProviderId,
        name: connForm.name,
        api_key: connForm.api_key,
      };
      if (connEditId) {
        await api.connections.update(connEditId, payload);
      } else {
        await api.connections.create(payload);
      }
      onConnClose();
      loadData();
    } catch (e: any) {
      setError(e?.message ?? "falha ao salvar chave");
    } finally {
      setSaving(false);
    }
  };

  const removeConnection = async (id: string, e?: React.MouseEvent) => {
    if (e) e.stopPropagation();
    if (confirm("Remover esta chave API?")) {
      await api.connections.remove(id);
      loadData();
    }
  };

  // --- MODELS ACTIONS ---
  const toggleProviderView = useCallback(async (providerId: string) => {
    if (expandedProviderId === providerId) {
      setExpandedProviderId(null);
      return;
    }
    setExpandedProviderId(providerId);
    if (modelsCache[providerId] || modelErrors[providerId]) return;
    
    setLoadingModels(providerId);
    try {
      const entries = await api.providers.models(providerId);
      setModelsCache((c) => ({ ...c, [providerId]: entries }));
    } catch (e: any) {
      setModelErrors((m) => ({ ...m, [providerId]: e.message }));
    } finally {
      setLoadingModels(null);
    }
  }, [expandedProviderId, modelsCache, modelErrors]);

  const syncProviderModels = async (providerId: string) => {
    setLoadingModels(providerId);
    try {
      const entries = await api.providers.syncModels(providerId);
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

  // --- RENDER HELPERS ---
  const groupedConnections = useMemo(() => {
    const groups: Record<string, Connection[]> = {};
    connections.forEach(c => {
      if (!groups[c.provider_id]) groups[c.provider_id] = [];
      groups[c.provider_id].push(c);
    });
    return groups;
  }, [connections]);

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

  return (
    <div className="space-y-5">
      <div className="flex justify-between items-center">
        <div>
          <h1 className="text-2xl font-bold tracking-tight">Providers</h1>
          <p className="text-sm text-default-500 mt-0.5">{providers.length} providers, {connections.length} chaves ativas</p>
        </div>
        <Button color="primary" variant="bordered" onPress={openNewProvider} startContent={<IconPlus />}>Novo provider</Button>
      </div>
      
      {loading ? (
        <div className="p-10 text-center text-default-500 text-sm bg-content1 rounded-2xl border border-default-100">Carregando...</div>
      ) : providers.length === 0 ? (
        <div className="p-10 text-center text-default-500 text-sm bg-content1 rounded-2xl border border-default-100">
          Nenhum provider configurado. Clique em <strong>Novo provider</strong>.
        </div>
      ) : (
        <div className="space-y-4">
          {providers.map((provider) => {
            const conns = groupedConnections[provider.id] || [];
            const isExpanded = expandedProviderId === provider.id;
            const activeCount = conns.filter(c => c.is_active).length;
            
            return (
              <div key={provider.id} className="bg-content1 rounded-2xl border border-default-100 overflow-hidden">
                {/* Header / Summary */}
                <div 
                  className={`flex items-center justify-between p-4 cursor-pointer hover:bg-default-100 transition-colors ${isExpanded ? "bg-primary/5 border-b border-default-100" : ""}`}
                  onClick={() => toggleProviderView(provider.id)}
                >
                  <div className="flex items-center gap-3">
                    <IconChevron expanded={isExpanded} />
                    <div>
                      <div className="font-semibold flex items-center gap-2">
                        {provider.name || provider.id}
                        {provider.name && <span className="text-xs font-mono text-default-400 font-normal">({provider.id})</span>}
                      </div>
                      <div className="text-xs text-default-500 flex items-center gap-2 mt-0.5">
                        <code className="text-default-400">{provider.base_url}</code>
                        <span>•</span>
                        <span>{conns.length} {conns.length === 1 ? 'chave' : 'chaves'} ({activeCount} ativas)</span>
                      </div>
                    </div>
                  </div>
                  
                  <div className="flex items-center gap-3">
                    <Chip size="sm" variant="flat" color="primary">{provider.format}</Chip>
                    <div className="flex gap-1" onClick={(e) => e.stopPropagation()}>
                      <Button isIconOnly size="sm" variant="light" onPress={(e) => openEditProvider(provider, e)} aria-label="editar"><IconPencil /></Button>
                      <Button isIconOnly size="sm" variant="light" color="danger" onPress={(e) => removeProvider(provider.id, e)} aria-label="excluir"><IconTrash /></Button>
                    </div>
                  </div>
                </div>
                
                {/* Expanded Content */}
                {isExpanded && (
                  <div className="p-4 bg-content1">
                    <div className="flex flex-col lg:flex-row gap-6 mb-6">
                      
                      {/* Left side: Load Balance */}
                      <div className="flex-1 bg-content2 p-4 rounded-xl border border-default-100">
                        <div className="text-sm font-semibold mb-3">Balanceamento de Carga</div>
                        <Select
                          selectedKeys={[provider.load_balance || "failover"]}
                          onChange={(e) => updateLoadBalance(provider.id, e.target.value)}
                          size="sm"
                          isDisabled={savingConfig === provider.id}
                        >
                          <SelectItem key="failover">Failover (prioriza a primeira chave ativa)</SelectItem>
                          <SelectItem key="round-robin">Round-robin (distribui entre as chaves)</SelectItem>
                        </Select>
                        <p className="text-[11px] text-default-500 mt-2 leading-relaxed">
                          {provider.load_balance === "round-robin" 
                            ? "Requisições são distribuídas rotativamente entre todas as chaves ativas." 
                            : "Usa sempre a primeira chave ativa da lista; cai para a próxima apenas em falha."}
                        </p>
                      </div>
                      
                      {/* Right side: Models */}
                      <div className="flex-[2] bg-content2 p-4 rounded-xl border border-default-100">
                        <div className="flex justify-between items-center mb-3">
                          <div className="text-sm font-semibold">Modelos do Provider</div>
                          <Button size="sm" variant="flat" color="primary" onPress={() => syncProviderModels(provider.id)} isLoading={loadingModels === provider.id}>
                            Sincronizar
                          </Button>
                        </div>
                        <div className="max-h-[120px] overflow-y-auto pr-2 custom-scrollbar">
                          <ModelsPanel
                            loading={loadingModels === provider.id}
                            models={modelsCache[provider.id]}
                            error={modelErrors[provider.id]}
                          />
                        </div>
                      </div>
                    </div>

                    {/* Connections Table */}
                    <div className="mb-3 text-sm font-semibold flex justify-between items-center">
                      Conexões / Chaves API
                      <Button size="sm" variant="flat" color="primary" onPress={() => openNewConnection(provider.id)} startContent={<IconPlus />}>
                        Adicionar Chave
                      </Button>
                    </div>
                    
                    {conns.length === 0 ? (
                      <div className="text-sm text-default-400 py-4 text-center border border-dashed border-default-200 rounded-xl">
                        Nenhuma chave configurada para este provider.
                      </div>
                    ) : (
                      <div className="border border-default-100 rounded-xl overflow-hidden">
                        <Table aria-label="connections" removeWrapper className="bg-content2">
                          <TableHeader>
                            <TableColumn>NOME</TableColumn>
                            <TableColumn>ID</TableColumn>
                            <TableColumn>STATUS</TableColumn>
                            <TableColumn align="end">AÇÕES</TableColumn>
                          </TableHeader>
                          <TableBody items={conns}>
                            {(c) => (
                              <TableRow key={c.id}>
                                <TableCell className="font-medium">{c.name || "Padrão"}</TableCell>
                                <TableCell><code className="text-[11px] text-default-400 font-mono">{c.id}</code></TableCell>
                                <TableCell>
                                  <Chip size="sm" variant="flat" color={c.is_active ? "success" : "default"}>
                                    {c.is_active ? "ativa" : "inativa"}
                                  </Chip>
                                </TableCell>
                                <TableCell>
                                  <div className="flex gap-1 justify-end">
                                    <Button isIconOnly size="sm" variant="light" onPress={(e) => openEditConnection(c, e)} aria-label="editar"><IconPencil /></Button>
                                    <Button isIconOnly size="sm" variant="light" color="danger" onPress={(e) => removeConnection(c.id, e)} aria-label="excluir"><IconTrash /></Button>
                                  </div>
                                </TableCell>
                              </TableRow>
                            )}
                          </TableBody>
                        </Table>
                      </div>
                    )}
                  </div>
                )}
              </div>
            );
          })}
        </div>
      )}

      {/* MODAL: PROVIDER CONFIG */}
      <Modal isOpen={isProviderOpen} onClose={onProviderClose} size="lg">
        <ModalContent>
          <ModalHeader>
            {providerEditId ? "Editar Endpoint (Provider)" : providerStep === "pick" ? "Escolher Provider" : "Configurar Provider"}
          </ModalHeader>
          <ModalBody className="gap-4">
            {!providerEditId && providerStep === "pick" && (
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
                <div className="grid grid-cols-2 sm:grid-cols-3 gap-2 max-h-80 overflow-y-auto custom-scrollbar pr-1">
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
                      </div>
                    </button>
                    );
                  })}
                </div>
                <Button variant="flat" onPress={() => { setProviderForm(emptyProvider); setProviderStep("form"); }}>Custom / OpenAI-compatible</Button>
              </>
            )}

            {providerStep === "oauth" && (
              <>
                <Button size="sm" variant="light" className="self-start" onPress={() => setProviderStep("pick")}>← voltar</Button>
                <div className="bg-primary/10 rounded-lg p-3 text-sm space-y-1">
                  <p className="font-medium">Conectando <strong>{oauthProviderId}</strong></p>
                  <p className="text-default-600">
                    Siga as instruções na janela do navegador, copie a URL final e cole abaixo.
                  </p>
                </div>
                {oauthAuthURL && (
                  <a href={oauthAuthURL} target="_blank" rel="noreferrer" className="text-sm text-primary underline break-all">
                    Abrir login novamente
                  </a>
                )}
                <Input
                  label="URL de callback ou Code"
                  value={oauthCode}
                  onValueChange={setOauthCode}
                />
              </>
            )}

            {(providerEditId || providerStep === "form") && (
              <>
                {!providerEditId && (
                  <Button size="sm" variant="light" className="self-start" onPress={() => setProviderStep("pick")}>← voltar</Button>
                )}
                <div className="grid grid-cols-2 gap-4">
                  <Input label="ID do Provider" placeholder="ex: openai" value={providerForm.id} onValueChange={(v) => setProviderForm({ ...providerForm, id: v })} isDisabled={!!providerEditId} />
                  <Input label="Nome Amigável" placeholder="ex: OpenAI" value={providerForm.name} onValueChange={(v) => setProviderForm({ ...providerForm, name: v })} />
                </div>
                <Input label="Base URL" placeholder="https://api.openai.com/v1" value={providerForm.base_url} onValueChange={(v) => setProviderForm({ ...providerForm, base_url: v })} />
                <div className="grid grid-cols-2 gap-4">
                  <Select label="Formato API" selectedKeys={[providerForm.format]} onChange={(e) => setProviderForm({ ...providerForm, format: e.target.value })}>
                    {FORMATS.map((f) => <SelectItem key={f}>{f}</SelectItem>)}
                  </Select>
                  <Select label="Autenticação" selectedKeys={[providerForm.auth]} onChange={(e) => setProviderForm({ ...providerForm, auth: e.target.value })}>
                    {AUTHS.map((a) => <SelectItem key={a}>{a}</SelectItem>)}
                  </Select>
                </div>
              </>
            )}
            
            {error && <p className="text-sm text-danger mt-2">{error}</p>}
          </ModalBody>
          <ModalFooter>
            {(providerEditId || providerStep === "form") && (
              <Button color="primary" onPress={submitProvider} isLoading={saving}>Salvar Provider</Button>
            )}
            {providerStep === "oauth" && (
              <Button color="primary" onPress={completeOAuth} isLoading={saving} isDisabled={!oauthCode.trim()}>Conectar</Button>
            )}
          </ModalFooter>
        </ModalContent>
      </Modal>

      {/* MODAL: CONNECTION (API KEY) */}
      <Modal isOpen={isConnOpen} onClose={onConnClose} size="md">
        <ModalContent>
          <ModalHeader>
            {connEditId ? "Editar Chave" : "Adicionar Chave"} <span className="text-default-400 text-sm ml-2 font-normal">({connProviderId})</span>
          </ModalHeader>
          <ModalBody className="gap-4">
            <Input label="Nome da Chave" placeholder="ex: Produção, Conta Secundária" value={connForm.name} onValueChange={(v) => setConnForm({ ...connForm, name: v })} />
            <Input 
              label="API Key" 
              type="password" 
              placeholder={connEditId ? "Deixe em branco para manter a atual" : "sk-..."} 
              value={connForm.api_key} 
              onValueChange={(v) => setConnForm({ ...connForm, api_key: v })} 
            />
            {error && <p className="text-sm text-danger">{error}</p>}
          </ModalBody>
          <ModalFooter>
            <Button color="primary" onPress={submitConnection} isLoading={saving}>Salvar Chave</Button>
          </ModalFooter>
        </ModalContent>
      </Modal>

    </div>
  );
}

function ModelsPanel({ loading, models, error }: { loading: boolean; models?: ModelEntry[]; error?: string }) {
  if (loading) return <div className="py-2 flex items-center gap-2 text-sm text-default-500"><Spinner size="sm" /> Sincronizando...</div>;
  if (error) return <div className="py-2 text-sm text-danger">Erro: {error}</div>;
  if (!models || models.length === 0) return <div className="py-2 text-sm text-default-500">Nenhum model sincronizado. Clique em Sincronizar.</div>;
  
  return (
    <div className="flex flex-wrap gap-2">
      {models.map((m) => (
        <Chip key={m.id} size="sm" variant="flat" color="primary" className="text-[11px] font-mono">
          {m.id}
        </Chip>
      ))}
    </div>
  );
}

function IconPlus() { return <svg className="w-4 h-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><path d="M5 12h14M12 5v14"/></svg>; }
function IconSearch() { return <svg className="w-4 h-4 text-default-400" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><circle cx="11" cy="11" r="8"/><path d="m21 21-4.3-4.3"/></svg>; }
function IconPencil() { return <svg className="w-4 h-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><path d="M12 20h9"/><path d="M16.5 3.5a2.12 2.12 0 0 1 3 3L7 19l-4 1 1-4Z"/></svg>; }
function IconTrash() { return <svg className="w-4 h-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><polyline points="3 6 5 6 21 6"/><path d="M19 6l-1.5 14a2 2 0 0 1-2 2H8.5a2 2 0 0 1-2-2L5 6"/><path d="M10 11v6M14 11v6"/></svg>; }
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