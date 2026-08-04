import { useEffect, useState } from "react";
import {
  Table, Button, Modal, Input, Chip, TextField, Label, Description, Card, AlertDialog,
  ToggleButton, Select, ListBox,
} from "@heroui/react";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
import { api, type ApiKey, type KeyLimit } from "../api";
import { ModelComboBox, type ModelComboBoxItem } from "../components/ModelComboBox";
import { IconPlus, IconTrash, IconPencil, IconApi, IconCopy, IconCheck, IconX, IconGauge, IconDollar, IconBox } from "../icons";

type LimitKind = "rate" | "budget";

const DURATION_PRESETS = ["1m", "1h", "5h", "12h", "24h", "7d", "30d"];

// Draft holds the "add a limit" form inputs for one feature section. Each
// active feature has its own draft so Rate and Budget can be configured
// independently and both visible at once.
interface Draft {
  max: string;
  duration: string;
  custom: string;
  customMode: boolean;
}

const emptyDraft = (): Draft => ({ max: "", duration: "1h", custom: "", customMode: false });

function formatLimit(l: KeyLimit, t: TFunction): string {
  return l.kind === "rate" ? `${Math.round(l.max)} ${t("keys.perReq")}${l.duration}` : `$${l.max}/${l.duration}`;
}

function genId(): string {
  return typeof crypto !== "undefined" && "randomUUID" in crypto
    ? crypto.randomUUID()
    : `l-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`;
}

// LimitsCell renders up to 3 limit chips and collapses the rest into a
// "+N" chip with a full tooltip, keeping the row compact.
function LimitsCell({ limits }: { limits: KeyLimit[] }) {
  const { t } = useTranslation();
  const shown = limits.slice(0, 3);
  const extra = limits.length - shown.length;
  if (limits.length === 0) {
    return <span className="text-xs text-muted">—</span>;
  }
  return (
    <div className="flex flex-wrap gap-1">
      {shown.map((l) => (
        <Chip key={l.id} size="sm" variant="soft" className="text-[10px]" title={formatLimit(l, t)}>
          {formatLimit(l, t)}
        </Chip>
      ))}
      {extra > 0 && (
        <Chip size="sm" variant="soft" className="text-[10px]" title={limits.map((l) => formatLimit(l, t)).join(", ")}>
          +{extra}
        </Chip>
      )}
    </div>
  );
}

