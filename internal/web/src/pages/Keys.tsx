import { useEffect, useState } from "react";
import {
  Table, Button, Modal, Input, Chip, TextField, Label, Description, Card, AlertDialog,
} from "@heroui/react";
import { api, type ApiKey } from "../api";
import { IconPlus, IconTrash, IconApi, IconCopy, IconCheck } from "../icons";

export default function Keys() {
  const [items, setItems] = useState<ApiKey[]>([]);
  const [loading, setLoading] = useState(true);
  const [createOpen, setCreateOpen] = useState(false);
  const [name, setName] = useState("");
  const [rpm, setRpm] = useState("");
  const [budgetUSD, setBudgetUSD] = useState("");
  const [budgetPeriod, setBudgetPeriod] = useState("");
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

  const create = async () => {
    setSaving(true);
    try {
      const k = await api.keys.create({
        name,
        rate_limit_rpm: rpm ? parseInt(rpm) : 0,
        budget_limit_usd: budgetUSD ? parseFloat(budgetUSD) : 0,
        budget_period: budgetPeriod || "",
      });
      setName(""); setRpm(""); setBudgetUSD(""); setBudgetPeriod(""); setCreateOpen(false); load();
      setCopied(k.key);
    } finally { setSaving(false); }
  };

  const remove = async (id: string) => {
    await api.keys.remove(id); load();
  };

  const toggleActive = async (k: ApiKey) => {
    await api.keys.update(k.id, { is_active: !k.is_active });
    load();
  };

  const updateRpm = async (k: ApiKey, value: string) => {
    const n = value ? parseInt(value) : 0;
    await api.keys.update(k.id, { rate_limit_rpm: n });
    load();
  };

  const updateBudget = async (k: ApiKey, usd: string, period: string) => {
    const n = usd ? parseFloat(usd) : 0;
    await api.keys.update(k.id, { budget_limit_usd: n, budget_period: period });
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
        <Button variant="outline" onPress={() => setCreateOpen(true)}><IconPlus className="w-4 h-4" /> Nova chave</Button>
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
              <Table.Content aria-label="keys" className="min-w-[820px]">
                <Table.Header>
                  <Table.Column isRowHeader id="name">Nome</Table.Column>
                  <Table.Column id="key">Chave</Table.Column>
                  <Table.Column id="rpm">Rate Limit</Table.Column>
                  <Table.Column id="budget">Budget</Table.Column>
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
                      <Table.Cell>
                        <Input
                          type="number"
                          defaultValue={k.rate_limit_rpm != null ? String(k.rate_limit_rpm) : ""}
                          placeholder="0"
                          className="w-20"
                          aria-label="Rate limit"
                          onBlur={(e) => {
                            const v = e.target.value;
                            if (v !== String(k.rate_limit_rpm || "")) updateRpm(k, v);
                          }}
                        />
                      </Table.Cell>
                      <Table.Cell>
                        <div className="flex items-center gap-1">
                          <span className="text-xs text-muted">$</span>
                          <Input
                            type="number"
                            step="0.01"
                            defaultValue={k.budget_limit_usd ? String(k.budget_limit_usd) : ""}
                            placeholder="0"
                            className="w-16"
                            aria-label="Budget limit"
                            onBlur={(e) => {
                              const v = e.target.value;
                              if (v !== String(k.budget_limit_usd || "")) updateBudget(k, v, k.budget_period || "");
                            }}
                          />
                          <select
                            defaultValue={k.budget_period || ""}
                            className="text-xs bg-transparent border border-border rounded-lg px-1 py-1.5 text-muted outline-none"
                            onChange={(e) => updateBudget(k, k.budget_limit_usd ? String(k.budget_limit_usd) : "", e.target.value)}
                          >
                            <option value="">—</option>
                            <option value="daily">dia</option>
                            <option value="monthly">mês</option>
                          </select>
                        </div>
                      </Table.Cell>
                      <Table.Cell>
                        <Chip size="sm" variant="soft" color={k.is_active ? "success" : "default"}>
                          {k.is_active ? "ativo" : "inativo"}
                        </Chip>
                      </Table.Cell>
                      <Table.Cell><span className="text-xs text-muted">{new Date(k.created_at).toLocaleDateString()}</span></Table.Cell>
                      <Table.Cell>
                        <div className="flex gap-1 justify-end">
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

      <Modal isOpen={createOpen} onOpenChange={setCreateOpen}>
        <Modal.Backdrop>
          <Modal.Container>
            <Modal.Dialog>
              <Modal.Header><Modal.Heading>Nova API Key</Modal.Heading></Modal.Header>
              <Modal.Body>
                <TextField value={name} onChange={setName}>
                  <Label>Nome</Label>
                  <Input placeholder="ex: dev, prod, mobile" />
                </TextField>
                <TextField value={rpm} onChange={setRpm}>
                  <Label>Rate Limit (req/min)</Label>
                  <Input type="number" placeholder="0 = ilimitado" />
                  <Description>Máximo de requisições por minuto. 0 desativa o limite.</Description>
                </TextField>
                <TextField value={budgetUSD} onChange={setBudgetUSD}>
                  <Label>Limite de gasto (USD)</Label>
                  <Input type="number" step="0.01" placeholder="0 = ilimitado" />
                  <Description>Rejeita requests quando o gasto exceder este valor no período.</Description>
                </TextField>
                <TextField value={budgetPeriod} onChange={setBudgetPeriod}>
                  <Label>Período do orçamento</Label>
                  <select className="w-full bg-transparent border border-border rounded-lg px-3 py-2 text-sm outline-none">
                    <option value="">Sem limite</option>
                    <option value="daily">Diário</option>
                    <option value="monthly">Mensal</option>
                  </select>
                </TextField>
              </Modal.Body>
              <Modal.Footer>
                <Button variant="secondary" onPress={() => setCreateOpen(false)}>Cancelar</Button>
                <Button variant="primary" onPress={create} isDisabled={saving}>Criar</Button>
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
