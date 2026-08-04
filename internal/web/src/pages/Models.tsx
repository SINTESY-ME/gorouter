import { useEffect, useMemo, useState, useCallback, memo, useRef } from "react";
import { Icon } from "@iconify/react";
import {
  Input, Spinner, Chip, Button, Card, Dropdown, Label, Modal, Select, ListBox, TextField,
} from "@heroui/react";
import { useTranslation } from "react-i18next";
import { api, type ModelEntry, type Provider, type ModelStat, type ModelPricing } from "../api";
import { formatCompact } from "../format";
import { IconSearch, IconTrash, IconPower, IconDollar, IconDotsVertical } from "../icons";

const KINDS = ["llm", "embedding", "image", "tts", "stt", "rerank", "ocr", "video"];

const formatPricePer1M = (perToken: number | undefined): string | null => {
  if (!perToken || perToken <= 0) return null;
  const per1M = perToken * 1_000_000;
  if (per1M < 0.01) return `$${per1M.toFixed(4)}/1M`;
  return `$${per1M.toFixed(2)}/1M`;
};
const formatPricePerImage = (perImage: number | undefined): string | null => {
  if (!perImage || perImage <= 0) return null;
  return `$${perImage.toFixed(4)}/img`;
};

// statKey derives the bare model id (without the "provider/" prefix) used as
// a key in the per-model stats map. Falls back to the full id.
function statKey(m: ModelEntry): string {
  const parts = m.id.split("/");
  return parts.length > 1 ? parts[1] : m.id;
}

interface ModelCardProps {
  model: ModelEntry;
  stat?: ModelStat;
  isMenuOpen: boolean;
  onOpenMenu: (id: string) => void;
  onCloseMenu: () => void;
  onToggle: (m: ModelEntry) => void;
  onRemove: (m: ModelEntry) => void;
  onPricing: (m: ModelEntry) => void;
}