// LimitEditor renders the configuration section for a single feature: the
// list of configured limits (with remove) and the "add a limit" row.
function LimitEditor({ kind, limits, draft, onDraftChange, onAdd, onRemove }: {
  kind: LimitKind;
  limits: KeyLimit[];
  draft: Draft;
  onDraftChange: (d: Draft) => void;
  onAdd: () => void;
  onRemove: (id: string) => void;
}) {
  const { t } = useTranslation();
  const durLabel = (v: string): string => {
    const durKeys: Record<string, string> = {
      "1m": "min1",
      "1h": "hour1",
      "5h": "hours5",
      "12h": "hours12",
      "24h": "day1",
      "7d": "days7",
      "30d": "days30",
    };
    return t(`keys.durPresets.${durKeys[v] ?? "custom"}`);
  };
  return (
    <div className="space-y-3 rounded-xl border border-border bg-surface-secondary/50 p-3">
      <div className="flex flex-wrap gap-2">
        {limits.map((l) => (
          <Chip key={l.id} size="sm" variant="soft" className="text-[11px]">
            <span className="flex items-center gap-1">
              {formatLimit(l, t)}
              <Button isIconOnly size="sm" variant="ghost" className="size-4 min-w-0 p-0 text-muted" onPress={() => onRemove(l.id)} aria-label={t("keys.removeLimitAria", { limit: formatLimit(l, t) })}>
                <IconX className="size-3" />
              </Button>
            </span>
          </Chip>
        ))}
        {limits.length === 0 && (
          <span className="text-xs text-muted">{kind === "rate" ? t("keys.limitEmptyRate") : t("keys.limitEmptyBudget")}</span>
        )}
      </div>

      <div className="flex flex-col sm:flex-row gap-2 items-start sm:items-end">
        <TextField value={draft.max} onChange={(v) => onDraftChange({ ...draft, max: v })} className="sm:w-36">
          <Label>{kind === "rate" ? t("keys.maxRate") : t("keys.maxBudget")}</Label>
          <Input variant="secondary" type="number" min="0" step={kind === "budget" ? "0.01" : "1"} placeholder={kind === "rate" ? t("keys.maxRatePlaceholder") : t("keys.maxBudgetPlaceholder")} />
        </TextField>
        <div className="flex flex-col gap-1 flex-1 min-w-0">
          <Label>{t("keys.duration")}</Label>
          {draft.customMode ? (
            <Input
              value={draft.custom}
              onChange={(e) => onDraftChange({ ...draft, custom: e.target.value })}
              placeholder={t("keys.customPlaceholder")}
               variant="secondary"
               aria-label={t("keys.customAria")}
            />
          ) : (
            <Select
              aria-label={t("keys.duration")}
              selectedKey={draft.duration}
              onSelectionChange={(key) => {
                const v = key as string;
                if (v === "custom") onDraftChange({ ...draft, customMode: true });
                else onDraftChange({ ...draft, duration: v });
              }}
            >
              <Select.Trigger className="bg-surface-secondary"><Select.Value /></Select.Trigger>
              <Select.Popover>
                <ListBox>
                  {DURATION_PRESETS.map((d) => <ListBox.Item key={d} id={d}>{durLabel(d)}</ListBox.Item>)}
                  <ListBox.Item id="custom">{t("keys.durPresets.custom")}</ListBox.Item>
                </ListBox>
              </Select.Popover>
            </Select>
          )}
        </div>
        <Button variant="secondary" onPress={onAdd} isDisabled={!parseFloat(draft.max) || !(draft.customMode ? draft.custom.trim() : draft.duration)}>
          {t("keys.addLimit")}
        </Button>
      </div>
      <Button
        size="sm"
        variant="ghost"
        onPress={() => onDraftChange({ ...draft, customMode: !draft.customMode })}
        className="text-xs"
      >
        {draft.customMode ? t("keys.usePresets") : t("keys.customDuration")}
      </Button>
    </div>
  );
}

