import { useEffect, useState, useCallback, useMemo } from "react";
import {
  Table, Button, Modal, Input, Select, ListBox, Chip, Spinner, Popover, TextField, Label,
  AlertDialog, toast,
} from "@heroui/react";
import { useTranslation } from "react-i18next";
import { api, type Provider, type Connection, type ModelEntry, type ProviderDef } from "../api";
import { IconPlus, IconSearch, IconPencil, IconTrash, IconChevron, IconEye, IconEyeOff } from "../icons";

const FORMATS = ["auto", "openai", "anthropic", "gemini", "responses"];
const AUTHS = ["bearer", "x-api-key", "none"];

const emptyProvider = { id: "", name: "", base_url: "", format: "auto", auth: "bearer", description: "" };
const emptyConnection = { name: "", api_key: "" };

export default function Providers() {
  const { t } = useTranslation();
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

  const [confirmProviderId, setConfirmProviderId] = useState<string | null>(null);
  const [confirmConnId, setConfirmConnId] = useState<string | null>(null);

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

  const pickTemplate = async (def: ProviderDef) => {
    if ((def.category === "oauth" || def.category === "free") && oauthProviders.includes(def.id)) {
      setOauthProviderId(def.id);
      setError("");
      try {
        const res = await api.oauth.start(def.id);
        setOauthState(res.state);
        setOauthAuthURL(res.auth_url);
        setProviderStep("oauth");
        window.open(res.auth_url, "_blank", "noopener,noreferrer");
      } catch (e: any) {
        setError(e?.message ?? t("providers.oauthStartFailed"));
      }
      return;
    }
    setProviderForm({
      id: def.id,
      name: def.display.name,
      base_url: def.transport.base_url,
      format: def.transport.format || "openai",
      auth: def.no_auth ? "bearer" : (def.transport.auth || "bearer"),
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
      setError(e?.message ?? t("providers.oauthCompleteFailed"));
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
      setError(e?.message ?? t("providers.saveProviderFailed"));
    } finally {
      setSaving(false);
    }
  };

  const removeProvider = async (id: string) => {
    await api.providers.remove(id);
    if (expandedProviderId === id) setExpandedProviderId(null);
    loadData();
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
      setError(e?.message ?? t("providers.saveKeyFailed"));
    } finally {
      setSaving(false);
    }
  };

  const removeConnection = async (id: string) => {
    await api.connections.remove(id);
    loadData();
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
          <h1 className="text-2xl font-bold tracking-tight">{t("providers.title")}</h1>
          <p className="text-sm text-muted mt-0.5">{t("providers.subtitle", { providers: providers.length, connections: connections.length })}</p>
        </div>
        <Button variant="outline" onPress={openNewProvider}><IconPlus className="w-4 h-4" /> {t("providers.new")}</Button>
      </div>

      {loading ? (
        <div className="p-10 text-center text-muted text-sm bg-surface rounded-2xl border border-border">{t("providers.loading")}</div>
      ) : providers.length === 0 ? (
        <div className="p-10 text-center text-muted text-sm bg-surface rounded-2xl border border-border">
          {t("providers.empty")} <strong>{t("providers.new")}</strong>.
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
                    <IconChevron className={`w-4 h-4 text-muted transition-transform ${isExpanded ? "rotate-90" : ""}`} />
                    <div>
                      <div className="font-semibold flex items-center gap-2">
                        {provider.name || provider.id}
                        {provider.name && <span className="text-xs font-mono text-muted font-normal">({provider.id})</span>}
                      </div>
                      <div className="text-xs text-muted flex items-center gap-2 mt-0.5">
                        <code className="text-muted">{provider.base_url}</code>
                        <span>•</span>
                        <span>{conns.length} {t("providers.key", { count: conns.length })} ({t("providers.activeOf", { count: activeCount })})</span>
                      </div>
                    </div>
                  </div>

                  <div className="flex items-center gap-3">
                    <Chip size="sm" variant="soft" color="accent">{provider.format}</Chip>
                    <div className="flex gap-1" onClick={(e) => e.stopPropagation()}>
                      <Button isIconOnly size="sm" variant="ghost" onPress={() => openEditProvider(provider)} aria-label={t("providers.editAria")}><IconPencil className="w-4 h-4" /></Button>
                      <Button isIconOnly size="sm" variant="ghost" className="text-danger" onPress={() => setConfirmProviderId(provider.id)} aria-label={t("providers.deleteAria")}><IconTrash className="w-4 h-4" /></Button>
                    </div>
                  </div>
                </div>

                {isExpanded && (
                  <div className="p-4 bg-surface">
                    <div className="flex items-center gap-3 mb-4 pb-3 border-b border-border">
                      <span className="text-sm font-medium shrink-0">{t("providers.loadBalance")}</span>
                      <Select
                        aria-label={t("providers.loadBalanceAria")}
                        selectedKey={provider.load_balance || "failover"}
                        onSelectionChange={(k) => updateLoadBalance(provider.id, (k as string) ?? "failover")}
                        className="max-w-[260px]"
                        isDisabled={savingConfig === provider.id}
                      >
                        <Select.Trigger><Select.Value /></Select.Trigger>
                        <Select.Popover>
                          <ListBox>
                            <ListBox.Item id="failover" textValue={t("providers.failoverShort")}>{t("providers.failover")}</ListBox.Item>
                            <ListBox.Item id="round-robin" textValue={t("providers.roundRobinShort")}>{t("providers.roundRobin")}</ListBox.Item>
                          </ListBox>
                        </Select.Popover>
                      </Select>
                      {savingConfig === provider.id && <Spinner size="sm" />}
                    </div>

                    <div className="mb-4">
                      <div className="flex justify-between items-center mb-2">
                        <div className="text-sm font-semibold">{t("providers.providerModels")}</div>
                        <Button size="sm" variant="outline" onPress={() => syncProviderModels(provider.id)} isDisabled={loadingModels === provider.id}>
                          {t("providers.sync")}
                        </Button>
                      </div>
                      <div className="max-h-[160px] overflow-y-auto pr-2">
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
                      {t("providers.connectionsTitle")}
                      <Button size="sm" variant="outline" onPress={() => openNewConnection(provider.id)}><IconPlus className="w-4 h-4" /> {t("providers.addKey")}</Button>
                    </div>

                    {conns.length === 0 ? (
                      <div className="text-sm text-muted py-4 text-center border border-dashed border-border rounded-xl">
                        {t("providers.noKeys")}
                      </div>
                    ) : (
                        <Table>
                          <Table.ScrollContainer>
                            <Table.Content aria-label={t("providers.connAria")} className="bg-surface-secondary min-w-[420px]">
                              <Table.Header>
                                <Table.Column isRowHeader id="name">{t("providers.colName")}</Table.Column>
                                <Table.Column id="id">{t("providers.colId")}</Table.Column>
                                <Table.Column id="status">{t("providers.colStatus")}</Table.Column>
                                <Table.Column id="actions">{t("providers.colActions")}</Table.Column>
                              </Table.Header>
                              <Table.Body items={conns}>
                                {(c) => (
                                  <Table.Row key={c.id} id={c.id}>
                                    <Table.Cell className="font-medium">{c.name || t("providers.defaultName")}</Table.Cell>
                                    <Table.Cell><code className="text-[11px] text-muted font-mono">{c.id}</code></Table.Cell>
                                    <Table.Cell>
                                      <Chip size="sm" variant="soft" color={c.is_active ? "success" : "default"}>
                                        {c.is_active ? t("providers.active") : t("providers.inactive")}
                                      </Chip>
                                    </Table.Cell>
                                    <Table.Cell>
                                      <div className="flex gap-1 justify-end" onClick={(e) => e.stopPropagation()}>
                                        <Button isIconOnly size="sm" variant="ghost" onPress={() => openEditConnection(c)} aria-label={t("providers.editAria")}><IconPencil className="w-4 h-4" /></Button>
                                        <Button isIconOnly size="sm" variant="ghost" className="text-danger" onPress={() => setConfirmConnId(c.id)} aria-label={t("providers.deleteAria")}><IconTrash className="w-4 h-4" /></Button>
                                      </div>
                                    </Table.Cell>
                                  </Table.Row>
                                )}
                              </Table.Body>
                            </Table.Content>
                          </Table.ScrollContainer>
                        </Table>
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
            <Modal.Dialog className={providerStep === "pick" && !providerEditId ? "max-w-2xl" : "max-w-xl"}>
              <Modal.Header>
                <div className="flex w-full items-center justify-between gap-4">
                  <Modal.Heading className="shrink-0">
                    {providerEditId ? t("providers.editEndpoint") : providerStep === "pick" ? t("providers.chooseProvider") : t("providers.configureProvider")}
                  </Modal.Heading>
                  {!providerEditId && providerStep === "pick" && (
                    <div className="relative w-full max-w-xs shrink-0">
                      <span className="absolute left-3 top-1/2 -translate-y-1/2 pointer-events-none"><IconSearch className="w-4 h-4 text-muted" /></span>
                      <Input
                        value={search}
                        onChange={(e) => setSearch(e.target.value)}
                        placeholder={t("providers.searchPlaceholder")}
                        variant="secondary"
                        className="pl-9"
                        autoFocus
                        aria-label={t("providers.searchAria")}
                      />
                    </div>
                  )}
                </div>
              </Modal.Header>
              <Modal.Body className="flex flex-col gap-4">
                {!providerEditId && providerStep === "pick" && (
                  <>
                    <div className="max-h-[420px] overflow-y-auto rounded-lg border border-border divide-y divide-border">
                      {filteredCatalog.map((def) => {
                        const isOauth = oauthProviders.includes(def.id);
                        const isPopular = POPULAR.includes(def.id);
                        return (
                          <Button
                            key={def.id}
                            variant="ghost"
                            onPress={() => pickTemplate(def)}
                            className="w-full h-auto min-h-[76px] rounded-none px-4 py-3 text-left hover:bg-default-soft items-center justify-start"
                          >
                            <span className="w-2.5 h-2.5 rounded-full shrink-0 bg-muted" style={def.display.color ? { background: def.display.color } : undefined} />
                            <span className="min-w-0 flex-1 flex flex-col gap-1">
                              <span className="flex items-center gap-2 min-w-0">
                                <span className="font-medium text-sm truncate">{def.display.name}</span>
                                {isPopular && <Chip size="sm" color="accent" variant="soft" className="h-5 shrink-0 text-[10px]">{t("providers.popular")}</Chip>}
                                {isOauth && <Chip size="sm" color="default" variant="soft" className="h-5 shrink-0 text-[10px]">{t("providers.oauth")}</Chip>}
                              </span>
                              <span className="flex flex-wrap items-center gap-x-3 gap-y-1 text-[11px] text-muted">
                                <span className="font-mono">{def.id}</span>
                                {def.capabilities?.slice(0, 3).map((capability) => <span key={capability}>{capability}</span>)}
                              </span>
                            </span>
                            <IconChevron className="w-4 h-4 shrink-0 text-muted" />
                          </Button>
                        );
                      })}
                      {filteredCatalog.length === 0 && (
                        <div className="px-4 py-10 text-center text-sm text-muted">{t("providers.noCatalogResults")}</div>
                      )}
                    </div>
                    <div className="flex items-center justify-between gap-3 pt-1">
                      <span className="text-xs text-muted">{t("providers.customProviderHint")}</span>
                      <Button size="sm" variant="secondary" onPress={() => { setProviderForm(emptyProvider); setProviderStep("form"); }}>{t("providers.custom")}</Button>
                    </div>
                  </>
                )}

                {providerStep === "oauth" && (
                  <>
                    <Button size="sm" variant="ghost" className="self-start" onPress={() => setProviderStep("pick")}>{t("providers.back")}</Button>
                    <div className="bg-accent/10 rounded-lg p-3 text-sm space-y-1">
                      <p className="font-medium">{t("providers.connecting", { provider: oauthProviderId })}</p>
                      <p className="text-foreground/80">
                        {t("providers.oauthInstructions")}
                      </p>
                    </div>
                    {oauthAuthURL && (
                      <a href={oauthAuthURL} target="_blank" rel="noreferrer" className="text-sm text-accent underline break-all">
                        {t("providers.openLoginAgain")}
                      </a>
                    )}
                    <TextField value={oauthCode} onChange={setOauthCode}>
                      <Label>{t("providers.callbackOrCode")}</Label>
                      <Input />
                    </TextField>
                  </>
                )}

                {(providerEditId || providerStep === "form") && (
                  <>
                    {!providerEditId && (
                      <Button size="sm" variant="ghost" className="self-start" onPress={() => setProviderStep("pick")}>{t("providers.back")}</Button>
                    )}
                    <div className="flex flex-col gap-4">
                      <TextField value={providerForm.id} onChange={(v) => setProviderForm({ ...providerForm, id: v })} isDisabled={!!providerEditId}>
                        <Label>{t("providers.providerId")}</Label>
                        <Input variant="secondary" placeholder={t("providers.providerIdPlaceholder")} />
                      </TextField>
                      <TextField value={providerForm.name} onChange={(v) => setProviderForm({ ...providerForm, name: v })}>
                        <Label>{t("providers.friendlyName")}</Label>
                        <Input variant="secondary" placeholder={t("providers.friendlyNamePlaceholder")} />
                      </TextField>
                      <TextField value={providerForm.base_url} onChange={(v) => setProviderForm({ ...providerForm, base_url: v })}>
                        <Label>{t("providers.baseUrl")}</Label>
                        <Input variant="secondary" placeholder={t("providers.baseUrlPlaceholder")} />
                      </TextField>
                      <div className="flex flex-col gap-1">
                        <Label>{t("providers.apiFormat")}</Label>
                        <Select aria-label={t("providers.apiFormatAria")} selectedKey={providerForm.format} onSelectionChange={(k) => setProviderForm({ ...providerForm, format: (k as string) ?? "auto" })}>
                          <Select.Trigger className="bg-surface-secondary"><Select.Value /></Select.Trigger>
                          <Select.Popover>
                            <ListBox>{FORMATS.map((f) => <ListBox.Item key={f} id={f}>{f}</ListBox.Item>)}</ListBox>
                          </Select.Popover>
                        </Select>
                      </div>
                      <div className="flex flex-col gap-1">
                        <Label>{t("providers.auth")}</Label>
                        <Select aria-label={t("providers.authAria")} selectedKey={providerForm.auth} onSelectionChange={(k) => setProviderForm({ ...providerForm, auth: (k as string) ?? "bearer" })}>
                          <Select.Trigger className="bg-surface-secondary"><Select.Value /></Select.Trigger>
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
              {(providerEditId || providerStep === "form" || providerStep === "oauth") && (
                <Modal.Footer>
                  {(providerEditId || providerStep === "form") && (
                    <Button variant="primary" onPress={submitProvider} isDisabled={saving}>{t("providers.saveProvider")}</Button>
                  )}
                  {providerStep === "oauth" && (
                    <Button variant="primary" onPress={completeOAuth} isDisabled={saving || !oauthCode.trim()}>{t("providers.connect")}</Button>
                  )}
                </Modal.Footer>
              )}
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
                  {connEditId ? t("providers.editKey") : t("providers.addKeyTitle")} <span className="text-muted text-sm ml-2 font-normal">{t("providers.connSuffix", { provider: connProviderId })}</span>
                </Modal.Heading>
              </Modal.Header>
              <Modal.Body className="flex flex-col gap-4">
                <TextField value={connForm.name} onChange={(v) => setConnForm({ ...connForm, name: v })}>
                  <Label>{t("providers.keyName")}</Label>
                  <Input variant="secondary" placeholder={t("providers.keyNamePlaceholder")} />
                </TextField>
                <TextField value={connForm.api_key} onChange={(v) => setConnForm({ ...connForm, api_key: v })}>
                  <Label>{t("providers.apiKey")}</Label>
                  <Input variant="secondary" type="password" placeholder={connEditId ? t("providers.keepPlaceholder") : t("providers.skPlaceholder")} />
                </TextField>
                {error && <p className="text-sm text-danger">{error}</p>}
              </Modal.Body>
              <Modal.Footer>
                <Button variant="primary" onPress={submitConnection} isDisabled={saving}>{t("providers.saveKey")}</Button>
              </Modal.Footer>
            </Modal.Dialog>
          </Modal.Container>
        </Modal.Backdrop>
      </Modal>

      <AlertDialog>
        <AlertDialog.Backdrop isOpen={!!confirmProviderId} onOpenChange={(o) => !o && setConfirmProviderId(null)}>
          <AlertDialog.Container>
            <AlertDialog.Dialog className="sm:max-w-[400px]">
              <AlertDialog.CloseTrigger />
              <AlertDialog.Header>
                <AlertDialog.Icon status="danger" />
                <AlertDialog.Heading>{t("providers.removeProviderTitle")}</AlertDialog.Heading>
              </AlertDialog.Header>
              <AlertDialog.Body>
                <p>{t("providers.removeProviderBody")}</p>
              </AlertDialog.Body>
              <AlertDialog.Footer>
                <Button slot="close" variant="tertiary">{t("providers.cancel")}</Button>
                <Button slot="close" variant="danger" onPress={() => { if (confirmProviderId) removeProvider(confirmProviderId); setConfirmProviderId(null); }}>{t("providers.remove")}</Button>
              </AlertDialog.Footer>
            </AlertDialog.Dialog>
          </AlertDialog.Container>
        </AlertDialog.Backdrop>
      </AlertDialog>

      <AlertDialog>
        <AlertDialog.Backdrop isOpen={!!confirmConnId} onOpenChange={(o) => !o && setConfirmConnId(null)}>
          <AlertDialog.Container>
            <AlertDialog.Dialog className="sm:max-w-[400px]">
              <AlertDialog.CloseTrigger />
              <AlertDialog.Header>
                <AlertDialog.Icon status="danger" />
                <AlertDialog.Heading>{t("providers.removeConnTitle")}</AlertDialog.Heading>
              </AlertDialog.Header>
              <AlertDialog.Body>
                <p>{t("providers.removeConnBody")}</p>
              </AlertDialog.Body>
              <AlertDialog.Footer>
                <Button slot="close" variant="tertiary">{t("providers.cancel")}</Button>
                <Button slot="close" variant="danger" onPress={() => { if (confirmConnId) removeConnection(confirmConnId); setConfirmConnId(null); }}>{t("providers.remove")}</Button>
              </AlertDialog.Footer>
            </AlertDialog.Dialog>
          </AlertDialog.Container>
        </AlertDialog.Backdrop>
      </AlertDialog>
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
  const { t } = useTranslation();
  const [adding, setAdding] = useState(false);
  const [newModel, setNewModel] = useState("");
  const [saving, setSaving] = useState(false);
  const [confirmModel, setConfirmModel] = useState<ModelEntry | null>(null);

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
      toast.danger(t("providers.errAddModel", { message: err.message || err }));
    } finally {
      setSaving(false);
    }
  };

  const handleToggle = async (m: ModelEntry) => {
    try {
      await api.models.update(m.id, { is_active: !m.is_active });
      if (onToggle) onToggle(m.id, !m.is_active);
    } catch (err: any) {
      toast.danger(t("providers.errGeneric", { message: err.message || err }));
    }
  };

  const handleRemove = async (m: ModelEntry) => {
    try {
      await api.models.remove(m.id);
      if (onRemove) onRemove(m.id);
    } catch (err: any) {
      toast.danger(t("providers.errGeneric", { message: err.message || err }));
    }
  };

  if (loading) return <div className="py-2 flex items-center gap-2 text-sm text-muted"><Spinner size="sm" /> {t("providers.syncing")}</div>;
  if (error) return <div className="py-2 text-sm text-danger">{t("providers.errRender", { error })}</div>;

  return (
    <div className="flex flex-wrap gap-2 items-center">
      {adding ? (
        <form onSubmit={(e) => { e.preventDefault(); handleAdd(); }} className="flex items-center gap-1">
          <Input
            autoFocus
            className="min-w-[120px] text-[11px] font-mono"
            placeholder={t("providers.modelNamePlaceholder")}
            value={newModel}
            onChange={(e) => setNewModel(e.target.value)}
            disabled={saving}
            onBlur={() => !saving && handleAdd()}
            aria-label={t("providers.newModelAria")}
          />
          <Button type="submit" size="sm" variant="primary" isDisabled={saving}>{t("providers.add")}</Button>
        </form>
      ) : (
        <Button
          size="sm"
          variant="outline"
          onPress={() => setAdding(true)}
          className="inline-flex items-center gap-1 rounded-full border border-dashed border-accent px-2.5 py-0.5 text-[11px] font-mono text-accent hover:bg-accent/10 transition-colors h-auto"
        >
          {t("providers.addChip")}
        </Button>
      )}

      {models?.map((m) => (
        <Popover key={m.id}>
          <Popover.Trigger>
            <Button
              size="sm"
              variant={m.is_active ? "primary" : "outline"}
              onPress={() => {}}
              className={`inline-flex items-center rounded-full px-2.5 py-0.5 text-[11px] font-mono cursor-pointer hover:opacity-80 transition-opacity h-auto ${m.is_active ? "bg-accent/15 text-accent" : "bg-default-soft text-muted"}`}
            >
              {m.is_active ? "" : <span className="text-muted mr-0.5">○</span>}
              {m.model_id || m.id}
            </Button>
          </Popover.Trigger>
          <Popover.Content placement="bottom" className="p-1">
            <div className="flex flex-col gap-1 min-w-[140px]">
              <div className="text-[11px] font-mono text-muted px-2 pt-1 pb-1 break-all">{m.id}</div>
              <Button size="sm" variant="ghost" onPress={() => handleToggle(m)} className="justify-start">
                {m.is_active ? <IconEyeOff className="w-4 h-4" /> : <IconEye className="w-4 h-4" />}
                {m.is_active ? t("providers.deactivate") : t("providers.activate")}
              </Button>
              <Button size="sm" variant="ghost" className="justify-start text-danger" onPress={() => setConfirmModel(m)}>
                <IconTrash className="w-4 h-4" /> {t("providers.removeModel")}
              </Button>
            </div>
          </Popover.Content>
        </Popover>
      ))}
      {models && models.length === 0 && !adding && (
        <span className="text-sm text-muted">{t("providers.noSyncedModels")}</span>
      )}

      <AlertDialog>
        <AlertDialog.Backdrop isOpen={!!confirmModel} onOpenChange={(o) => !o && setConfirmModel(null)}>
          <AlertDialog.Container>
            <AlertDialog.Dialog className="sm:max-w-[400px]">
              <AlertDialog.CloseTrigger />
              <AlertDialog.Header>
                <AlertDialog.Icon status="danger" />
                <AlertDialog.Heading>{t("providers.removeModelTitle", { id: confirmModel?.id })}</AlertDialog.Heading>
              </AlertDialog.Header>
              <AlertDialog.Body>
                <p>{t("providers.removeModelBody")}</p>
              </AlertDialog.Body>
              <AlertDialog.Footer>
                <Button slot="close" variant="tertiary">{t("providers.cancel")}</Button>
                <Button slot="close" variant="danger" onPress={() => { if (confirmModel) handleRemove(confirmModel); setConfirmModel(null); }}>{t("providers.remove")}</Button>
              </AlertDialog.Footer>
            </AlertDialog.Dialog>
          </AlertDialog.Container>
        </AlertDialog.Backdrop>
      </AlertDialog>
    </div>
  );
}
