import { useEffect, useState } from "react";
import {
  Table, Button, Modal, Input, Chip, TextField, Label, Description, Card, AlertDialog,
  ToggleButton, Select, ListBox,
} from "@heroui/react";
import { api, type ApiKey, type KeyLimit } from "../api";
import { IconPlus, IconTrash, IconPencil, IconApi, IconCopy, IconCheck, IconX, IconGauge, IconDollar } from "../icons";

type LimitKind = "rate" | "budget";

const DURATION_PRESETS = [
  { value: "1h", label: "1 hora" },
  { value: "5h", label: "5 horas" },
  { value: "12h", label: "12 horas" },
  { value: "24h", label: "1 dia" },
  { value: "7d", label: "7 dias" },
  { value: "30d", label: "30 dias" },
];

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

function formatLimit(l: KeyLimit): string {
  return l.kind === "rate" ? `${Math.round(l.max)} req/${l.duration}` : `$${l.max}/${l.duration}`;
}

function genId(): string {
  return typeof crypto !== "undefined" && "randomUUID" in crypto
    ? crypto.randomUUID()
    : `l-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`;
}

// LimitsCell renders up to 3 limit chips and collapses the rest into a
// "+N" chip with a full tooltip, keeping the row compact.
function LimitsCell({ limits }: { limits: KeyLimit[] }) {
  const shown = limits.slice(0, 3);
  const extra = limits.length - shown.length;
  if (limits.length === 0) {
    return <span className="text-xs text-muted">—</span>;
  }
  return (
    <div className="flex flex-wrap gap-1">
      {shown.map((l) => (
        <Chip key={l.id} size="sm" variant="soft" className="text-[10px]" title={formatLimit(l)}>
          {formatLimit(l)}
        </Chip>
      ))}
      {extra > 0 && (
        <Chip size="sm" variant="soft" className="text-[10px]" title={limits.map(formatLimit).join(", ")}>
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
  return (
    <div className="space-y-3 rounded-xl border border-border bg-surface-secondary/50 p-3">
      <div className="flex flex-wrap gap-2">
        {limits.map((l) => (
          <Chip key={l.id} size="sm" variant="soft" className="text-[11px]">
            <span className="flex items-center gap-1">
              {formatLimit(l)}
              <Button isIconOnly size="sm" variant="ghost" className="size-4 min-w-0 p-0 text-muted" onPress={() => onRemove(l.id)} aria-label={`remover ${formatLimit(l)}`}>
                <IconX className="size-3" />
              </Button>
            </span>
          </Chip>
        ))}
        {limits.length === 0 && (
          <span className="text-xs text-muted">Nenhum limite {kind === "rate" ? "de requisições" : "de gasto"}.</span>
        )}
      </div>

      <div className="flex flex-col sm:flex-row gap-2 items-start sm:items-end">
        <TextField value={draft.max} onChange={(v) => onDraftChange({ ...draft, max: v })} className="sm:w-36">
          <Label>{kind === "rate" ? "Máx. requisições" : "Máx. USD"}</Label>
          <Input type="number" min="0" step={kind === "budget" ? "0.01" : "1"} placeholder={kind === "rate" ? "ex: 100" : "ex: 10.00"} />
        </TextField>
        <div className="flex flex-col gap-1 flex-1 min-w-0">
          <Label>Duração</Label>
          {draft.customMode ? (
            <Input
              value={draft.custom}
              onChange={(e) => onDraftChange({ ...draft, custom: e.target.value })}
              placeholder="ex: 90d, 6h, 45m"
              aria-label="Duração personalizada"
            />
          ) : (
            <Select
              aria-label="Duração"
              selectedKey={draft.duration}
              onSelectionChange={(key) => {
                const v = key as string;
                if (v === "custom") onDraftChange({ ...draft, customMode: true });
                else onDraftChange({ ...draft, duration: v });
              }}
            >
              <Select.Trigger><Select.Value /></Select.Trigger>
              <Select.Popover>
                <ListBox>
                  {DURATION_PRESETS.map((d) => <ListBox.Item key={d.value} id={d.value}>{d.label}</ListBox.Item>)}
                  <ListBox.Item id="custom">Personalizado...</ListBox.Item>
                </ListBox>
              </Select.Popover>
            </Select>
          )}
        </div>
        <Button variant="secondary" onPress={onAdd} isDisabled={!parseFloat(draft.max) || !(draft.customMode ? draft.custom.trim() : draft.duration)}>
          Adicionar
        </Button>
      </div>
      <Button
        size="sm"
        variant="ghost"
        onPress={() => onDraftChange({ ...draft, customMode: !draft.customMode })}
        className="text-xs"
      >
        {draft.customMode ? "Usar presets" : "Duração personalizada"}
      </Button>
    </div>
  );
}

export default function Keys() {
  const [items, setItems] = useState<ApiKey[]>([]);
  const [loading, setLoading] = useState(true);
  const [modalOpen, setModalOpen] = useState(false);
  const [editing, setEditing] = useState<ApiKey | null>(null);
  const [formName, setFormName] = useState("");
  const [formLimits, setFormLimits] = useState<KeyLimit[]>([]);
  // features tracks which limit features are toggled ON in the modal. Each
  // feature is an independent ToggleButton; only active features show their
  // configuration section. This keeps the modal clean as more features are
  // added.
  const [features, setFeatures] = useState<{ rate: boolean; budget: boolean }>({ rate: false, budget: false });
  const [drafts, setDrafts] = useState<Record<LimitKind, Draft>>({ rate: emptyDraft(), budget: emptyDraft() });
  const [error, setError] = useState<string | null>(null);
  const [copied, setCopied] = useState<string | null>(null);
  const [copiedKeyId, setCopiedKeyId] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);
  const [endpoint, setEndpoint] = useState("/v1");
  const [endpointCopied, setEndpointCopied] = useState(false);
  const [confirmId, setConfirmId] = useState<string | null>(null);

  useEffect(() => {
    if (typeof window !== "undefined") setEndpoint(`${window.location.origin}/v1`);
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
    setFeatures({ rate: false, budget: false });
    setDrafts({ rate: emptyDraft(), budget: emptyDraft() });
    setError(null);
    setModalOpen(true);
  };

  const openEdit = (k: ApiKey) => {
    const limits = k.limits ? k.limits.map((l) => ({ ...l })) : [];
    setEditing(k);
    setFormName(k.name);
    setFormLimits(limits);
    setFeatures({
      rate: limits.some((l) => l.kind === "rate"),
      budget: limits.some((l) => l.kind === "budget"),
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
        await api.keys.update(editing.id, { name: formName, limits: formLimits });
        setModalOpen(false);
        load();
      } else {
        const k = await api.keys.create({ name: formName, limits: formLimits });
        setModalOpen(false);
        load();
        setCopied(k.key);
      }
    } catch (e: any) {
      setError(e?.message ?? "falha ao salvar");
    } finally {
      setSaving(false);
    }
  };

  const toggleFeature = (kind: LimitKind, on: boolean) => {
    setFeatures((prev) => ({ ...prev, [kind]: on }));
    // Turning a feature off clears its limits.
    if (!on) {
      setFormLimits((prev) => prev.filter((l) => l.kind !== kind));
    }
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

  const copyEndpoint = async () => {
    try { await navigator.clipboard.writeText(endpoint); setEndpointCopied(true); setTimeout(() => setEndpointCopied(false), 1500); } catch {}
  };

  return (
    <div className="space-y-5">
      <div className="flex justify-between items-center">
        <div>
          <h1 className="text-2xl font-bold tracking-tight">API Keys</h1>
          <p className="text-sm text-muted mt-0.5">{items.length} chaves cadastradas</p>
        </div>
        <Button variant="outline" onPress={openCreate}><IconPlus className="w-4 h-4" /> Nova chave</Button>
      </div>

      <Card className="p-5">
        <div className="flex items-center gap-2 mb-3">
          <IconApi className="w-4 h-4" />
          <h2 className="text-base font-semibold">API Endpoint</h2>
        </div>
        <p className="text-xs text-muted mb-3">
          Aponte seu cliente (Claude Code, Cursor, Codex, Cline...) para este endpoint.
        </p>
        <div className="flex items-center gap-2">
          <Chip size="sm" variant="soft" className="shrink-0 min-w-[70px] justify-center font-mono text-xs">Local</Chip>
          <Input value={endpoint} readOnly className="flex-1 font-mono text-sm" aria-label="API Endpoint" />
          <Button
            isIconOnly
            variant="secondary"
            onPress={copyEndpoint}
            aria-label="copiar endpoint"
            className={endpointCopied ? "text-success" : ""}
          >
            {endpointCopied ? <IconCheck className="w-4 h-4 text-success" /> : <IconCopy className="w-4 h-4" />}
          </Button>
        </div>
      </Card>

      <Card className="overflow-hidden">
        {loading ? (
          <div className="p-10 text-center text-muted text-sm">Carregando...</div>
        ) : items.length === 0 ? (
          <div className="p-10 text-center text-muted text-sm">
            Nenhuma chave ainda. Clique em <strong>Nova chave</strong>.
          </div>
        ) : (
          <Table>
            <Table.ScrollContainer>
              <Table.Content aria-label="keys" className="min-w-[760px]">
                <Table.Header>
                  <Table.Column isRowHeader id="name">Nome</Table.Column>
                  <Table.Column id="key">Chave</Table.Column>
                  <Table.Column id="limits">Limites</Table.Column>
                  <Table.Column id="status">Status</Table.Column>
                  <Table.Column id="created">Criada</Table.Column>
                  <Table.Column id="actions">Ações</Table.Column>
                </Table.Header>
                <Table.Body items={items}>
                  {(k) => (
                    <Table.Row key={k.id} id={k.id}>
                      <Table.Cell><span className="font-medium">{k.name}</span></Table.Cell>
                      <Table.Cell>
                        <div
                          className="inline-flex items-center gap-1.5 cursor-pointer hover:bg-default-soft rounded-lg px-2 py-1 transition-colors group"
                          onClick={() => copyKey(k)}
                          title="Clique para copiar"
                        >
                          <code className="text-xs text-muted group-hover:text-accent transition-colors">{k.key.slice(0, 10)}…{k.key.slice(-6)}</code>
                          {copiedKeyId === k.id ? <IconCheck className="text-success shrink-0 w-3 h-3" /> : <IconCopy className="w-3 h-3 text-muted/70 shrink-0 group-hover:text-accent transition-colors" />}
                        </div>
                      </Table.Cell>
                      <Table.Cell><LimitsCell limits={k.limits || []} /></Table.Cell>
                      <Table.Cell>
                        <Chip size="sm" variant="soft" color={k.is_active ? "success" : "default"}>
                          {k.is_active ? "ativo" : "inativo"}
                        </Chip>
                      </Table.Cell>
                      <Table.Cell><span className="text-xs text-muted">{new Date(k.created_at).toLocaleDateString()}</span></Table.Cell>
                      <Table.Cell>
                        <div className="flex gap-1 justify-end">
                          <Button size="sm" variant="secondary" onPress={() => openEdit(k)} aria-label={`editar ${k.name}`}>
                            <IconPencil className="w-3.5 h-3.5" /> Editar
                          </Button>
                          <Button size="sm" variant="secondary" onPress={() => toggleActive(k)}>
                            {k.is_active ? "Desativar" : "Ativar"}
                          </Button>
                          <Button isIconOnly size="sm" variant="ghost" className="text-danger" onPress={() => setConfirmId(k.id)} aria-label="excluir"><IconTrash className="w-4 h-4" /></Button>
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
                <Modal.Heading>{editing ? `Editar API Key — ${editing.name}` : "Nova API Key"}</Modal.Heading>
              </Modal.Header>
              <Modal.Body className="gap-4">
                <TextField value={formName} onChange={setFormName}>
                  <Label>Nome</Label>
                  <Input placeholder="ex: dev, prod, mobile" />
                </TextField>

                <div className="flex flex-col gap-1">
                  <Label>Limites</Label>
                  <div className="flex flex-wrap gap-2">
                    <ToggleButton isSelected={features.rate} onChange={(v) => toggleFeature("rate", v)}>
                      <IconGauge className="w-4 h-4" /> Rate Limit
                    </ToggleButton>
                    <ToggleButton isSelected={features.budget} onChange={(v) => toggleFeature("budget", v)}>
                      <IconDollar className="w-4 h-4" /> Budget Limit
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
                  Ative as features que quiser e configure seus limites. A key é bloqueada se <strong>qualquer</strong> limite exceder. Limites são janelas deslizantes.
                </Description>

                {error && <p className="text-sm text-danger">{error}</p>}
              </Modal.Body>
              <Modal.Footer>
                <Button variant="secondary" onPress={() => setModalOpen(false)}>Cancelar</Button>
                <Button variant="primary" onPress={save} isDisabled={saving || !formName}>{editing ? "Salvar" : "Criar"}</Button>
              </Modal.Footer>
            </Modal.Dialog>
          </Modal.Container>
        </Modal.Backdrop>
      </Modal>

      <Modal isOpen={!!copied} onOpenChange={(o) => { if (!o) setCopied(null); }}>
        <Modal.Backdrop>
          <Modal.Container>
            <Modal.Dialog>
              <Modal.Header><Modal.Heading>Chave criada</Modal.Heading></Modal.Header>
              <Modal.Body>
                <p className="text-sm text-warning">Copie agora — não será mostrada novamente.</p>
                <code className="block bg-surface-secondary p-3 rounded-lg font-mono text-xs break-all border border-border mt-3">{copied}</code>
              </Modal.Body>
              <Modal.Footer>
                <Button variant="primary" onPress={() => navigator.clipboard.writeText(copied || "")}>Copiar</Button>
                <Button variant="secondary" onPress={() => setCopied(null)}>Fechar</Button>
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
                <AlertDialog.Heading>Remover esta chave?</AlertDialog.Heading>
              </AlertDialog.Header>
              <AlertDialog.Body>
                <p>Apps que usam esta chave perderão acesso ao gateway.</p>
              </AlertDialog.Body>
              <AlertDialog.Footer>
                <Button slot="close" variant="tertiary">Cancelar</Button>
                <Button slot="close" variant="danger" onPress={() => { if (confirmId) remove(confirmId); setConfirmId(null); }}>Remover</Button>
              </AlertDialog.Footer>
            </AlertDialog.Dialog>
          </AlertDialog.Container>
        </AlertDialog.Backdrop>
      </AlertDialog>
    </div>
  );
}
