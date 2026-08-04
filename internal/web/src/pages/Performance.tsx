import { useEffect, useState } from "react";
import { Spinner, Switch, Button, Card, Select, ListBox, Input, Chip } from "@heroui/react";
import { api } from "../api";
import { ModelComboBox, type ModelComboBoxItem } from "../components/ModelComboBox";
import { formatCompact } from "../format";
import { IconPlus, IconTrash, IconX } from "../icons";

interface CacheStats {
  enabled: boolean;
  entries: number;
  hits: number;
  misses: number;
}

export default function Performance() {
  const [rtkEnabled, setRtkEnabled] = useState(false);
  const [cacheEnabled, setCacheEnabled] = useState(false);
  const [semanticEnabled, setSemanticEnabled] = useState(false);
  const [semanticMode, setSemanticMode] = useState("active");
  const [embeddingModel, setEmbeddingModel] = useState("");
  const [embeddingModels, setEmbeddingModels] = useState<string[]>([]);
  const [loading, setLoading] = useState(true);
  const [rtkLoading, setRtkLoading] = useState(false);
  const [cacheLoading, setCacheLoading] = useState(false);
  const [cacheStats, setCacheStats] = useState<CacheStats | null>(null);
  const [semanticStats, setSemanticStats] = useState<CacheStats | null>(null);
  const [flushing, setFlushing] = useState(false);
  // Caching groups: group name -> model list.
  const [groups, setGroups] = useState<Record<string, string[]>>({});
  const [newGroupName, setNewGroupName] = useState("");
  const [modelItems, setModelItems] = useState<ModelComboBoxItem[]>([]);

  const refresh = () => {
    Promise.all([
      api.settings.get().then((s) => {
        setRtkEnabled(s.rtk_enabled);
        setCacheEnabled(s.cache_enabled ?? false);
        setSemanticEnabled(s.semantic_cache_enabled ?? false);
        setSemanticMode(s.semantic_cache_mode || "active");
        setEmbeddingModel(s.semantic_cache_model || "");
        setGroups(s.caching_groups || {});
      }).catch(() => {}),
      api.cache.stats().then((s) => setCacheStats({ enabled: s.enabled, entries: s.entries ?? 0, hits: s.hits ?? 0, misses: s.misses ?? 0 })).catch(() => setCacheStats(null)),
      api.semanticCache.stats().then((s) => setSemanticStats({ enabled: s.enabled, entries: s.entries ?? 0, hits: s.hits ?? 0, misses: s.misses ?? 0 })).catch(() => setSemanticStats(null)),
      api.models.list().then((ms) => {
        const em = ms.filter((m) => m.kind === "embedding").map((m) => m.id);
        setEmbeddingModels(em);
        setModelItems(ms.filter((m) => m.owned_by !== "combo").map((m) => ({ id: m.id, itemType: "model", kind: m.kind || "llm", isActive: true })));
      }).catch(() => {}),
    ]).finally(() => setLoading(false));
  };

  useEffect(() => { refresh(); }, []);

  const toggleRtk = (enabled: boolean) => {
    setRtkLoading(true);
    api.settings.update({ rtk_enabled: enabled })
      .then(() => setRtkEnabled(enabled))
      .catch(() => setRtkEnabled(!enabled))
      .finally(() => setRtkLoading(false));
  };

  const toggleCache = (enabled: boolean) => {
    setCacheLoading(true);
    api.settings.update({ cache_enabled: enabled })
      .then(() => {
        setCacheEnabled(enabled);
        setTimeout(refresh, 200);
      })
      .catch(() => setCacheEnabled(!enabled))
      .finally(() => setCacheLoading(false));
  };

  const flushCache = () => {
    setFlushing(true);
    api.cache.flush()
      .then(() => setTimeout(refresh, 200))
      .catch(() => {})
      .finally(() => setFlushing(false));
  };

  const toggleSemantic = (enabled: boolean) => {
    api.settings.update({ semantic_cache_enabled: enabled })
      .then(() => {
        setSemanticEnabled(enabled);
        setTimeout(refresh, 200);
      })
      .catch(() => setSemanticEnabled(!enabled));
  };

  const setSemMode = (mode: string) => {
    setSemanticMode(mode);
    api.settings.update({ semantic_cache_mode: mode }).catch(() => {});
  };

  const selectEmbeddingModel = (id: string) => {
    setEmbeddingModel(id);
    api.settings.update({ semantic_cache_model: id }).catch(() => {});
  };

  const flushSemantic = () => {
    api.semanticCache.flush()
      .then(() => setTimeout(refresh, 200))
      .catch(() => {});
  };

  const persistGroups = (next: Record<string, string[]>) => {
    setGroups(next);
    api.settings.update({ caching_groups: next }).catch(() => refresh());
  };

  const addGroup = () => {
    const name = newGroupName.trim();
    if (!name || groups[name]) return;
    persistGroups({ ...groups, [name]: [] });
    setNewGroupName("");
  };

  const removeGroup = (name: string) => {
    const next = { ...groups };
    delete next[name];
    persistGroups(next);
  };

  const addModelToGroup = (group: string, id: string) => {
    if (groups[group]?.includes(id)) return;
    persistGroups({ ...groups, [group]: [...(groups[group] || []), id] });
  };

  const removeModelFromGroup = (group: string, id: string) => {
    persistGroups({ ...groups, [group]: groups[group].filter((m) => m !== id) });
  };

  if (loading) return <div className="flex justify-center py-20"><Spinner /></div>;

  const hitRate = cacheStats && cacheStats.hits && cacheStats.misses
    ? ((cacheStats.hits / (cacheStats.hits + cacheStats.misses)) * 100).toFixed(1)
    : null;

  return (
    <div className="space-y-6 max-w-3xl">
      <div>
        <h1 className="text-2xl font-bold tracking-tight">Performance</h1>
        <p className="text-sm text-muted mt-0.5">
          Otimizações para reduzir latência, tokens e custo.
        </p>
      </div>

      {/* RTK section */}
      <Card className="p-6">
        <div className="flex items-start justify-between gap-4">
          <div className="flex-1">
            <div className="flex items-center gap-2">
              <h3 className="font-semibold">RTK — Token Compression</h3>
              <span className="text-[10px] text-muted bg-default-soft px-1.5 py-0.5 rounded">beta</span>
            </div>
            <p className="text-sm text-muted mt-1">
              Comprime outputs de ferramentas (git diff, grep, ls, etc.) dentro de <code className="text-xs">tool_result</code> blocks antes de enviar ao provider. Reduz 20–40% dos input tokens.
            </p>
            <p className="text-xs text-muted mt-2">
              Detecção automática do tipo de output · fail-open (erro = body original) · só afeta requests de chat
            </p>
          </div>
          <Switch
            isSelected={rtkEnabled}
            onChange={toggleRtk}
            isDisabled={rtkLoading}
            size="lg"
          >
            <Switch.Content aria-label="RTK token compression">
              <Switch.Control>
                <Switch.Thumb />
              </Switch.Control>
            </Switch.Content>
          </Switch>
        </div>
      </Card>

      {/* Cache section */}
      <Card className="p-6">
        <div className="flex items-start justify-between gap-4">
          <div className="flex-1">
            <h3 className="font-semibold">Response Cache</h3>
            <p className="text-sm text-muted mt-1">
              Cache de respostas por hash determinístico do request. Requests idênticos recebem a resposta cacheada sem chamar o provider — zero latência upstream, zero custo de token.
            </p>
            <p className="text-xs text-muted mt-2">
              LRU 10k entries · TTL 5min · stream + non-stream · bypass via <code className="text-xs">x-gr-cache: off</code> header
            </p>
          </div>
          <Switch
            isSelected={cacheEnabled}
            onChange={toggleCache}
            isDisabled={cacheLoading}
            size="lg"
          >
            <Switch.Content aria-label="Response cache">
              <Switch.Control>
                <Switch.Thumb />
              </Switch.Control>
            </Switch.Content>
          </Switch>
        </div>

        {/* Cache stats */}
        {cacheEnabled && cacheStats && cacheStats.enabled && (
          <div className="mt-4 pt-4 border-t border-border">
            <div className="grid grid-cols-3 gap-4">
              <div>
                <p className="text-xs text-muted uppercase tracking-wide">Entries</p>
                <p className="text-2xl font-bold tabular-nums mt-1">{formatCompact(cacheStats.entries ?? 0)}</p>
              </div>
              <div>
                <p className="text-xs text-muted uppercase tracking-wide">Hits</p>
                <p className="text-2xl font-bold tabular-nums mt-1 text-success">{formatCompact(cacheStats.hits ?? 0)}</p>
              </div>
              <div>
                <p className="text-xs text-muted uppercase tracking-wide">Misses</p>
                <p className="text-2xl font-bold tabular-nums mt-1 text-muted">{formatCompact(cacheStats.misses ?? 0)}</p>
              </div>
            </div>
            {hitRate && (
              <div className="mt-3 flex items-center gap-2">
                <p className="text-xs text-muted">Hit rate:</p>
                <p className="text-xs font-semibold text-success">{hitRate}%</p>
                <Button
                  size="sm"
                  variant="danger-soft"
                  onPress={flushCache}
                  isDisabled={flushing}
                  className="ml-auto"
                >
                  Limpar cache
                </Button>
              </div>
            )}
          </div>
        )}
      </Card>

      {/* Semantic cache section */}
      <Card className="p-6">
        <div className="flex items-start justify-between gap-4">
          <div className="flex-1">
            <div className="flex items-center gap-2">
              <h3 className="font-semibold">Semantic Cache</h3>
              <span className="text-[10px] text-muted bg-default-soft px-1.5 py-0.5 rounded">beta</span>
            </div>
            <p className="text-sm text-muted mt-1">
              Cache vetorial por similaridade. Requests com prompts parafraseados recebem respostas cacheadas sem chamar o provider. Funciona em conjunto com o Response Cache — o hash determinístico primeiro, a similaridade depois.
            </p>
            <p className="text-xs text-muted mt-2">
              Cache keyado por modelo real (compartilhado entre combo e chamadas diretas) · precisa de um modelo de embedding configurado
            </p>
          </div>
          <Switch
            isSelected={semanticEnabled}
            onChange={toggleSemantic}
            isDisabled={!embeddingModels.length && !embeddingModel}
            size="lg"
          >
            <Switch.Content aria-label="Semantic cache">
              <Switch.Control>
                <Switch.Thumb />
              </Switch.Control>
            </Switch.Content>
          </Switch>
        </div>

        {semanticEnabled && (
          <div className="mt-4 pt-4 border-t border-border space-y-4">
            <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
              <div>
                <p className="text-xs text-muted mb-1.5">Modelo de embedding</p>
                {embeddingModels.length > 0 ? (
                  <Select
                    aria-label="Modelo de embedding"
                    selectedKey={embeddingModel || null}
                    onSelectionChange={(k) => selectEmbeddingModel(String(k))}
                    className="w-full"
                  >
                    <Select.Trigger>
                      <Select.Value>{embeddingModel || "Selecione um modelo"}</Select.Value>
                      <Select.Indicator />
                    </Select.Trigger>
                    <Select.Popover>
                      <ListBox>
                        {embeddingModels.map((m) => <ListBox.Item key={m} id={m}>{m}</ListBox.Item>)}
                      </ListBox>
                    </Select.Popover>
                  </Select>
                ) : (
                  <p className="text-sm text-muted">Nenhum modelo embedding disponível no catálogo.</p>
                )}
              </div>
              <div>
                <p className="text-xs text-muted mb-1.5">Modo</p>
                <Select
                  aria-label="Modo do semantic cache"
                  selectedKey={semanticMode}
                  onSelectionChange={(k) => setSemMode(String(k))}
                  className="w-full"
                >
                  <Select.Trigger>
                    <Select.Value>{semanticMode === "lazy" ? "Lazy" : "Active"}</Select.Value>
                    <Select.Indicator />
                  </Select.Trigger>
                  <Select.Popover>
                    <ListBox>
                      <ListBox.Item id="active">Active — busca em cada miss (mais hits)</ListBox.Item>
                      <ListBox.Item id="lazy">Lazy — só busca após 50 entries (sem overhead)</ListBox.Item>
                    </ListBox>
                  </Select.Popover>
                </Select>
              </div>
            </div>

            {semanticStats && semanticStats.enabled && (
              <div className="pt-4 border-t border-border">
                <div className="grid grid-cols-3 gap-4">
                  <div>
                    <p className="text-xs text-muted uppercase tracking-wide">Entries</p>
                    <p className="text-2xl font-bold tabular-nums mt-1">{formatCompact(semanticStats.entries ?? 0)}</p>
                  </div>
                  <div>
                    <p className="text-xs text-muted uppercase tracking-wide">Hits</p>
                    <p className="text-2xl font-bold tabular-nums mt-1 text-success">{formatCompact(semanticStats.hits ?? 0)}</p>
                  </div>
                  <div>
                    <p className="text-xs text-muted uppercase tracking-wide">Misses</p>
                    <p className="text-2xl font-bold tabular-nums mt-1 text-muted">{formatCompact(semanticStats.misses ?? 0)}</p>
                  </div>
                </div>
                <div className="mt-3 flex justify-end">
                  <Button size="sm" variant="danger-soft" onPress={flushSemantic}>Limpar cache semântico</Button>
                </div>
              </div>
            )}
          </div>
        )}
      </Card>

      {/* Caching groups */}
      <Card className="p-6">
        <div className="flex items-start justify-between gap-4">
          <div className="flex-1">
            <h3 className="font-semibold">Caching Groups</h3>
            <p className="text-sm text-muted mt-1">
              Modelos intercambiáveis compartilham a mesma entrada de cache. A resposta de um é servida para request que rotearia para outro — os modelos do grupo precisam ser equivalentes.
            </p>
          </div>
        </div>
        <div className="mt-4 flex gap-2">
          <Input
            value={newGroupName}
            onChange={(e) => setNewGroupName(e.target.value)}
            onKeyDown={(e) => { if (e.key === "Enter") addGroup(); }}
            placeholder="Nome do grupo (ex: gpt-family)"
            className="flex-1"
          />
          <Button variant="primary" onPress={addGroup} isDisabled={!newGroupName.trim()}>
            <IconPlus className="w-4 h-4" /> Adicionar grupo
          </Button>
        </div>
        <div className="mt-4 space-y-3">
          {Object.entries(groups).map(([name, models]) => (
            <div key={name} className="border border-border rounded-lg p-3">
              <div className="flex items-center justify-between gap-3">
                <p className="text-sm font-medium truncate">{name}</p>
                <Button size="sm" variant="danger-soft" isIconOnly aria-label={`Remover grupo ${name}`} onPress={() => removeGroup(name)}>
                  <IconTrash className="w-3.5 h-3.5" />
                </Button>
              </div>
              <div className="mt-2 flex flex-wrap gap-1.5">
                {models.length === 0 && <span className="text-xs text-muted">Nenhum modelo — adicione abaixo.</span>}
                {models.map((m) => (
                  <Chip key={m} size="sm" variant="soft">
                    <span className="flex items-center gap-1">
                      {m}
                      <button
                        className="text-muted hover:text-danger transition-colors"
                        onClick={() => removeModelFromGroup(name, m)}
                        aria-label={`Remover ${m} do grupo ${name}`}
                      >
                        <IconX className="w-3 h-3" />
                      </button>
                    </span>
                  </Chip>
                ))}
              </div>
              <ModelComboBox
                items={modelItems.filter((i) => !models.includes(i.id))}
                ariaLabel={`Adicionar modelo ao grupo ${name}`}
                inputPlaceholder="Adicionar modelo..."
                inputClassName="text-xs"
                className="mt-2 max-w-xs"
                selectedKey={null}
                onSelectionChange={(id) => addModelToGroup(name, id)}
              />
            </div>
          ))}
          {Object.keys(groups).length === 0 && (
            <p className="text-sm text-muted">Nenhum grupo ainda. Crie um acima para compartilhar cache entre modelos equivalentes.</p>
          )}
        </div>
      </Card>

      {/* Info note */}
      <Card variant="transparent" className="p-4">
        <p className="text-xs text-muted">
          As otimizações são <strong>fail-open</strong>: qualquer erro interno retorna o body/resposta original sem afetar o request. Quando desligadas, zero overhead (nil check no hot path).
        </p>
      </Card>
    </div>
  );
}