export default function Keys() {
  const { t } = useTranslation();
  const [items, setItems] = useState<ApiKey[]>([]);
  const [loading, setLoading] = useState(true);
  const [modalOpen, setModalOpen] = useState(false);
  const [editing, setEditing] = useState<ApiKey | null>(null);
  const [formName, setFormName] = useState("");
  const [formLimits, setFormLimits] = useState<KeyLimit[]>([]);
  const [formAllowed, setFormAllowed] = useState<string[]>([]);
  // features tracks which limit features are toggled ON in the modal. Each
  // feature is an independent ToggleButton; only active features show their
  // configuration section. This keeps the modal clean as more features are
  // added.
  const [features, setFeatures] = useState<{ rate: boolean; budget: boolean; allowedModels: boolean }>({ rate: false, budget: false, allowedModels: false });
  const [drafts, setDrafts] = useState<Record<LimitKind, Draft>>({ rate: emptyDraft(), budget: emptyDraft() });
  const [error, setError] = useState<string | null>(null);
  const [copied, setCopied] = useState<string | null>(null);
  const [copiedKeyId, setCopiedKeyId] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);
  const [endpoint, setEndpoint] = useState("");
  const [endpointCopied, setEndpointCopied] = useState<string | null>(null);
  const [confirmId, setConfirmId] = useState<string | null>(null);
  const [modelItems, setModelItems] = useState<ModelComboBoxItem[]>([]);

  useEffect(() => {
    api.models.list()
      .then((ms) => setModelItems(ms.filter((m) => m.owned_by !== "combo").map((m) => ({ id: m.id, itemType: "model", kind: m.kind || "llm", isActive: true }))))
      .catch(() => {});
  }, []);

  useEffect(() => {
    if (typeof window !== "undefined") setEndpoint(window.location.origin);
  }, []);

  const load = () => {
    setLoading(true);
    api.keys.list().then(setItems).catch(() => setItems([])).finally(() => setLoading(false));
  };
  useEffect(load, []);

  const openCreate = () => {
    setEditing(null);
    setFormName("");
    setFormLimits([]);
    setFormAllowed([]);
    setFeatures({ rate: false, budget: false, allowedModels: false });
    setDrafts({ rate: emptyDraft(), budget: emptyDraft() });
    setError(null);
    setModalOpen(true);
  };

  const openEdit = (k: ApiKey) => {
    const limits = k.limits ? k.limits.map((l) => ({ ...l })) : [];
    setEditing(k);
    setFormName(k.name);
    setFormLimits(limits);
    setFormAllowed(k.allowed_models || []);
    setFeatures({
      rate: limits.some((l) => l.kind === "rate"),
      budget: limits.some((l) => l.kind === "budget"),
      allowedModels: !!(k.allowed_models && k.allowed_models.length),
    });
    setDrafts({ rate: emptyDraft(), budget: emptyDraft() });
    setError(null);
    setModalOpen(true);
  };

  const save = async () => {
    setSaving(true);
    setError(null);
    try {
      if (editing) {
        await api.keys.update(editing.id, { name: formName, limits: formLimits, allowed_models: formAllowed });
        setModalOpen(false);
        load();
      } else {
        const k = await api.keys.create({ name: formName, limits: formLimits, allowed_models: formAllowed });
        setModalOpen(false);
        load();
        setCopied(k.key);
      }
    } catch (e: any) {
      setError(e?.message ?? t("keys.saveError"));
    } finally {
      setSaving(false);
    }
  };

  const toggleFeature = (kind: LimitKind | "allowedModels", on: boolean) => {
    setFeatures((prev) => ({ ...prev, [kind]: on }));
    // Turning a feature off clears its configuration.
    if (!on) {
      if (kind === "allowedModels") setFormAllowed([]);
      else setFormLimits((prev) => prev.filter((l) => l.kind !== kind));
    }
  };

  const toggleAllowedModel = (id: string) => {
    setFormAllowed((prev) => (prev.includes(id) ? prev.filter((m) => m !== id) : [...prev, id]));
  };

  const addLimit = (kind: LimitKind) => {
    const d = drafts[kind];
    const max = parseFloat(d.max);
    const duration = d.customMode ? d.custom.trim() : d.duration;
    if (!max || max <= 0 || !duration) return;
    setFormLimits((prev) => [...prev, { id: genId(), kind, max, duration }]);
    setDrafts((prev) => ({ ...prev, [kind]: emptyDraft() }));
  };

  const removeLimit = (id: string) => {
    setFormLimits((prev) => prev.filter((l) => l.id !== id));
  };

  const remove = async (id: string) => {
    await api.keys.remove(id); load();
  };

  const toggleActive = async (k: ApiKey) => {
    await api.keys.update(k.id, { is_active: !k.is_active });
    load();
  };

  const copyKey = async (k: ApiKey) => {
    try {
      await navigator.clipboard.writeText(k.key);
      setCopiedKeyId(k.id);
      setTimeout(() => setCopiedKeyId(null), 1500);
    } catch {}
  };

  const copyToClipboard = async (text: string, key: string) => {
    try { await navigator.clipboard.writeText(text); setEndpointCopied(key); setTimeout(() => setEndpointCopied(null), 1500); } catch {}
  };

  return (
    <div className="space-y-5">
      <div className="flex justify-between items-center">
        <div>
          <h1 className="text-2xl font-bold tracking-tight">{t("keys.title")}</h1>
          <p className="text-sm text-muted mt-0.5">{t("keys.subtitle", { count: items.length })}</p>
        </div>
        <Button variant="outline" onPress={openCreate}><IconPlus className="w-4 h-4" /> {t("keys.new")}</Button>
      </div>

      <Card className="p-5">
        <div className="flex items-center gap-2 mb-3">
          <IconApi className="w-4 h-4" />
          <h2 className="text-base font-semibold">{t("keys.endpointTitle")}</h2>
        </div>
        <p className="text-xs text-muted mb-4">
          {t("keys.endpointDesc")}
        </p>
        <div className="space-y-3">
          <div>
            <div className="flex items-center gap-2 mb-1.5">
              <Chip size="sm" variant="soft" color="accent" className="shrink-0 font-mono text-[11px]">POST</Chip>
              <span className="text-xs font-medium">{t("keys.endpointChat")}</span>
              <span className="text-[11px] text-muted">{t("keys.endpointChatDesc")}</span>
            </div>
            <div className="flex items-center gap-2">
              <code className="flex-1 bg-surface-secondary px-3 py-2 rounded-lg font-mono text-xs border border-border break-all">{endpoint}/v1/chat/completions</code>
              <Button
                isIconOnly
                variant="secondary"
                onPress={() => copyToClipboard(`${endpoint}/v1/chat/completions`, "chat")}
                aria-label={t("keys.copyEndpointAria")}
                className={endpointCopied === "chat" ? "text-success" : ""}
              >
                {endpointCopied === "chat" ? <IconCheck className="w-4 h-4 text-success" /> : <IconCopy className="w-4 h-4" />}
              </Button>
            </div>
          </div>
          <div>
            <div className="flex items-center gap-2 mb-1.5">
              <Chip size="sm" variant="soft" color="accent" className="shrink-0 font-mono text-[11px]">POST</Chip>
              <span className="text-xs font-medium">{t("keys.endpointResponses")}</span>
              <span className="text-[11px] text-muted">{t("keys.endpointResponsesDesc")}</span>
            </div>
            <div className="flex items-center gap-2">
              <code className="flex-1 bg-surface-secondary px-3 py-2 rounded-lg font-mono text-xs border border-border break-all">{endpoint}/v1/responses</code>
              <Button
                isIconOnly
                variant="secondary"
                onPress={() => copyToClipboard(`${endpoint}/v1/responses`, "responses")}
                aria-label={t("keys.copyEndpointAria")}
                className={endpointCopied === "responses" ? "text-success" : ""}
              >
                {endpointCopied === "responses" ? <IconCheck className="w-4 h-4 text-success" /> : <IconCopy className="w-4 h-4" />}
              </Button>
            </div>
          </div>
          <div>
            <div className="flex items-center gap-2 mb-1.5">
              <Chip size="sm" variant="soft" color="default" className="shrink-0 font-mono text-[11px]">POST</Chip>
              <span className="text-xs font-medium">{t("keys.endpointMessages")}</span>
              <span className="text-[11px] text-muted">{t("keys.endpointMessagesDesc")}</span>
            </div>
            <div className="flex items-center gap-2">
              <code className="flex-1 bg-surface-secondary px-3 py-2 rounded-lg font-mono text-xs border border-border break-all">{endpoint}/v1/messages</code>
              <Button
                isIconOnly
                variant="secondary"
                onPress={() => copyToClipboard(`${endpoint}/v1/messages`, "messages")}
                aria-label={t("keys.copyEndpointAria")}
                className={endpointCopied === "messages" ? "text-success" : ""}
              >
                {endpointCopied === "messages" ? <IconCheck className="w-4 h-4 text-success" /> : <IconCopy className="w-4 h-4" />}
              </Button>
            </div>
          </div>
        </div>
      </Card>

      <Card className="overflow-hidden">
        {loading ? (
          <div className="p-10 text-center text-muted text-sm">{t("keys.loading")}</div>
        ) : items.length === 0 ? (
          <div className="p-10 text-center text-muted text-sm">
            {t("keys.empty")} <strong>{t("keys.new")}</strong>.
          </div>
        ) : (
          <Table>
            <Table.ScrollContainer>
              <Table.Content aria-label={t("keys.title")} className="min-w-[760px]">
                <Table.Header>
                  <Table.Column isRowHeader id="name">{t("keys.colName")}</Table.Column>
                  <Table.Column id="key">{t("keys.colKey")}</Table.Column>
                  <Table.Column id="models">{t("keys.colModels")}</Table.Column>
                  <Table.Column id="limits">{t("keys.colLimits")}</Table.Column>
                  <Table.Column id="status">{t("keys.colStatus")}</Table.Column>
                  <Table.Column id="created">{t("keys.colCreated")}</Table.Column>
                  <Table.Column id="actions">{t("keys.colActions")}</Table.Column>
                </Table.Header>
                <Table.Body items={items}>
                  {(k) => (
                    <Table.Row key={k.id} id={k.id}>
                      <Table.Cell><span className="font-medium">{k.name}</span></Table.Cell>
                      <Table.Cell>
                        <div
                          className="inline-flex items-center gap-1.5 cursor-pointer hover:bg-default-soft rounded-lg px-2 py-1 transition-colors group"
                          onClick={() => copyKey(k)}
                          title={t("keys.copyTooltip")}
                        >
                          <code className="text-xs text-muted group-hover:text-accent transition-colors">{k.key.slice(0, 10)}…{k.key.slice(-6)}</code>
                          {copiedKeyId === k.id ? <IconCheck className="text-success shrink-0 w-3 h-3" /> : <IconCopy className="w-3 h-3 text-muted/70 shrink-0 group-hover:text-accent transition-colors" />}
                        </div>
                      </Table.Cell>
                      <Table.Cell><LimitsCell limits={k.limits || []} /></Table.Cell>
                      <Table.Cell>
                        {k.allowed_models && k.allowed_models.length > 0 ? (
                          <div className="flex flex-wrap gap-1 max-w-[220px]">
                            {k.allowed_models.slice(0, 3).map((m) => (
                              <Chip key={m} size="sm" variant="soft" className="text-[10px]">{m}</Chip>
                            ))}
                            {k.allowed_models.length > 3 && <Chip size="sm" variant="soft" className="text-[10px]">+{k.allowed_models.length - 3}</Chip>}
                          </div>
                        ) : (
                          <span className="text-xs text-muted">{t("keys.all")}</span>
                        )}
                      </Table.Cell>
                      <Table.Cell>
                        <Chip size="sm" variant="soft" color={k.is_active ? "success" : "default"}>
                          {k.is_active ? t("keys.active") : t("keys.inactive")}
                        </Chip>
                      </Table.Cell>
                      <Table.Cell><span className="text-xs text-muted">{new Date(k.created_at).toLocaleDateString()}</span></Table.Cell>
                      <Table.Cell>
                        <div className="flex gap-1 justify-end">
                          <Button size="sm" variant="secondary" onPress={() => openEdit(k)} aria-label={t("keys.editAria", { name: k.name })}>
                            <IconPencil className="w-3.5 h-3.5" /> {t("keys.edit")}
                          </Button>
                          <Button size="sm" variant="secondary" onPress={() => toggleActive(k)}>
                            {k.is_active ? t("keys.deactivate") : t("keys.activate")}
                          </Button>
                          <Button isIconOnly size="sm" variant="ghost" className="text-danger" onPress={() => setConfirmId(k.id)} aria-label={t("keys.deleteAria")}><IconTrash className="w-4 h-4" /></Button>
                        </div>
                      </Table.Cell>
                    </Table.Row>
                  )}
                </Table.Body>
              </Table.Content>
            </Table.ScrollContainer>
          </Table>
        )}
      </Card>

      <Modal isOpen={modalOpen} onOpenChange={(o) => { if (!o) setModalOpen(false); }}>
        <Modal.Backdrop>
          <Modal.Container>
            <Modal.Dialog>
              <Modal.Header>
                <Modal.Heading>{editing ? t("keys.editModal", { name: editing.name }) : t("keys.createModal")}</Modal.Heading>
              </Modal.Header>
              <Modal.Body className="flex flex-col gap-4">
                <TextField value={formName} onChange={setFormName}>
                  <Label>{t("keys.name")}</Label>
                  <Input variant="secondary" placeholder={t("keys.namePlaceholder")} />
                </TextField>

                <div className="flex flex-col gap-1">
                  <Label>{t("keys.accessLabel")}</Label>
                  <ToggleButton isSelected={features.allowedModels} onChange={(v) => toggleFeature("allowedModels", v)}>
                    <IconBox className="w-4 h-4" /> {t("keys.allowedModels")}
                  </ToggleButton>
                </div>

                {features.allowedModels && (
                  <div className="flex flex-col gap-2">
                    {formAllowed.length > 0 && (
                      <div className="flex flex-wrap gap-1.5">
                        {formAllowed.map((m) => (
                          <Chip key={m} size="sm" variant="soft">
                            <span className="flex items-center gap-1">
                              {m}
                              <button
                                className="text-muted hover:text-danger transition-colors"
                                onClick={() => toggleAllowedModel(m)}
                                aria-label={t("keys.removeModelAria", { model: m })}
                              >
                                <IconX className="w-3 h-3" />
                              </button>
                            </span>
                          </Chip>
                        ))}
                      </div>
                    )}
                    <ModelComboBox
                      items={modelItems.filter((i) => !formAllowed.includes(i.id))}
                      ariaLabel={t("keys.addModelAria")}
                      inputPlaceholder={t("keys.addModelPlaceholder")}
                      inputClassName="text-xs"
                      selectedKey={null}
                      onSelectionChange={toggleAllowedModel}
                    />
                    <Description>
                      {t("keys.allowedDesc")}
                    </Description>
                  </div>
                )}

                <div className="flex flex-col gap-1">
                  <Label>{t("keys.limitsLabel")}</Label>
                  <div className="flex flex-wrap gap-2">
                    <ToggleButton isSelected={features.rate} onChange={(v) => toggleFeature("rate", v)}>
                      <IconGauge className="w-4 h-4" /> {t("keys.rateLimit")}
                    </ToggleButton>
                    <ToggleButton isSelected={features.budget} onChange={(v) => toggleFeature("budget", v)}>
                      <IconDollar className="w-4 h-4" /> {t("keys.budgetLimit")}
                    </ToggleButton>
                  </div>
                </div>

                {features.rate && (
                  <LimitEditor
                    kind="rate"
                    limits={formLimits.filter((l) => l.kind === "rate")}
                    draft={drafts.rate}
                    onDraftChange={(d) => setDrafts((prev) => ({ ...prev, rate: d }))}
                    onAdd={() => addLimit("rate")}
                    onRemove={removeLimit}
                  />
                )}
                {features.budget && (
                  <LimitEditor
                    kind="budget"
                    limits={formLimits.filter((l) => l.kind === "budget")}
                    draft={drafts.budget}
                    onDraftChange={(d) => setDrafts((prev) => ({ ...prev, budget: d }))}
                    onAdd={() => addLimit("budget")}
                    onRemove={removeLimit}
                  />
                )}

                <Description>
                  {t("keys.limitsDesc", { any: <strong>{t("keys.any")}</strong> })}
                </Description>

                {error && <p className="text-sm text-danger">{error}</p>}
              </Modal.Body>
              <Modal.Footer>
                <Button variant="secondary" onPress={() => setModalOpen(false)}>{t("keys.cancel")}</Button>
                <Button variant="primary" onPress={save} isDisabled={saving || !formName}>{editing ? t("keys.save") : t("keys.create")}</Button>
              </Modal.Footer>
            </Modal.Dialog>
          </Modal.Container>
        </Modal.Backdrop>
      </Modal>

      <Modal isOpen={!!copied} onOpenChange={(o) => { if (!o) setCopied(null); }}>
        <Modal.Backdrop>
          <Modal.Container>
            <Modal.Dialog>
              <Modal.Header><Modal.Heading>{t("keys.createdTitle")}</Modal.Heading></Modal.Header>
              <Modal.Body>
                <p className="text-sm text-warning">{t("keys.createdBody")}</p>
                <code className="block bg-surface-secondary p-3 rounded-lg font-mono text-xs break-all border border-border mt-3">{copied}</code>
              </Modal.Body>
              <Modal.Footer>
                <Button variant="primary" onPress={() => navigator.clipboard.writeText(copied || "")}>{t("keys.copy")}</Button>
                <Button variant="secondary" onPress={() => setCopied(null)}>{t("keys.close")}</Button>
              </Modal.Footer>
            </Modal.Dialog>
          </Modal.Container>
        </Modal.Backdrop>
      </Modal>

      <AlertDialog>
        <AlertDialog.Backdrop isOpen={!!confirmId} onOpenChange={(o) => !o && setConfirmId(null)}>
          <AlertDialog.Container>
            <AlertDialog.Dialog className="sm:max-w-[400px]">
              <AlertDialog.CloseTrigger />
              <AlertDialog.Header>
                <AlertDialog.Icon status="danger" />
                <AlertDialog.Heading>{t("keys.removeTitle")}</AlertDialog.Heading>
              </AlertDialog.Header>
              <AlertDialog.Body>
                <p>{t("keys.removeBody")}</p>
              </AlertDialog.Body>
              <AlertDialog.Footer>
                <Button slot="close" variant="tertiary">{t("keys.cancel")}</Button>
                <Button slot="close" variant="danger" onPress={() => { if (confirmId) remove(confirmId); setConfirmId(null); }}>{t("keys.remove")}</Button>
              </AlertDialog.Footer>
            </AlertDialog.Dialog>
          </AlertDialog.Container>
        </AlertDialog.Backdrop>
      </AlertDialog>
    </div>
  );
}