// ModelCard renders a single model card. It is memoized so a state change in
// the parent (open menu id, copy feedback) only re-renders the affected card
// instead of every card in the list — this is what keeps the page responsive
// with hundreds of models.
const ModelCard = memo(function ModelCard({
  model: m,
  stat: st,
  isMenuOpen,
  onOpenMenu,
  onCloseMenu,
  onToggle,
  onRemove,
  onPricing,
}: ModelCardProps) {
  const { t } = useTranslation();
  const [copied, setCopied] = useState(false);
  const copyTimer = useRef<number | null>(null);
  useEffect(() => () => { if (copyTimer.current !== null) window.clearTimeout(copyTimer.current); }, []);

  const handleCopy = useCallback(() => {
    navigator.clipboard.writeText(m.id).then(() => {
      setCopied(true);
      if (copyTimer.current !== null) window.clearTimeout(copyTimer.current);
      copyTimer.current = window.setTimeout(() => setCopied(false), 1500);
    }).catch(() => {});
  }, [m.id]);

  const handleAction = useCallback((key: import("@heroui/react").Key) => {
    const k = String(key);
    onCloseMenu();
    if (k === "pricing") onPricing(m);
    else if (k === "toggle") onToggle(m);
    else if (k === "remove") onRemove(m);
  }, [onCloseMenu, onPricing, onToggle, onRemove, m]);

  return (
    <Card className="group relative p-3 hover:border-border transition-colors">
      <div className="flex items-start gap-2 pr-6">
        <code
          className="text-sm font-mono truncate flex-1 cursor-pointer hover:text-accent transition-colors"
          title={t("models.copyTooltip", { id: m.id })}
          onClick={handleCopy}
        >
          {copied ? t("models.copied") : m.id}
        </code>
      </div>
      <span
        className={`absolute right-9 top-3 w-2 h-2 rounded-full ${m.is_active ? "bg-success" : "bg-default-soft"}`}
        title={m.is_active ? t("models.active") : t("models.inactive")}
      />
      <div className="flex min-w-0 items-center gap-1.5">
        <Chip size="sm" color="default" className="h-5 shrink-0 text-[10px] opacity-70">{m.kind}</Chip>
        {m.context > 0 && (
          <Chip size="sm" color="accent" variant="soft" className="h-5 shrink-0 text-[10px]" title={t("models.ctxTitle")}>
            {m.context >= 1000 ? `${(m.context / 1000).toLocaleString("en-US", { maximumFractionDigits: 0 })}k` : m.context} {t("models.ctxSuffix")}
          </Chip>
        )}
        <span className="min-w-0 truncate text-[10px] text-muted">{m.source}</span>
        <div className="ml-auto flex min-w-0 items-center gap-1.5 text-[10px] opacity-70">
          {(() => {
            const p = m.pricing;
            if (!p || (!p.source && !p.input_cost_per_token && !p.output_cost_per_token && !p.output_cost_per_image)) return null;
            const inPrice = formatPricePer1M(p.input_cost_per_token);
            const outPrice = formatPricePer1M(p.output_cost_per_token);
            const imgPrice = formatPricePerImage(p.output_cost_per_image);
            if (!inPrice && !outPrice && !imgPrice) return <span className="truncate text-muted">{t("models.free")}</span>;
            return (
              <span className="flex min-w-0 items-center gap-1 truncate">
                {inPrice && <span className="truncate tabular-nums text-success">{inPrice}</span>}
                {outPrice && <span className="truncate tabular-nums text-accent">{outPrice}</span>}
                {imgPrice && <span className="truncate tabular-nums text-warning">{imgPrice}</span>}
              </span>
            );
          })()}
        </div>
      </div>
      {st && st.requests > 0 && (
        <div className="flex items-center gap-3 text-[10px] text-muted">
          <span className="tabular-nums">{st.avg_tps > 0 ? `${st.avg_tps.toFixed(1)} ${t("models.tokPerSec")}` : "—"}</span>
          <span className="tabular-nums">{st.avg_ttft_ms && st.avg_ttft_ms > 0 ? `${t("models.ttft")}${Math.round(st.avg_ttft_ms)}ms` : ""}{st.avg_ttft_ms && st.avg_ttft_ms > 0 ? " · " : ""}{st.avg_latency_ms > 0 ? `${Math.round(st.avg_latency_ms)}ms` : "—"}</span>
          <span className="tabular-nums">{st.requests > 999 ? formatCompact(st.requests) : `${st.requests}x`}</span>
        </div>
      )}
      <div className="absolute top-1.5 right-1.5 opacity-0 transition-opacity group-hover:opacity-100">
        {isMenuOpen ? (
          <Dropdown isOpen onOpenChange={(o) => { if (!o) onCloseMenu(); }}>
            <Dropdown.Trigger>
              <Button isIconOnly size="sm" variant="tertiary" className="size-6 min-w-6 p-0" aria-label={t("models.closeMenuAria")}>
                <IconDotsVertical className="size-3.5" />
              </Button>
            </Dropdown.Trigger>
            <Dropdown.Popover placement="bottom end">
              <Dropdown.Menu onAction={handleAction}>
                <Dropdown.Item id="pricing" textValue={t("models.editPrice")}>
                  <IconDollar className="size-4 shrink-0 text-muted" />
                  <Label>{t("models.editPrice")}</Label>
                </Dropdown.Item>
                <Dropdown.Item id="toggle" textValue={m.is_active ? t("models.deactivate") : t("models.activate")}>
                  <IconPower className={`size-4 shrink-0 ${m.is_active ? "text-success" : "text-muted"}`} />
                  <Label>{m.is_active ? t("models.deactivate") : t("models.activate")}</Label>
                </Dropdown.Item>
                <Dropdown.Item id="remove" textValue={t("models.delete")} variant="danger">
                  <IconTrash className="size-4 shrink-0 text-danger" />
                  <Label>{t("models.delete")}</Label>
                </Dropdown.Item>
              </Dropdown.Menu>
            </Dropdown.Popover>
          </Dropdown>
        ) : (
          <Button
            isIconOnly
            size="sm"
            variant="tertiary"
            className="size-6 min-w-6 p-0"
            aria-label={t("models.actionsAria")}
            onPress={() => onOpenMenu(m.id)}
          >
            <IconDotsVertical className="size-3.5" />
          </Button>
        )}
      </div>
    </Card>
  );
});

