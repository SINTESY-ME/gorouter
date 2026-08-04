import { useCallback, useEffect, useMemo, useState } from "react";
import { Button, Chip, Input, Label, ListBox, Modal, Select, Switch, Table, TextField } from "@heroui/react";
import { useTranslation } from "react-i18next";
import { api, User, UserPermissions } from "../api";
import { IconPlus, IconTrash, IconX } from "../icons";

type PermKey = keyof UserPermissions;

const PERM_KEYS: { key: PermKey; label: string }[] = [
  { key: "can_manage_own_providers", label: "users.permOwnProviders" },
  { key: "can_create_combos", label: "users.permCreateCombos" },
  { key: "can_manage_cache", label: "users.permCache" },
  { key: "can_access_settings", label: "users.permSettings" },
];

interface FormState {
  id: string | null;
  username: string;
  password: string;
  role: "admin" | "member";
  perms: UserPermissions;
  models: string[];
  combos: string[];
  providers: string[];
}

const emptyForm = (): FormState => ({
  id: null, username: "", password: "", role: "member",
  perms: { can_manage_own_providers: false, can_create_combos: false, can_manage_cache: false, can_access_settings: false },
  models: [], combos: [], providers: [],
});

export default function Users() {
  const { t } = useTranslation();
  const [items, setItems] = useState<User[]>([]);
  const [loading, setLoading] = useState(true);
  const [modalOpen, setModalOpen] = useState(false);
  const [form, setForm] = useState<FormState>(emptyForm());
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const [modelOptions, setModelOptions] = useState<string[]>([]);
  const [comboOptions, setComboOptions] = useState<string[]>([]);
  const [providerOptions, setProviderOptions] = useState<string[]>([]);

  const load = useCallback(() => {
    setLoading(true);
    api.users.list().then(setItems).catch(() => setItems([])).finally(() => setLoading(false));
  }, []);

  useEffect(() => {
    load();
    api.models.all().then((ms) => setModelOptions(ms.map((m) => m.id))).catch(() => {});
    api.combos.list().then((cs) => setComboOptions(cs.map((c) => c.name))).catch(() => {});
    api.providers.list().then((ps) => setProviderOptions(ps.map((p) => p.id))).catch(() => {});
  }, [load]);

  const openCreate = () => {
    setForm(emptyForm());
    setError("");
    setModalOpen(true);
  };

  const openEdit = (u: User) => {
    setForm({
      id: u.id,
      username: u.username,
      password: "",
      role: u.role,
      perms: u.permissions ?? emptyForm().perms,
      models: u.allowed_models ?? [],
      combos: u.allowed_combos ?? [],
      providers: u.allowed_providers ?? [],
    });
    setError("");
    setModalOpen(true);
  };

  const save = async () => {
    if (!form.username) { setError(t("users.errUsername")); return; }
    if (!form.id && !form.password) { setError(t("users.errPassword")); return; }
    setSaving(true);
    setError("");
    try {
      if (form.id) {
        await api.users.update(form.id, {
          username: form.username,
          ...(form.password ? { password: form.password } : {}),
          role: form.role,
          permissions: form.perms,
        });
      } else {
        await api.users.create({ username: form.username, password: form.password, role: form.role, permissions: form.perms });
      }
      if (form.id && form.role === "member") {
        await api.users.setAccess(form.id, "model", form.models);
        await api.users.setAccess(form.id, "combo", form.combos);
        await api.users.setAccess(form.id, "provider", form.providers);
      }
      setModalOpen(false);
      load();
    } catch (e: any) {
      setError(e?.message ?? t("users.saveError"));
    } finally {
      setSaving(false);
    }
  };

  const remove = async (id: string) => {
    try { await api.users.remove(id); load(); } catch {}
  };

  const rows = useMemo(() => items, [items]);

  return (
    <div className="space-y-5">
      <div className="flex justify-between items-center">
        <div>
          <h1 className="text-xl font-semibold">{t("users.title")}</h1>
          <p className="text-sm text-muted mt-1">{t("users.subtitle")}</p>
        </div>
        <Button onPress={openCreate}><IconPlus className="w-4 h-4" />{t("users.create")}</Button>
      </div>

      <Table>
        <Table.ScrollContainer>
          <Table.Content aria-label={t("users.tableAria")} className="min-w-[1180px]">
            <Table.Header>
              <Table.Column isRowHeader>{t("users.colUser")}</Table.Column>
              <Table.Column>{t("users.colRole")}</Table.Column>
              <Table.Column>{t("users.colPermissions")}</Table.Column>
              <Table.Column>{t("users.colAccess")}</Table.Column>
              <Table.Column>{t("users.colKeys")}</Table.Column>
              <Table.Column>{t("users.colCreated")}</Table.Column>
              <Table.Column>{t("users.colSession")}</Table.Column>
              <Table.Column>{t("users.colActions")}</Table.Column>
            </Table.Header>
            <Table.Body>
              {rows.map((u) => (
                <Table.Row key={u.id} id={u.id}>
                  <Table.Cell>
                    <div className="flex items-center gap-2">
                      <span className={`size-2 rounded-full shrink-0 ${u.session_active ? "bg-success" : "bg-default-soft"}`} title={u.session_active ? t("users.sessionActive") : t("users.sessionInactive")} />
                      <span className="font-medium">{u.username}</span>
                    </div>
                  </Table.Cell>
                  <Table.Cell>
                    <Chip size="sm" variant={u.role === "admin" ? "primary" : "soft"} color={u.role === "admin" ? "accent" : "default"}>
                      {u.role === "admin" ? t("users.roleAdmin") : t("users.roleMember")}
                    </Chip>
                  </Table.Cell>
                  <Table.Cell>
                    <div className="flex flex-wrap gap-1 min-w-48">
                      {PERM_KEYS.map((p) => {
                        const enabled = u.role === "admin" || Boolean(u.permissions?.[p.key]);
                        return <Chip key={p.key} size="sm" variant="soft" color={enabled ? "success" : "default"} className="h-5 text-[10px]">{t(p.label).replace(/^Manage own |^Create own |^Configure |^Access /, "")}</Chip>;
                      })}
                    </div>
                  </Table.Cell>
                  <Table.Cell>
                    <span className="text-xs text-muted whitespace-nowrap">
                      {u.role === "admin" ? t("users.allAccess") : `${(u.allowed_models ?? []).length} ${t("users.models")} · ${(u.allowed_combos ?? []).length} ${t("users.combos")} · ${(u.allowed_providers ?? []).length} ${t("users.providers")}`}
                    </span>
                  </Table.Cell>
                  <Table.Cell><span className="text-xs text-muted">{u.api_keys_count}</span></Table.Cell>
                  <Table.Cell><span className="text-xs text-muted whitespace-nowrap">{new Date(u.created_at).toLocaleDateString()}</span></Table.Cell>
                  <Table.Cell>
                    <span className={`text-xs ${u.session_active ? "text-success" : "text-muted"}`}>{u.session_active ? t("users.sessionActive") : t("users.sessionInactive")}</span>
                  </Table.Cell>
                  <Table.Cell>
                    <div className="flex items-center gap-2">
                      <Button size="sm" variant="secondary" onPress={() => openEdit(u)}>{t("users.edit")}</Button>
                      <Button size="sm" variant="secondary" isIconOnly onPress={() => remove(u.id)} aria-label={t("users.delete")}>
                        <IconTrash className="w-3.5 h-3.5" />
                      </Button>
                    </div>
                  </Table.Cell>
                </Table.Row>
              ))}
            </Table.Body>
          </Table.Content>
        </Table.ScrollContainer>
      </Table>
      {!loading && rows.length === 0 && (
        <div className="p-10 text-center text-muted text-sm">{t("users.empty")}</div>
      )}

      <Modal isOpen={modalOpen} onOpenChange={setModalOpen}>
        <Modal.Backdrop>
        <Modal.Container>
          <Modal.Dialog>
            <Modal.Header>
              <Modal.Heading>{form.id ? t("users.editTitle") : t("users.createTitle")}</Modal.Heading>
            </Modal.Header>
            <Modal.Body className="space-y-4">
              <TextField isRequired value={form.username} onChange={(v) => setForm({ ...form, username: v })}>
                <Label>{t("users.username")}</Label>
                <Input placeholder={t("users.usernamePlaceholder")} />
              </TextField>
              <TextField isRequired={!form.id} value={form.password} onChange={(v) => setForm({ ...form, password: v })} type="password">
                <Label>{t("users.password")}</Label>
                <Input placeholder={form.id ? t("users.passwordLeave") : t("users.passwordPlaceholder")} />
              </TextField>
              <Select aria-label={t("users.role")} selectedKey={form.role} onSelectionChange={(k) => setForm({ ...form, role: (k as "admin" | "member") })}>
                <Select.Trigger><Select.Value>{form.role === "admin" ? t("users.roleAdmin") : t("users.roleMember")}</Select.Value><Select.Indicator /></Select.Trigger>
                <Select.Popover>
                  <ListBox>
                    <ListBox.Item key="member" id="member">{t("users.roleMember")}</ListBox.Item>
                    <ListBox.Item key="admin" id="admin">{t("users.roleAdmin")}</ListBox.Item>
                  </ListBox>
                </Select.Popover>
              </Select>

              {form.role === "member" && (
                <>
                  <div className="space-y-2">
                    <p className="text-sm font-medium">{t("users.permissions")}</p>
                    {PERM_KEYS.map((p) => (
                      <Switch
                        key={p.key}
                        isSelected={form.perms[p.key]}
                        onChange={(v) => setForm({ ...form, perms: { ...form.perms, [p.key]: v } })}
                      >
                        <Switch.Content>
                          <Switch.Control>
                            <Switch.Thumb />
                          </Switch.Control>
                          <span className="text-sm">{t(p.label)}</span>
                        </Switch.Content>
                      </Switch>
                    ))}
                  </div>
                  <div className="space-y-2">
                    <p className="text-sm font-medium">{t("users.grantedModels")}</p>
                    <AccessPicker options={modelOptions} selected={form.models} onChange={(v) => setForm({ ...form, models: v })} placeholder={t("users.pickModels")} />
                  </div>
                  <div className="space-y-2">
                    <p className="text-sm font-medium">{t("users.grantedCombos")}</p>
                    <AccessPicker options={comboOptions} selected={form.combos} onChange={(v) => setForm({ ...form, combos: v })} placeholder={t("users.pickCombos")} />
                  </div>
                  <div className="space-y-2">
                    <p className="text-sm font-medium">{t("users.grantedProviders")}</p>
                    <AccessPicker options={providerOptions} selected={form.providers} onChange={(v) => setForm({ ...form, providers: v })} placeholder={t("users.pickProviders")} />
                  </div>
                </>
              )}
              {error && <p className="text-sm text-danger">{error}</p>}
            </Modal.Body>
            <Modal.Footer>
              <Button variant="secondary" onPress={() => setModalOpen(false)}>{t("users.cancel")}</Button>
              <Button onPress={save} isPending={saving}>{saving ? t("users.saving") : t("users.save")}</Button>
            </Modal.Footer>
          </Modal.Dialog>
        </Modal.Container>
        </Modal.Backdrop>
      </Modal>
    </div>
  );
}

