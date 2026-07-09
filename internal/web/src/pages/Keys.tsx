import { useEffect, useState } from "react";
import {
  Table, TableHeader, TableColumn, TableBody, TableRow, TableCell,
  Button, Modal, ModalContent, ModalHeader, ModalBody, ModalFooter,
  Input, useDisclosure, Chip,
} from "@heroui/react";
import { api, type ApiKey } from "../api";

export default function Keys() {
  const [items, setItems] = useState<ApiKey[]>([]);
  const [loading, setLoading] = useState(true);
  const { isOpen, onOpen, onClose } = useDisclosure();
  const [name, setName] = useState("");
  const [rpm, setRpm] = useState("");
  const [copied, setCopied] = useState<string | null>(null);
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
      setName(""); setRpm(""); onClose(); load();
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

  const copyEndpoint = async () => {
    try { await navigator.clipboard.writeText(endpoint); setEndpointCopied(true); setTimeout(() => setEndpointCopied(false), 1500); } catch {}
  };

  return (
    <div className="space-y-5">
      <div className="flex justify-between items-center">
        <div>
          <h1 className="text-2xl font-bold tracking-tight">API Keys</h1>
          <p className="text-sm text-default-500 mt-0.5">{items.length} chaves cadastradas</p>
        </div>
        <Button color="primary" variant="bordered" onPress={onOpen} startContent={<IconPlus />}>Nova chave</Button>
      </div>

      <div className="bg-content1 rounded-2xl border border-default-100 p-5">
        <div className="flex items-center gap-2 mb-3">
          <IconApi />
          <h2 className="text-base font-semibold">API Endpoint</h2>
        </div>
        <p className="text-xs text-default-500 mb-3">
          Aponte seu cliente (Claude Code, Cursor, Codex, Cline...) para este endpoint.
        </p>
        <div className="flex items-center gap-2">
          <Chip size="sm" variant="flat" className="shrink-0 min-w-[70px] justify-center font-mono text-xs">Local</Chip>
          <Input
            value={endpoint}
            isReadOnly
            classNames={{ inputWrapper: "font-mono text-sm", input: "font-mono text-sm" }}
            className="flex-1"
            aria-label="API Endpoint"
          />
          <Button
            isIconOnly
            variant="flat"
            onPress={copyEndpoint}
            aria-label="copiar endpoint"
            color={endpointCopied ? "success" : "default"}
          >
            {endpointCopied ? <IconCheck /> : <IconCopy />}
          </Button>
        </div>
      </div>
      <div className="bg-content1 rounded-2xl border border-default-100 overflow-hidden">
        {loading ? (
          <div className="p-10 text-center text-default-500 text-sm">Carregando...</div>
        ) : items.length === 0 ? (
          <div className="p-10 text-center text-default-500 text-sm">
            Nenhuma chave ainda. Clique em <strong>Nova chave</strong>.
          </div>
        ) : (
          <Table aria-label="keys" removeWrapper>
            <TableHeader>
              <TableColumn>NOME</TableColumn>
              <TableColumn>CHAVE</TableColumn>
              <TableColumn>RATE LIMIT</TableColumn>
              <TableColumn>STATUS</TableColumn>
              <TableColumn>CRIADA</TableColumn>
              <TableColumn align="end">AÇÕES</TableColumn>
            </TableHeader>
            <TableBody items={items}>
              {(k) => (
                <TableRow key={k.id}>
                  <TableCell><span className="font-medium">{k.name}</span></TableCell>
                  <TableCell><code className="text-xs text-default-500">{k.key.slice(0, 10)}…{k.key.slice(-6)}</code></TableCell>
                  <TableCell>
                    <Input
                      size="sm"
                      type="number"
                      defaultValue={k.rate_limit_rpm != null ? String(k.rate_limit_rpm) : ""}
                      placeholder="0"
                      className="w-20"
                      classNames={{ inputWrapper: "h-8 min-h-8" }}
                      onBlur={(e) => {
                        const v = e.target.value;
                        if (v !== String(k.rate_limit_rpm || "")) updateRpm(k, v);
                      }}
                    />
                  </TableCell>
                  <TableCell>
                    <Chip size="sm" variant="flat" color={k.is_active ? "success" : "default"}>
                      {k.is_active ? "ativo" : "inativo"}
                    </Chip>
                  </TableCell>
                  <TableCell><span className="text-xs text-default-500">{new Date(k.created_at).toLocaleDateString()}</span></TableCell>
                  <TableCell>
                    <div className="flex gap-1 justify-end">
                      <Button size="sm" variant="flat" onPress={() => toggleActive(k)}>
                        {k.is_active ? "Desativar" : "Ativar"}
                      </Button>
                      <Button isIconOnly size="sm" variant="light" color="danger" onPress={() => remove(k.id)} aria-label="excluir"><IconTrash /></Button>
                    </div>
                  </TableCell>
                </TableRow>
              )}
            </TableBody>
          </Table>
        )}
      </div>

      <Modal isOpen={isOpen} onClose={onClose}>
        <ModalContent>
          <ModalHeader>Nova API Key</ModalHeader>
          <ModalBody>
            <Input label="Nome" value={name} onValueChange={setName} placeholder="ex: dev, prod, mobile" />
            <Input
              label="Rate Limit (req/min)"
              type="number"
              value={rpm}
              onValueChange={setRpm}
              placeholder="0 = ilimitado"
              description="Máximo de requisições por minuto. 0 desativa o limite."
            />
          </ModalBody>
          <ModalFooter>
            <Button variant="flat" onPress={onClose}>Cancelar</Button>
            <Button color="primary" onPress={create} isLoading={saving}>Criar</Button>
          </ModalFooter>
        </ModalContent>
      </Modal>

      <Modal isOpen={!!copied} onClose={() => setCopied(null)}>
        <ModalContent>
          <ModalHeader>Chave criada</ModalHeader>
          <ModalBody>
            <p className="text-sm text-warning">Copie agora — não será mostrada novamente.</p>
            <code className="block bg-content2 p-3 rounded-lg font-mono text-xs break-all border border-default-100 mt-3">{copied}</code>
          </ModalBody>
          <ModalFooter>
            <Button color="primary" onPress={() => navigator.clipboard.writeText(copied || "")}>Copiar</Button>
            <Button variant="flat" onPress={() => setCopied(null)}>Fechar</Button>
          </ModalFooter>
        </ModalContent>
      </Modal>
    </div>
  );
}

function IconPlus() { return <svg className="w-4 h-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><path d="M5 12h14M12 5v14"/></svg>; }
function IconTrash() { return <svg className="w-4 h-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><polyline points="3 6 5 6 21 6"/><path d="M19 6l-1.5 14a2 2 0 0 1-2 2H8.5a2 2 0 0 1-2-2L5 6"/><path d="M10 11v6M14 11v6"/></svg>; }
function IconApi() { return <svg className="w-4 h-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><polyline points="16 18 22 12 16 6"/><polyline points="8 6 2 12 8 18"/></svg>; }
function IconCopy() { return <svg className="w-4 h-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><rect x="9" y="9" width="13" height="13" rx="2" ry="2"/><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"/></svg>; }
function IconCheck() { return <svg className="w-4 h-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><polyline points="20 6 9 17 4 12"/></svg>; }