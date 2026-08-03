import { useEffect, useState } from "react";
import { Spinner, Switch, Button, Card } from "@heroui/react";
import { api } from "../api";
import { formatCompact } from "../format";

interface CacheStats {
  enabled: boolean;
  entries: number;
  hits: number;
  misses: number;
}

export default function Performance() {
  const [rtkEnabled, setRtkEnabled] = useState(false);
  const [cacheEnabled, setCacheEnabled] = useState(false);
  const [loading, setLoading] = useState(true);
  const [rtkLoading, setRtkLoading] = useState(false);
  const [cacheLoading, setCacheLoading] = useState(false);
  const [cacheStats, setCacheStats] = useState<CacheStats | null>(null);
  const [flushing, setFlushing] = useState(false);

  const refresh = () => {
    Promise.all([
      api.settings.get().then((s) => {
        setRtkEnabled(s.rtk_enabled);
        setCacheEnabled(s.cache_enabled ?? false);
      }).catch(() => {}),
      api.cache.stats().then((s) => setCacheStats({ enabled: s.enabled, entries: s.entries ?? 0, hits: s.hits ?? 0, misses: s.misses ?? 0 })).catch(() => setCacheStats(null)),
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

      {/* Info note */}
      <Card variant="transparent" className="p-4">
        <p className="text-xs text-muted">
          Ambas as otimizações são <strong>fail-open</strong>: qualquer erro interno retorna o body/resposta original sem afetar o request. Quando desligadas, zero overhead (nil check no hot path).
        </p>
      </Card>
    </div>
  );
}