function AccessPicker({ options, selected, onChange, placeholder }: {
  options: string[];
  selected: string[];
  onChange: (v: string[]) => void;
  placeholder: string;
}) {
  const toggle = (id: string) => {
    onChange(selected.includes(id) ? selected.filter((x) => x !== id) : [...selected, id]);
  };
  return (
    <div className="space-y-2">
      <div className="flex flex-wrap gap-1.5">
        {selected.map((id) => (
          <Chip key={id} size="sm" variant="soft" className="group">
            <Chip.Label>{id}</Chip.Label>
            <button
              type="button"
              aria-label={`remove ${id}`}
              onClick={() => toggle(id)}
              className="ml-1 rounded-full opacity-60 hover:opacity-100 transition-opacity"
            >
              <IconX className="w-3 h-3" />
            </button>
          </Chip>
        ))}
      </div>
      <Select
        aria-label={placeholder}
        placeholder={placeholder}
        selectedKey={null}
        onSelectionChange={(k) => { if (k) toggle(k as string); }}
      >
        <Select.Trigger><Select.Value>{placeholder}</Select.Value><Select.Indicator /></Select.Trigger>
        <Select.Popover>
          <ListBox>
            {options.filter((o) => !selected.includes(o)).map((o) => (
              <ListBox.Item key={o} id={o}>{o}</ListBox.Item>
            ))}
          </ListBox>
        </Select.Popover>
      </Select>
    </div>
  );
}