export default function Models() {
  const { t } = useTranslation();
  const [items, setItems] = useState<ModelEntry[]>([]);
  const [stats, setStats] = useState<Record<string, ModelStat>>({});
  const [providers, setProviders] = useState<Provider[]>([]);
  const [loading, setLoading] = useState(true);
  const [query, setQuery] = useState("");
  const [syncing, setSyncing] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [addOpen, setAddOpen] = useState(false);
  const [addProviderId, setAddProviderId] = useState<string>("");
  const [addForm, setAddForm] = useState({ model_id: "", name: "", kind: "llm", context: 0 });
  const [pricingOpen, setPricingOpen] = useState(false);
  const [pricingModel, setPricingModel] = useState<ModelEntry | null>(null);
  const [pricingForm, setPricingForm] = useState({ inputPer1M: "", outputPer1M: "", perImage: "" });
  const [openMenuId, setOpenMenuId] = useState<string | null>(null);
  // Collapsible provider groups. Collapsed by default so the page doesn't
  // render ~1,000 model cards at once (the DOM stays small and interactions
  // like opening a dropdown stay instant). A search query auto-expands all.
  const [expandedGroups, setExpandedGroups] = useState<Set<string>>(new Set());
  const [showAllGroups, setShowAllGroups] = useState<Set<string>>(new Set());

  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    // Load providers (for the sync/add flows) and the full model catalog in
    // a single round-trip, in parallel. The old code issued one
    // /api/providers/{id}/models request per provider, which made the page
    // slow to load with many providers.
    Promise.all([
      api.providers.list().catch(() => [] as Provider[]),
      api.models.all().catch(() => [] as ModelEntry[]),
    ])
      .then(([ps, all]) => {
        if (cancelled) return;
        setProviders(ps);
        setItems(all);
        api.models.stats().then(setStats).catch(() => {});
      })
      .catch((e) => setError(e?.message ?? t("models.loadError")))
      .finally(() => setLoading(false));
    return () => { cancelled = true; };
  }, []);

  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase();
    if (!q) return items;
    return items.filter((m) =>
      m.id.toLowerCase().includes(q) ||
      m.provider_id.toLowerCase().includes(q) ||
      m.kind.toLowerCase().includes(q)
    );
  }, [items, query]);

  const groups = useMemo(() => {
    const order: string[] = [];
    const map: Record<string, ModelEntry[]> = {};
    for (const m of filtered) {
      const key = m.provider_id;
      if (!map[key]) { map[key] = []; order.push(key); }
      map[key].push(m);
    }
    for (const k of order) map[k].sort((a, b) => a.id.localeCompare(b.id));
    order.sort();
    return order.map((k) => ({ providerId: k, models: map[k] }));
  }, [filtered]);

  // When searching, every group is expanded and uncapped (results are a
  // small filtered subset). Otherwise groups render collapsed, expanding to
  // a capped window with a "show all" affordance. This keeps the DOM small
  // with ~1,000+ models in the catalog.
  const isSearching = query.trim() !== "";
  const GROUP_PAGE = 50;
  const visibleGroups = useMemo(() => {
    return groups.map((g) => {
      if (isSearching) {
        return { ...g, collapsed: false, shown: g.models.length, capped: false };
      }
      const expanded = expandedGroups.has(g.providerId);
      const showAll = showAllGroups.has(g.providerId);
      if (!expanded) {
        return { ...g, collapsed: true, shown: 0, capped: false };
      }
      const shown = showAll ? g.models.length : Math.min(GROUP_PAGE, g.models.length);
      return { ...g, collapsed: false, shown, capped: !showAll && g.models.length > GROUP_PAGE };
    });
  }, [groups, isSearching, expandedGroups, showAllGroups]);

  const toggleGroup = useCallback((providerId: string) => {
    // During search every group is expanded; ignore toggles so clearing the
    // search doesn't leave groups in an unexpected collapsed state.
    if (query.trim() !== "") return;
    setExpandedGroups((prev) => {
      const next = new Set(prev);
      if (next.has(providerId)) next.delete(providerId); else next.add(providerId);
      return next;
    });
  }, [query]);

  const expandAllGroup = useCallback((providerId: string) => {
    setShowAllGroups((prev) => new Set(prev).add(providerId));
  }, []);

  const sync = useCallback(async (providerId: string) => {
    const p = providers.find((x) => x.id === providerId);
    if (!p) return;
    setSyncing(p.id);
    try {
      const entries = await api.providers.syncModels(p.id);
      setItems((prev) => {
        const without = prev.filter((m) => m.provider_id !== providerId);
        return [...without, ...entries];
      });
    } catch (e: any) {
      setError(e?.message ?? t("models.syncError"));
    } finally {
      setSyncing(null);
    }
  }, [providers]);

  const toggleActive = useCallback(async (m: ModelEntry) => {
    try {
      await api.models.update(m.id, { is_active: !m.is_active });
      setItems((prev) => prev.map((x) => x.id === m.id ? { ...x, is_active: !x.is_active } : x));
    } catch (e: any) { setError(e?.message); }
  }, []);

  const removeModel = useCallback(async (m: ModelEntry) => {
    try {
      await api.models.remove(m.id);
      setItems((prev) => prev.filter((x) => x.id !== m.id));
    } catch (e: any) { setError(e?.message); }
  }, []);

  const openAdd = useCallback((providerId: string) => {
    const p = providers.find((x) => x.id === providerId);
    if (!p) return;
    setAddProviderId(p.id);
    setAddForm({ model_id: "", name: "", kind: "llm", context: 0 });
    setAddOpen(true);
  }, [providers]);

  const submitAdd = async () => {
    try {
      const entry = await api.providers.addModel(addProviderId, {
        model_id: addForm.model_id,
        name: addForm.name || undefined,
        kind: addForm.kind,
        context: addForm.context || undefined,
      });
      setItems((prev) => [...prev, entry]);
      setAddOpen(false);
    } catch (e: any) { setError(e?.message); }
  };

  const openPricing = useCallback((m: ModelEntry) => {
    setPricingModel(m);
    const p = (m.pricing || {}) as ModelPricing;
    setPricingForm({
      inputPer1M: p.input_cost_per_token ? String((p.input_cost_per_token * 1_000_000).toFixed(2)) : "",
      outputPer1M: p.output_cost_per_token ? String((p.output_cost_per_token * 1_000_000).toFixed(2)) : "",
      perImage: p.output_cost_per_image ? String(p.output_cost_per_image) : "",
    });
    setPricingOpen(true);
  }, []);

  const submitPricing = async () => {
    if (!pricingModel) return;
    const pricing: ModelPricing = {
      input_cost_per_token: parseFloat(pricingForm.inputPer1M) ? parseFloat(pricingForm.inputPer1M) / 1_000_000 : 0,
      output_cost_per_token: parseFloat(pricingForm.outputPer1M) ? parseFloat(pricingForm.outputPer1M) / 1_000_000 : 0,
      output_cost_per_image: parseFloat(pricingForm.perImage) ? parseFloat(pricingForm.perImage) : 0,
    };
    try {
      const updated = await api.models.pricing(pricingModel.id, pricing);
      setItems((prev) => prev.map((x) => x.id === pricingModel.id ? { ...x, pricing: updated.pricing } : x));
      setPricingOpen(false);
    } catch (e: any) { setError(e?.message); }
  };

  const openMenu = useCallback((id: string) => setOpenMenuId(id), []);
  const closeMenu = useCallback(() => setOpenMenuId(null), []);

  if (loading) {
    return <div className="flex justify-center py-20"><Spinner /></div>;
  }

  return (
    <div className="space-y-5">
      <div className="flex justify-between items-end gap-4 flex-wrap">
        <div>
          <h1 className="text-2xl font-bold tracking-tight">{t("models.title")}</h1>
          <p className="text-sm text-muted mt-0.5">{t("models.subtitle", { count: items.length, active: items.filter(m => m.is_active).length })}</p>
        </div>
        <div className="relative max-w-xs">
          <span className="absolute left-3 top-1/2 -translate-y-1/2 pointer-events-none"><IconSearch className="w-4 h-4 text-muted" /></span>
          <Input
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder={t("models.searchPlaceholder")}
            className="pl-9"
            variant="secondary"
            aria-label={t("models.searchAria")}
          />
        </div>
      </div>

      {error && (
        <div className="bg-danger-soft border border-danger/30 text-danger rounded-xl p-4 text-sm">{error}</div>
      )}

      {groups.length === 0 && (
        <div className="text-center py-20 text-muted text-sm">
          {query ? t("models.noMatch") : `${t("models.noneAvailable")} ${t("models.createSync")}`}
        </div>
      )}

      <div className="space-y-6">
        {visibleGroups.map((g) => (
          <div key={g.providerId}>
            <div className="flex items-center gap-2 mb-3">
              <button
                type="button"
                onClick={() => toggleGroup(g.providerId)}
                aria-label={g.collapsed ? t("models.expandGroup", { provider: g.providerId }) : t("models.collapseGroup", { provider: g.providerId })}
                className="flex items-center gap-2 text-left group cursor-pointer"
              >
                <Icon
                  className={`w-4 h-4 text-muted transition-transform shrink-0 ${g.collapsed ? "" : "rotate-90"}`}
                  icon="gravity-ui:chevron-right"
                />
                <Chip size="sm" color="default" className="font-mono group-hover:opacity-80 transition-opacity">{g.providerId}</Chip>
                <span className="text-xs text-muted">
                  {g.models.length} {t("models.model", { count: g.models.length })}
                  {!g.collapsed && g.shown < g.models.length ? t("models.showing", { count: g.shown }) : ""}
                </span>
              </button>
              <div className="flex gap-1 ml-auto">
                <Button size="sm" variant="secondary" onPress={() => sync(g.providerId)} isDisabled={syncing === providers.find(p => p.id === g.providerId)?.id}>
                  {t("models.sync")}
                </Button>
                <Button size="sm" variant="outline" onPress={() => openAdd(g.providerId)}>{t("models.addModel")}</Button>
              </div>
            </div>
            {!g.collapsed && (
              <>
                <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4 gap-2">
                  {g.models.slice(0, g.shown).map((m) => (
                    <ModelCard
                      key={m.id}
                      model={m}
                      stat={stats[statKey(m)] || stats[m.id]}
                      isMenuOpen={openMenuId === m.id}
                      onOpenMenu={openMenu}
                      onCloseMenu={closeMenu}
                      onToggle={toggleActive}
                      onRemove={removeModel}
                      onPricing={openPricing}
                    />
                  ))}
                </div>
                {g.capped && (
                  <div className="mt-3 flex justify-center">
                    <Button size="sm" variant="secondary" onPress={() => expandAllGroup(g.providerId)}>
                      {t("models.showAll", { count: g.models.length - g.shown })}
                    </Button>
                  </div>
                )}
              </>
            )}
          </div>
        ))}
      </div>

      <Modal isOpen={pricingOpen} onOpenChange={setPricingOpen}>
        <Modal.Backdrop>
          <Modal.Container>
            <Modal.Dialog>
              <Modal.Header><Modal.Heading>{t("models.editPriceTitle", { id: pricingModel?.id })}</Modal.Heading></Modal.Header>
              <Modal.Body className="flex flex-col gap-4">
                <TextField value={pricingForm.inputPer1M} onChange={(v) => setPricingForm({ ...pricingForm, inputPer1M: v })}>
                  <Label>{t("models.inputLabel")}</Label>
                  <Input variant="secondary" type="number" placeholder={t("models.inputPlaceholder")} step="0.01" />
                </TextField>
                <TextField value={pricingForm.outputPer1M} onChange={(v) => setPricingForm({ ...pricingForm, outputPer1M: v })}>
                  <Label>{t("models.outputLabel")}</Label>
                  <Input variant="secondary" type="number" placeholder={t("models.outputPlaceholder")} step="0.01" />
                </TextField>
                <TextField value={pricingForm.perImage} onChange={(v) => setPricingForm({ ...pricingForm, perImage: v })}>
                  <Label>{t("models.perImage")}</Label>
                  <Input variant="secondary" type="number" placeholder={t("models.perImagePlaceholder")} step="0.01" />
                </TextField>
                <p className="text-xs text-muted">
                  {t("models.priceHint")}
                </p>
              </Modal.Body>
              <Modal.Footer>
                <Button variant="primary" onPress={submitPricing}>{t("models.savePrice")}</Button>
              </Modal.Footer>
            </Modal.Dialog>
          </Modal.Container>
        </Modal.Backdrop>
      </Modal>

      <Modal isOpen={addOpen} onOpenChange={setAddOpen}>
        <Modal.Backdrop>
          <Modal.Container>
            <Modal.Dialog>
              <Modal.Header><Modal.Heading>{t("models.addTitle")}</Modal.Heading></Modal.Header>
              <Modal.Body className="flex flex-col gap-4">
                <TextField value={addForm.model_id} onChange={(v) => setAddForm({ ...addForm, model_id: v })}>
                  <Label>{t("models.modelId")}</Label>
                  <Input variant="secondary" placeholder={t("models.modelIdPlaceholder")} />
                </TextField>
                <TextField value={addForm.name} onChange={(v) => setAddForm({ ...addForm, name: v })}>
                  <Label>{t("models.addName")}</Label>
                  <Input variant="secondary" placeholder={t("models.addNamePlaceholder")} />
                </TextField>
                <div className="flex flex-col gap-1">
                  <Label>{t("models.kind")}</Label>
                  <Select aria-label={t("models.kind")} selectedKey={addForm.kind} onSelectionChange={(k) => setAddForm({ ...addForm, kind: (k as string) ?? "llm" })}>
                    <Select.Trigger className="bg-surface-secondary"><Select.Value /></Select.Trigger>
                    <Select.Popover>
                      <ListBox>{KINDS.map((k) => <ListBox.Item key={k} id={k}>{k}</ListBox.Item>)}</ListBox>
                    </Select.Popover>
                  </Select>
                </div>
                <TextField value={String(addForm.context)} onChange={(v) => setAddForm({ ...addForm, context: parseInt(v) || 0 })}>
                  <Label>{t("models.context")}</Label>
                  <Input variant="secondary" type="number" />
                </TextField>
              </Modal.Body>
              <Modal.Footer>
                <Button variant="primary" onPress={submitAdd} isDisabled={!addForm.model_id}>{t("models.addSubmit")}</Button>
              </Modal.Footer>
            </Modal.Dialog>
          </Modal.Container>
        </Modal.Backdrop>
      </Modal>
    </div>
  );
}
