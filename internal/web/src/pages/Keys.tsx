import { useEffect, useState } from "react";
import {
  Table, Button, Modal, Input, Chip, TextField, Label, Description,
} from "@heroui/react";
import { api, type ApiKey } from "../api";

export default function Keys() {
  const [items, setItems] = useState<ApiKey[]>([]);
  const [loading, setLoading] = useState(true);
  const [createOpen, setCreateOpen] = useState(false);
  const [name, setName] = useState("");
  const [rpm, setRpm] = useState("");
  const [copied, setCopied] = useState<string | null>(null);
  const [copiedKeyId, setCopiedKeyId] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);
  const [endpoint, setEndpoint] = useState("/v1");
  const [endpointCopied, setEndpointCopied] = useState(false);

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
      const k = await api.keys.create({ name, rate_limit_rpm: rpm ? parseInt(rpm) : 0 });
      setName(""); setRpm(""); setCreateOpen(false); load();
      setCopied(k.key);
    } finally { setSaving(false); }
  };

  const remove = async (id: string) => {
    if (confirm("Remover esta chave?")) { await api.keys.remove(id); load(); }
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
        <Button variant="outline" onPress={() => setCreateOpen(true)}><IconPlus /> Nova chave</Button>
      </div>

      <div className="bg-surface rounded-2xl border border-border p-5">
        <div className="flex items-center gap-2 mb-3">
          <IconApi />
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
            {endpointCopied ? <IconCheck /> : <IconCopy />}
          </Button>
        </div>
      </div>

      <div className="bg-surface rounded-2xl border border-border overflow-hidden">
        {loading ? (
          <div className="p-10 text-center text-muted text-sm">Carregando...</div>
        ) : items.length === 0 ? (
          <div className="p-10 text-center text-muted text-sm">
            Nenhuma chave ainda. Clique em <strong>Nova chave</strong>.
          </div>
        ) : (
          <Table>
            <Table.ScrollContainer>
              <Table.Content aria-label="keys" className="min-w-[640px]">
                <Table.Header>
                  <Table.Column isRowHeader id="name">Nome</Table.Column>
                  <Table.Column id="key">Chave</Table.Column>
                  <Table.Column id="rpm">Rate Limit</Table.Column>
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
                          {copiedKeyId === k.id ? <IconCheck className="text-success shrink-0" /> : <IconCopy className="w-3 h-3 text-muted/70 shrink-0 group-hover:text-accent transition-colors" />}
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
                          <Button isIconOnly size="sm" variant="ghost" className="text-danger" onPress={() => remove(k.id)} aria-label="excluir"><IconTrash /></Button>
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
    </div>
  );
}

function IconPlus() { return <svg className="w-4 h-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><path d="M5 12h14M12 5v14"/></svg>; }
function IconTrash() { return <svg className="w-4 h-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><polyline points="3 6 5 6 21 6"/><path d="M19 6l-1.5 14a2 2 0 0 1-2 2H8.5a2 2 0 0 1-2-2L5 6"/><path d="M10 11v6M14 11v6"/></svg>; }
function IconApi() { return <svg className="w-4 h-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><polyline points="16 18 22 12 16 6"/><polyline points="8 6 2 12 8 18"/></svg>; }
function IconCopy({ className = "w-4 h-4" }: { className?: string }) { return <svg className={className} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><rect x="9" y="9" width="13" height="13" rx="2" ry="2"/><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"/></svg>; }
function IconCheck({ className = "w-4 h-4" }: { className?: string }) { return <svg className={className} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><polyline points="20 6 9 17 4 12"/></svg>; }
