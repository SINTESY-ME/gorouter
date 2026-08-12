import { useEffect, useState, useMemo } from "react";
import {
  Table, Button, Modal, Input, Select, ListBox, Chip, Spinner, TextField, Label, TextArea,
  Switch, AlertDialog, toast,
} from "@heroui/react";
import { useTranslation } from "react-i18next";
import { api, type MCPClient, type MCPToolDef } from "../api";
import { IconPlus, IconPencil, IconTrash, IconPlug, IconPower, IconChevron } from "../icons";

const CONN_TYPES = ["http", "sse", "stdio"];
const AUTH_TYPES = ["none", "bearer"];

const emptyClient = {
  name: "",
  connection_type: "http" as const,
  url: "",
  headers: {} as Record<string, string>,
  stdio_command: "",
  stdio_args: [] as string[],
  auth_type: "none" as const,
  auth_token: "",
  tools_to_execute: ["*"] as string[],
  enabled: true,
  sync_seconds: 0,
};

function stateColor(state?: string) {
  switch (state) {
    case "connected": return "success" as const;
    case "error": return "danger" as const;
    case "disabled": return "default" as const;
    default: return "warning" as const;
  }
}

export default function MCP() {
  const { t } = useTranslation();
  const [clients, setClients] = useState<MCPClient[]>([]);
  const [tools, setTools] = useState<MCPToolDef[]>([]);
  const [loading, setLoading] = useState(true);

  const [open, setOpen] = useState(false);
  const [form, setForm] = useState<Record<string, any>>(emptyClient);
  const [editId, setEditId] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const [confirmId, setConfirmId] = useState<string | null>(null);
  const [expandedTools, setExpandedTools] = useState<string | null>(null);
  const [toolsCache, setToolsCache] = useState<Record<string, MCPToolDef[]>>({});

  const loadData = () => {
    setLoading(true);
    Promise.all([api.mcpClients.list(), api.mcpClients.tools()])
      .then(([c, ts]) => { setClients(c); setTools(ts); })
      .catch(() => { setClients([]); setTools([]); })
      .finally(() => setLoading(false));
  };

  useEffect(() => { loadData(); }, []);

  const openNew = () => {
    setForm({ ...emptyClient, headers: {}, tools_to_execute: ["*"] });
    setEditId(null);
    setError("");
    setOpen(true);
  };

  const openEdit = (c: MCPClient) => {
    setForm({
      name: c.name,
      connection_type: c.connection_type || "http",
      url: c.url || "",
      headers: c.headers || {},
      stdio_command: c.stdio_command || "",
      stdio_args: c.stdio_args || [],
      auth_type: c.auth_type || "none",
      auth_token: "",
      tools_to_execute: c.tools_to_execute?.length ? c.tools_to_execute : ["*"],
      enabled: c.enabled,
      sync_seconds: c.sync_seconds || 0,
    });
    setEditId(c.id);
    setError("");
    setOpen(true);
  };

  const submit = async () => {
    setSaving(true);
    setError("");
    try {
      const payload: Record<string, any> = {
        name: form.name,
        connection_type: form.connection_type,
        headers: form.headers,
        auth_type: form.auth_type,
        enabled: form.enabled,
        sync_seconds: Number(form.sync_seconds) || 0,
      };
      if (form.connection_type === "stdio") {
        payload.stdio_command = form.stdio_command;
        payload.stdio_args = (form.stdio_args_text || "").split(/\s+/).filter(Boolean);
      } else {
        payload.url = form.url;
      }
      if (form.auth_type === "bearer" && form.auth_token) payload.auth_token = form.auth_token;
      const allow = (form.tools_allow_text || "*").split(",").map((s: string) => s.trim()).filter(Boolean);
      payload.tools_to_execute = allow.length ? allow : ["*"];

      if (editId) await api.mcpClients.update(editId, payload);
      else await api.mcpClients.create(payload);
      setOpen(false);
      loadData();
    } catch (e: any) {
      setError(e?.message ?? t("mcp.saveFailed"));
    } finally {
      setSaving(false);
    }
  };

  const remove = async (id: string) => {
    try {
      await api.mcpClients.remove(id);
      loadData();
    } catch (e: any) {
      toast.error(e?.message ?? t("mcp.deleteFailed"));
    }
    setConfirmId(null);
  };

  const toggleEnabled = async (c: MCPClient) => {
    try {
      if (c.enabled) await api.mcpClients.disable(c.id);
      else await api.mcpClients.enable(c.id);
      loadData();
    } catch (e: any) {
      toast.error(e?.message ?? t("mcp.toggleFailed"));
    }
  };

  const reconnect = async (id: string) => {
    try {
      await api.mcpClients.reconnect(id);
      loadData();
      toast.success(t("mcp.reconnected"));
    } catch (e: any) {
      toast.error(e?.message ?? t("mcp.reconnectFailed"));
    }
  };

  const toggleTools = async (c: MCPClient) => {
    if (expandedTools === c.id) {
      setExpandedTools(null);
      return;
    }
    setExpandedTools(c.id);
    if (toolsCache[c.id]) return;
    try {
      const all = await api.mcpClients.tools();
      setToolsCache((cache) => ({ ...cache, [c.id]: all.filter((x) => x.name.startsWith(c.name + "__")) }));
    } catch { /* ignore */ }
  };

  const groupedTools = useMemo(() => {
    const groups: Record<string, MCPToolDef[]> = {};
    tools.forEach((x) => {
      const idx = x.name.indexOf("__");
      const client = idx > 0 ? x.name.slice(0, idx) : "?";
      if (!groups[client]) groups[client] = [];
      groups[client].push(x);
    });
    return groups;
  }, [tools]);

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-xl font-semibold">{t("mcp.title")}</h1>
          <p className="text-sm text-muted">{t("mcp.subtitle")}</p>
        </div>
        <Button variant="primary" onPress={openNew} startContent={<IconPlus className="w-4 h-4" />}>
          {t("mcp.addClient")}
        </Button>
      </div>

      {loading ? (
        <div className="flex justify-center py-16"><Spinner /></div>
      ) : clients.length === 0 ? (
        <div className="bg-surface rounded-2xl border border-border p-12 text-center space-y-2">
          <IconPlug className="w-8 h-8 text-muted mx-auto" />
          <p className="text-sm text-muted">{t("mcp.empty")}</p>
        </div>
      ) : (
        <div className="space-y-4">
          {clients.map((c) => (
            <div key={c.id} className="bg-surface rounded-2xl border border-border overflow-hidden">
              <div className="flex items-center justify-between p-4 cursor-pointer hover:bg-default-soft transition-colors" onClick={() => toggleTools(c)}>
                <div className="flex items-center gap-3">
                  <IconChevron className={`w-4 h-4 text-muted transition-transform ${expandedTools === c.id ? "rotate-90" : ""}`} />
                  <div>
                    <div className="font-semibold flex items-center gap-2">
                      {c.name}
                      <Chip size="sm" color={stateColor(c.state)} variant="soft">{c.state ?? "unknown"}</Chip>
                    </div>
                    <div className="text-xs text-muted flex items-center gap-2 mt-0.5">
                      <code>{c.connection_type === "stdio" ? c.stdio_command : c.url}</code>
                      <span>•</span>
                      <span>{c.tool_count ?? 0} {t("mcp.tools", { count: c.tool_count ?? 0 })}</span>
                    </div>
                    {c.error && <div className="text-xs text-danger mt-0.5 truncate max-w-[480px]">{c.error}</div>}
                  </div>
                </div>
                <div className="flex items-center gap-2" onClick={(e) => e.stopPropagation()}>
                  {c.state === "error" && (
                    <Button size="sm" variant="outline" onPress={() => reconnect(c.id)}>{t("mcp.reconnect")}</Button>
                  )}
                  <Button isIconOnly size="sm" variant="ghost" onPress={() => toggleEnabled(c)} aria-label={t("mcp.toggleAria")}>
                    <IconPower className={`w-4 h-4 ${c.enabled ? "text-success" : "text-muted"}`} />
                  </Button>
                  <Button isIconOnly size="sm" variant="ghost" onPress={() => openEdit(c)} aria-label={t("mcp.editAria")}><IconPencil className="w-4 h-4" /></Button>
                  <Button isIconOnly size="sm" variant="ghost" className="text-danger" onPress={() => setConfirmId(c.id)} aria-label={t("mcp.deleteAria")}><IconTrash className="w-4 h-4" /></Button>
                </div>
              </div>

              {expandedTools === c.id && (
                <div className="p-4 bg-surface border-t border-border">
                  <div className="text-sm font-semibold mb-2">{t("mcp.discoveredTools")}</div>
                  {toolsCache[c.id]?.length ? (
                    <div className="space-y-2 max-h-[300px] overflow-y-auto pr-2">
                      {toolsCache[c.id].map((tool) => (
                        <div key={tool.name} className="border border-border rounded-lg p-3">
                          <div className="flex items-center justify-between gap-2">
                            <span className="font-mono text-sm">{tool.name}</span>
                            <Chip size="sm" variant="flat" color="accent">{t("mcp.exposed")}</Chip>
                          </div>
                          {tool.description && <p className="text-xs text-muted mt-1">{tool.description}</p>}
                        </div>
                      ))}
                    </div>
                  ) : (
                    <p className="text-sm text-muted">{t("mcp.noTools")}</p>
                  )}
                </div>
              )}
            </div>
          ))}
        </div>
      )}

      <Modal isOpen={open} onOpenChange={setOpen}>
        <Modal.Backdrop>
          <Modal.Container>
            <Modal.Dialog className="max-w-xl">
              <Modal.CloseTrigger />
              <Modal.Header>
                <Modal.Heading>{editId ? t("mcp.editClient") : t("mcp.addClient")}</Modal.Heading>
              </Modal.Header>
              <Modal.Body className="flex flex-col gap-4">
                <TextField value={form.name} onChange={(v) => setForm({ ...form, name: v })}>
                  <Label>{t("mcp.name")}</Label>
                  <Input variant="secondary" placeholder={t("mcp.namePlaceholder")} />
                </TextField>

                <div className="flex flex-col gap-1">
                  <Label>{t("mcp.connectionType")}</Label>
                  <Select aria-label={t("mcp.connectionTypeAria")} selectedKey={form.connection_type} onSelectionChange={(k) => setForm({ ...form, connection_type: (k as string) ?? "http" })}>
                    <Select.Trigger className="bg-surface-secondary"><Select.Value /></Select.Trigger>
                    <Select.Popover>
                      <ListBox>{CONN_TYPES.map((c) => <ListBox.Item key={c} id={c}>{c}</ListBox.Item>)}</ListBox>
                    </Select.Popover>
                  </Select>
                </div>

                {form.connection_type === "stdio" ? (
                  <>
                    <TextField value={form.stdio_command} onChange={(v) => setForm({ ...form, stdio_command: v })}>
                      <Label>{t("mcp.stdioCommand")}</Label>
                      <Input variant="secondary" placeholder="npx" />
                    </TextField>
                    <TextField value={form.stdio_args_text || ""} onChange={(v) => setForm({ ...form, stdio_args_text: v })}>
                      <Label>{t("mcp.stdioArgs")}</Label>
                      <Input variant="secondary" placeholder="--arg value" />
                    </TextField>
                  </>
                ) : (
                  <TextField value={form.url} onChange={(v) => setForm({ ...form, url: v })}>
                    <Label>{t("mcp.url")}</Label>
                    <Input variant="secondary" placeholder="https://mcp.example.com/mcp" />
                  </TextField>
                )}

                <div className="flex flex-col gap-1">
                  <Label>{t("mcp.authType")}</Label>
                  <Select aria-label={t("mcp.authTypeAria")} selectedKey={form.auth_type} onSelectionChange={(k) => setForm({ ...form, auth_type: (k as string) ?? "none" })}>
                    <Select.Trigger className="bg-surface-secondary"><Select.Value /></Select.Trigger>
                    <Select.Popover>
                      <ListBox>{AUTH_TYPES.map((a) => <ListBox.Item key={a} id={a}>{a}</ListBox.Item>)}</ListBox>
                    </Select.Popover>
                  </Select>
                </div>

                {form.auth_type === "bearer" && (
                  <TextField value={form.auth_token} onChange={(v) => setForm({ ...form, auth_token: v })}>
                    <Label>{t("mcp.authToken")}</Label>
                    <Input variant="secondary" type="password" />
                  </TextField>
                )}

                <TextField value={form.tools_allow_text ?? "*"} onChange={(v) => setForm({ ...form, tools_allow_text: v })}>
                  <Label>{t("mcp.toolsAllow")}</Label>
                  <Input variant="secondary" placeholder="*  or  client__tool1, client__tool2" />
                </TextField>

                <div className="flex items-center justify-between">
                  <Label>{t("mcp.syncSeconds")}</Label>
                  <Input
                    type="number"
                    value={String(form.sync_seconds ?? 0)}
                    onChange={(e) => setForm({ ...form, sync_seconds: Number(e.target.value) || 0 })}
                    className="max-w-[160px]"
                    aria-label={t("mcp.syncSecondsAria")}
                  />
                </div>

                <div className="flex items-center justify-between">
                  <Label>{t("mcp.enabled")}</Label>
                  <Switch isSelected={!!form.enabled} onValueChange={(v) => setForm({ ...form, enabled: v })} />
                </div>

                {error && <p className="text-sm text-danger mt-2">{error}</p>}
              </Modal.Body>
              <Modal.Footer>
                <Button variant="primary" onPress={submit} isDisabled={saving}>{t("mcp.saveClient")}</Button>
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
                <AlertDialog.Heading>{t("mcp.deleteTitle")}</AlertDialog.Heading>
              </AlertDialog.Header>
              <AlertDialog.Body>
                <p>{t("mcp.deleteConfirm")}</p>
              </AlertDialog.Body>
              <AlertDialog.Footer>
                <Button slot="close" variant="tertiary">{t("mcp.cancel")}</Button>
                <Button slot="close" variant="danger" onPress={() => { if (confirmId) remove(confirmId); setConfirmId(null); }}>{t("mcp.delete")}</Button>
              </AlertDialog.Footer>
            </AlertDialog.Dialog>
          </AlertDialog.Container>
        </AlertDialog.Backdrop>
      </AlertDialog>
    </div>
  );
}
