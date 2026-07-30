import { useEffect, useState, useCallback, useMemo } from "react";
import {
  Table, Button, Modal, Input, Select, ListBox, Chip, Spinner, Popover, TextField, Label,
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

  const [isProviderOpen, setProviderOpen] = useState(false);
  const [providerForm, setProviderForm] = useState<Record<string, string>>(emptyProvider);
  const [providerEditId, setProviderEditId] = useState<string | null>(null);
  const [providerStep, setProviderStep] = useState<"pick" | "form" | "oauth">("pick");

  const [isConnOpen, setConnOpen] = useState(false);
  const [connForm, setConnForm] = useState<Record<string, string>>(emptyConnection);
  const [connEditId, setConnEditId] = useState<string | null>(null);
  const [connProviderId, setConnProviderId] = useState<string>("");

  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const [search, setSearch] = useState("");
  const [savingConfig, setSavingConfig] = useState<string | null>(null);

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

  const openNewProvider = () => {
    setProviderForm(emptyProvider);
    setProviderEditId(null);
    setProviderStep("pick");
    setError("");
    setSearch("");
    api.providers.catalog().then(setCatalog).catch(() => setCatalog([]));
    api.oauth.list().then(setOauthProviders).catch(() => setOauthProviders([]));
    setProviderOpen(true);
  };

  const openEditProvider = (p: Provider) => {
    setProviderForm({
      id: p.id, name: p.name, base_url: p.base_url, format: p.format, auth: p.auth, description: p.description || ""
    });
    setProviderEditId(p.id);
    setProviderStep("form");
    setError("");
    setProviderOpen(true);
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
      setProviderOpen(false);
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
      setProviderOpen(false);
      loadData();

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

  const removeProvider = async (id: string) => {
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

  const openNewConnection = (providerId: string) => {
    setConnProviderId(providerId);
    setConnForm(emptyConnection);
    setConnEditId(null);
    setError("");
    setConnOpen(true);
  };

  const openEditConnection = (c: Connection) => {
    setConnProviderId(c.provider_id);
    setConnForm({ name: c.name, api_key: "" });
    setConnEditId(c.id);
    setError("");
    setConnOpen(true);
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
      setConnOpen(false);
      loadData();
    } catch (e: any) {
      setError(e?.message ?? "falha ao salvar chave");
    } finally {
      setSaving(false);
    }
  };

  const removeConnection = async (id: string) => {
    if (confirm("Remover esta chave API?")) {
      await api.connections.remove(id);
      loadData();
    }
  };

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
          <p className="text-sm text-muted mt-0.5">{providers.length} providers, {connections.length} chaves ativas</p>
        </div>
        <Button variant="outline" onPress={openNewProvider}><IconPlus /> Novo provider</Button>
      </div>

      {loading ? (
        <div className="p-10 text-center text-muted text-sm bg-surface rounded-2xl border border-border">Carregando...</div>
      ) : providers.length === 0 ? (
        <div className="p-10 text-center text-muted text-sm bg-surface rounded-2xl border border-border">
          Nenhum provider configurado. Clique em <strong>Novo provider</strong>.
        </div>
      ) : (
        <div className="space-y-4">
          {providers.map((provider) => {
            const conns = groupedConnections[provider.id] || [];
            const isExpanded = expandedProviderId === provider.id;
            const activeCount = conns.filter(c => c.is_active).length;

            return (
              <div key={provider.id} className="bg-surface rounded-2xl border border-border overflow-hidden">
                <div
                  className={`flex items-center justify-between p-4 cursor-pointer hover:bg-default-soft transition-colors ${isExpanded ? "bg-accent/5 border-b border-border" : ""}`}
                  onClick={() => toggleProviderView(provider.id)}
                >
                  <div className="flex items-center gap-3">
                    <IconChevron expanded={isExpanded} />
                    <div>
                      <div className="font-semibold flex items-center gap-2">
                        {provider.name || provider.id}
                        {provider.name && <span className="text-xs font-mono text-muted font-normal">({provider.id})</span>}
                      </div>
                      <div className="text-xs text-muted flex items-center gap-2 mt-0.5">
                        <code className="text-muted">{provider.base_url}</code>
                        <span>•</span>
                        <span>{conns.length} {conns.length === 1 ? 'chave' : 'chaves'} ({activeCount} ativas)</span>
                      </div>
                    </div>
                  </div>

                  <div className="flex items-center gap-3">
                    <Chip size="sm" variant="soft" color="accent">{provider.format}</Chip>
                    <div className="flex gap-1" onClick={(e) => e.stopPropagation()}>
                      <Button isIconOnly size="sm" variant="ghost" onPress={() => openEditProvider(provider)} aria-label="editar"><IconPencil /></Button>
                      <Button isIconOnly size="sm" variant="ghost" className="text-danger" onPress={() => removeProvider(provider.id)} aria-label="excluir"><IconTrash /></Button>
                    </div>
                  </div>
                </div>

                {isExpanded && (
                  <div className="p-4 bg-surface">
                    <div className="flex items-center gap-3 mb-4 pb-3 border-b border-border">
                      <span className="text-sm font-medium shrink-0">Balanceamento:</span>
                      <Select
                        aria-label="Balanceamento"
                        selectedKey={provider.load_balance || "failover"}
                        onSelectionChange={(k) => updateLoadBalance(provider.id, (k as string) ?? "failover")}
                        className="max-w-[260px]"
                        isDisabled={savingConfig === provider.id}
                      >
                        <Select.Trigger><Select.Value /></Select.Trigger>
                        <Select.Popover>
                          <ListBox>
                            <ListBox.Item id="failover" textValue="Failover">Failover (prioriza 1ª chave ativa)</ListBox.Item>
                            <ListBox.Item id="round-robin" textValue="Round-robin">Round-robin (distribui entre chaves)</ListBox.Item>
                          </ListBox>
                        </Select.Popover>
                      </Select>
                      {savingConfig === provider.id && <Spinner size="sm" />}
                    </div>

                    <div className="mb-4">
                      <div className="flex justify-between items-center mb-2">
                        <div className="text-sm font-semibold">Modelos do Provider</div>
                        <Button size="sm" variant="outline" onPress={() => syncProviderModels(provider.id)} isDisabled={loadingModels === provider.id}>
                          Sincronizar
                        </Button>
                      </div>
                      <div className="max-h-[160px] overflow-y-auto pr-2 custom-scrollbar">
                        <ModelsPanel
                          providerId={provider.id}
                          loading={loadingModels === provider.id}
                          models={modelsCache[provider.id]}
                          error={modelErrors[provider.id]}
                          onAdd={(model) => {
                            setModelsCache((c) => ({
                              ...c,
                              [provider.id]: [...(c[provider.id] || []), model]
                            }));
                          }}
                          onRemove={(modelId) => {
                            setModelsCache((c) => ({
                              ...c,
                              [provider.id]: (c[provider.id] || []).filter(m => m.id !== modelId)
                            }));
                          }}
                          onToggle={(modelId, active) => {
                            setModelsCache((c) => ({
                              ...c,
                              [provider.id]: (c[provider.id] || []).map(m =>
                                m.id === modelId ? { ...m, is_active: active } : m
                              )
                            }));
                          }}
                        />
                      </div>
                    </div>

                    <div className="mb-3 text-sm font-semibold flex justify-between items-center">
                      Conexões / Chaves API
                      <Button size="sm" variant="outline" onPress={() => openNewConnection(provider.id)}><IconPlus /> Adicionar Chave</Button>
                    </div>

                    {conns.length === 0 ? (
                      <div className="text-sm text-muted py-4 text-center border border-dashed border-border rounded-xl">
                        Nenhuma chave configurada para este provider.
                      </div>
                    ) : (
                      <div className="border border-border rounded-xl overflow-hidden">
                        <Table>
                          <Table.ScrollContainer>
                            <Table.Content aria-label="connections" className="bg-surface-secondary min-w-[420px]">
                              <Table.Header>
                                <Table.Column isRowHeader id="name">Nome</Table.Column>
                                <Table.Column id="id">ID</Table.Column>
                                <Table.Column id="status">Status</Table.Column>
                                <Table.Column id="actions">Ações</Table.Column>
                              </Table.Header>
                              <Table.Body items={conns}>
                                {(c) => (
                                  <Table.Row key={c.id} id={c.id}>
                                    <Table.Cell className="font-medium">{c.name || "Padrão"}</Table.Cell>
                                    <Table.Cell><code className="text-[11px] text-muted font-mono">{c.id}</code></Table.Cell>
                                    <Table.Cell>
                                      <Chip size="sm" variant="soft" color={c.is_active ? "success" : "default"}>
                                        {c.is_active ? "ativa" : "inativa"}
                                      </Chip>
                                    </Table.Cell>
                                    <Table.Cell>
                                      <div className="flex gap-1 justify-end" onClick={(e) => e.stopPropagation()}>
                                        <Button isIconOnly size="sm" variant="ghost" onPress={() => openEditConnection(c)} aria-label="editar"><IconPencil /></Button>
                                        <Button isIconOnly size="sm" variant="ghost" className="text-danger" onPress={() => removeConnection(c.id)} aria-label="excluir"><IconTrash /></Button>
                                      </div>
                                    </Table.Cell>
                                  </Table.Row>
                                )}
                              </Table.Body>
                            </Table.Content>
                          </Table.ScrollContainer>
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

      <Modal isOpen={isProviderOpen} onOpenChange={setProviderOpen}>
        <Modal.Backdrop>
          <Modal.Container>
            <Modal.Dialog className="max-w-xl">
              <Modal.Header>
                <Modal.Heading>
                  {providerEditId ? "Editar Endpoint (Provider)" : providerStep === "pick" ? "Escolher Provider" : "Configurar Provider"}
                </Modal.Heading>
              </Modal.Header>
              <Modal.Body className="gap-4">
                {!providerEditId && providerStep === "pick" && (
                  <>
                    <div className="relative mb-2">
                      <span className="absolute left-3 top-1/2 -translate-y-1/2 pointer-events-none"><IconSearch /></span>
                      <Input
                        value={search}
                        onChange={(e) => setSearch(e.target.value)}
                        placeholder="Buscar provider..."
                        variant="secondary"
                        className="pl-9"
                        autoFocus
                        aria-label="Buscar provider"
                      />
                    </div>
                    <div className="grid grid-cols-2 sm:grid-cols-3 gap-2 max-h-80 overflow-y-auto custom-scrollbar pr-1">
                      {filteredCatalog.map((t) => {
                        const isOauth = oauthProviders.includes(t.id);
                        const isPopular = POPULAR.includes(t.id);
                        return (
                          <button
                            key={t.id}
                            type="button"
                            onClick={() => pickTemplate(t)}
                            className="text-left rounded-xl border border-border p-3 hover:border-accent/50 hover:bg-background transition-colors"
                          >
                            <div className="flex items-center gap-2 mb-1">
                              <span className="w-2.5 h-2.5 rounded-full shrink-0" style={{ background: t.display.color || "#888" }} />
                              <span className="font-medium text-sm truncate">{t.display.name}</span>
                            </div>
                            <p className="text-[11px] text-muted font-mono truncate">{t.id}</p>
                            <div className="flex flex-wrap gap-1 mt-2">
                              {isOauth && <Chip size="sm" color="default" variant="soft" className="h-5 text-[10px]">OAuth</Chip>}
                              {isPopular && <Chip size="sm" color="accent" variant="soft" className="h-5 text-[10px]">Popular</Chip>}
                            </div>
                          </button>
                        );
                      })}
                    </div>
                    <Button variant="secondary" onPress={() => { setProviderForm(emptyProvider); setProviderStep("form"); }}>Custom / OpenAI-compatible</Button>
                  </>
                )}

                {providerStep === "oauth" && (
                  <>
                    <Button size="sm" variant="ghost" className="self-start" onPress={() => setProviderStep("pick")}>← voltar</Button>
                    <div className="bg-accent/10 rounded-lg p-3 text-sm space-y-1">
                      <p className="font-medium">Conectando <strong>{oauthProviderId}</strong></p>
                      <p className="text-foreground/80">
                        Siga as instruções na janela do navegador, copie a URL final e cole abaixo.
                      </p>
                    </div>
                    {oauthAuthURL && (
                      <a href={oauthAuthURL} target="_blank" rel="noreferrer" className="text-sm text-accent underline break-all">
                        Abrir login novamente
                      </a>
                    )}
                    <TextField value={oauthCode} onChange={setOauthCode}>
                      <Label>URL de callback ou Code</Label>
                      <Input />
                    </TextField>
                  </>
                )}

                {(providerEditId || providerStep === "form") && (
                  <>
                    {!providerEditId && (
                      <Button size="sm" variant="ghost" className="self-start" onPress={() => setProviderStep("pick")}>← voltar</Button>
                    )}
                    <div className="grid grid-cols-2 gap-4">
                      <TextField value={providerForm.id} onChange={(v) => setProviderForm({ ...providerForm, id: v })} isDisabled={!!providerEditId}>
                        <Label>ID do Provider</Label>
                        <Input placeholder="ex: openai" />
                      </TextField>
                      <TextField value={providerForm.name} onChange={(v) => setProviderForm({ ...providerForm, name: v })}>
                        <Label>Nome Amigável</Label>
                        <Input placeholder="ex: OpenAI" />
                      </TextField>
                    </div>
                    <TextField value={providerForm.base_url} onChange={(v) => setProviderForm({ ...providerForm, base_url: v })}>
                      <Label>Base URL</Label>
                      <Input placeholder="https://api.openai.com/v1" />
                    </TextField>
                    <div className="grid grid-cols-2 gap-4">
                      <div className="flex flex-col gap-1">
                        <Label>Formato API</Label>
                        <Select aria-label="Formato API" selectedKey={providerForm.format} onSelectionChange={(k) => setProviderForm({ ...providerForm, format: (k as string) ?? "auto" })}>
                          <Select.Trigger><Select.Value /></Select.Trigger>
                          <Select.Popover>
                            <ListBox>{FORMATS.map((f) => <ListBox.Item key={f} id={f}>{f}</ListBox.Item>)}</ListBox>
                          </Select.Popover>
                        </Select>
                      </div>
                      <div className="flex flex-col gap-1">
                        <Label>Autenticação</Label>
                        <Select aria-label="Autenticação" selectedKey={providerForm.auth} onSelectionChange={(k) => setProviderForm({ ...providerForm, auth: (k as string) ?? "bearer" })}>
                          <Select.Trigger><Select.Value /></Select.Trigger>
                          <Select.Popover>
                            <ListBox>{AUTHS.map((a) => <ListBox.Item key={a} id={a}>{a}</ListBox.Item>)}</ListBox>
                          </Select.Popover>
                        </Select>
                      </div>
                    </div>
                  </>
                )}

                {error && <p className="text-sm text-danger mt-2">{error}</p>}
              </Modal.Body>
              <Modal.Footer>
                {(providerEditId || providerStep === "form") && (
                  <Button variant="primary" onPress={submitProvider} isDisabled={saving}>Salvar Provider</Button>
                )}
                {providerStep === "oauth" && (
                  <Button variant="primary" onPress={completeOAuth} isDisabled={saving || !oauthCode.trim()}>Conectar</Button>
                )}
              </Modal.Footer>
            </Modal.Dialog>
          </Modal.Container>
        </Modal.Backdrop>
      </Modal>

      <Modal isOpen={isConnOpen} onOpenChange={setConnOpen}>
        <Modal.Backdrop>
          <Modal.Container>
            <Modal.Dialog>
              <Modal.Header>
                <Modal.Heading>
                  {connEditId ? "Editar Chave" : "Adicionar Chave"} <span className="text-muted text-sm ml-2 font-normal">({connProviderId})</span>
                </Modal.Heading>
              </Modal.Header>
              <Modal.Body className="gap-4">
                <TextField value={connForm.name} onChange={(v) => setConnForm({ ...connForm, name: v })}>
                  <Label>Nome da Chave</Label>
                  <Input placeholder="ex: Produção, Conta Secundária" />
                </TextField>
                <TextField value={connForm.api_key} onChange={(v) => setConnForm({ ...connForm, api_key: v })}>
                  <Label>API Key</Label>
                  <Input type="password" placeholder={connEditId ? "Deixe em branco para manter a atual" : "sk-..."} />
                </TextField>
                {error && <p className="text-sm text-danger">{error}</p>}
              </Modal.Body>
              <Modal.Footer>
                <Button variant="primary" onPress={submitConnection} isDisabled={saving}>Salvar Chave</Button>
              </Modal.Footer>
            </Modal.Dialog>
          </Modal.Container>
        </Modal.Backdrop>
      </Modal>
    </div>
  );
}

function ModelsPanel({ providerId, loading, models, error, onAdd, onRemove, onToggle }: {
  providerId: string;
  loading: boolean;
  models?: ModelEntry[];
  error?: string;
  onAdd?: (model: ModelEntry) => void;
  onRemove?: (modelId: string) => void;
  onToggle?: (modelId: string, active: boolean) => void;
}) {
  const [adding, setAdding] = useState(false);
  const [newModel, setNewModel] = useState("");
  const [saving, setSaving] = useState(false);

  const handleAdd = async () => {
    const val = newModel.trim();
    if (!val) {
      setAdding(false);
      return;
    }
    setSaving(true);
    try {
      const created = await api.providers.addModel(providerId, { model_id: val });
      if (onAdd) onAdd(created);
      setNewModel("");
      setAdding(false);
    } catch (err: any) {
      alert(`Erro ao adicionar modelo: ${err.message || err}`);
    } finally {
      setSaving(false);
    }
  };

  const handleToggle = async (m: ModelEntry) => {
    try {
      await api.models.update(m.id, { is_active: !m.is_active });
      if (onToggle) onToggle(m.id, !m.is_active);
    } catch (err: any) {
      alert(`Erro: ${err.message || err}`);
    }
  };

  const handleRemove = async (m: ModelEntry) => {
    if (confirm(`Remover o modelo "${m.id}"?`)) {
      try {
        await api.models.remove(m.id);
        if (onRemove) onRemove(m.id);
      } catch (err: any) {
        alert(`Erro: ${err.message || err}`);
      }
    }
  };

  if (loading) return <div className="py-2 flex items-center gap-2 text-sm text-muted"><Spinner size="sm" /> Sincronizando...</div>;
  if (error) return <div className="py-2 text-sm text-danger">Erro: {error}</div>;

  return (
    <div className="flex flex-wrap gap-2 items-center">
      {adding ? (
        <form onSubmit={(e) => { e.preventDefault(); handleAdd(); }} className="flex items-center gap-1">
          <Input
            autoFocus
            className="min-w-[120px] text-[11px] font-mono"
            placeholder="nome do modelo"
            value={newModel}
            onChange={(e) => setNewModel(e.target.value)}
            disabled={saving}
            onBlur={() => !saving && handleAdd()}
            aria-label="Novo modelo"
          />
        </form>
      ) : (
        <button
          type="button"
          onClick={() => setAdding(true)}
          className="inline-flex items-center gap-1 rounded-full border border-dashed border-accent px-2.5 py-0.5 text-[11px] font-mono text-accent hover:bg-accent/10 transition-colors"
        >
          <span className="font-bold">+</span> adicionar
        </button>
      )}

      {models?.map((m) => (
        <Popover key={m.id}>
          <Popover.Trigger>
            <button
              type="button"
              className={`inline-flex items-center rounded-full px-2.5 py-0.5 text-[11px] font-mono cursor-pointer hover:opacity-80 transition-opacity ${m.is_active ? "bg-accent/15 text-accent" : "bg-default-soft text-muted"}`}
            >
              {m.is_active ? "" : <span className="text-muted mr-0.5">○</span>}
              {m.model_id || m.id}
            </button>
          </Popover.Trigger>
          <Popover.Content placement="bottom" className="p-1">
            <div className="flex flex-col gap-1 min-w-[140px]">
              <div className="text-[11px] font-mono text-muted px-2 pt-1 pb-1 break-all">{m.id}</div>
              <Button size="sm" variant="ghost" onPress={() => handleToggle(m)} className="justify-start">
                {m.is_active ? <IconEyeOff /> : <IconEye />}
                {m.is_active ? "Inativar" : "Ativar"}
              </Button>
              <Button size="sm" variant="ghost" className="justify-start text-danger" onPress={() => handleRemove(m)}>
                <IconTrash /> Remover
              </Button>
            </div>
          </Popover.Content>
        </Popover>
      ))}
      {models && models.length === 0 && !adding && (
        <span className="text-sm text-muted">Nenhum modelo sincronizado.</span>
      )}
    </div>
  );
}

function IconPlus() { return <svg className="w-4 h-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><path d="M5 12h14M12 5v14"/></svg>; }
function IconSearch() { return <svg className="w-4 h-4 text-muted" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><circle cx="11" cy="11" r="8"/><path d="m21 21-4.3-4.3"/></svg>; }
function IconPencil() { return <svg className="w-4 h-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><path d="M12 20h9"/><path d="M16.5 3.5a2.12 2.12 0 0 1 3 3L7 19l-4 1 1-4Z"/></svg>; }
function IconTrash() { return <svg className="w-4 h-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><polyline points="3 6 5 6 21 6"/><path d="M19 6l-1.5 14a2 2 0 0 1-2 2H8.5a2 2 0 0 1-2-2L5 6"/><path d="M10 11v6M14 11v6"/></svg>; }
function IconChevron({ expanded }: { expanded: boolean }) {
  return (
    <svg
      className={`w-4 h-4 text-muted transition-transform ${expanded ? "rotate-90" : ""}`}
      viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"
    >
      <polyline points="9 18 15 12 9 6" />
    </svg>
  );
}
function IconEye() { return <svg className="w-4 h-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><path d="M2 12s3-7 10-7 10 7 10 7-3 7-10 7-10-7-10-7Z"/><circle cx="12" cy="12" r="3"/></svg>; }
function IconEyeOff() { return <svg className="w-4 h-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><path d="M9.88 9.88a3 3 0 1 0 4.24 4.24"/><path d="M10.73 5.08A10.43 10.43 0 0 1 12 5c7 0 10 7 10 7a13.16 13.16 0 0 1-1.67 2.68"/><path d="M6.61 6.61A13.526 13.526 0 0 0 2 12s3 7 10 7a9.74 9.74 0 0 0 5.39-1.61"/><line x1="2" y1="2" x2="22" y2="22"/></svg>; }
