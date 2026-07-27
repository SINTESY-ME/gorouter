import { useEffect, useRef, useState, useCallback } from "react";
import {
  Button, Chip, Autocomplete, AutocompleteItem, Textarea,
} from "@heroui/react";
import {
  api, streamChat, type ChatMessage, type ModelEntry, type Combo,
} from "../api";

interface PlaygroundMsg {
  id: string;
  role: "user" | "assistant";
  content: string;
  // Metrics (assistant messages only)
  model?: string;
  combo?: string;
  latencyMs?: number;
  tokens?: { prompt: number; completion: number; total: number };
  tps?: number;
  streaming?: boolean;
  error?: string;
}

const KIND_COLORS: Record<string, "primary" | "success" | "warning" | "secondary" | "danger" | "default"> = {
  llm: "primary", embedding: "success", image: "warning", tts: "secondary", stt: "danger",
  rerank: "default", ocr: "default", video: "default",
};

export default function Playground() {
  const [models, setModels] = useState<ModelEntry[]>([]);
  const [combos, setCombos] = useState<Combo[]>([]);
  const [loadingOpts, setLoadingOpts] = useState(true);
  const [selectedModel, setSelectedModel] = useState("");
  const [messages, setMessages] = useState<PlaygroundMsg[]>([]);
  const [input, setInput] = useState("");
  const [streaming, setStreaming] = useState(false);
  const abortRef = useRef<AbortController | null>(null);
  const scrollRef = useRef<HTMLDivElement>(null);

  // Load model + combo lists
  useEffect(() => {
    let cancelled = false;
    (async () => {
      setLoadingOpts(true);
      try {
        const ps = await api.providers.list();
        const [providerModels, combosList] = await Promise.all([
          Promise.allSettled(ps.map((p) => api.providers.models(p.id))),
          api.combos.list().catch(() => []),
        ]);
        if (cancelled) return;
        const allModels: ModelEntry[] = [];
        providerModels.forEach((r) => {
          if (r.status === "fulfilled") r.value.forEach((m) => allModels.push(m));
        });
        // Only LLM models make sense for chat
        setModels(allModels.filter((m) => m.kind === "llm" || !m.kind));
        setCombos(combosList.filter((c) => !c.kind || c.kind === "llm"));
      } finally {
        if (!cancelled) setLoadingOpts(false);
      }
    })();
    return () => { cancelled = true; };
  }, []);

  // Auto-scroll to bottom on new content
  useEffect(() => {
    if (scrollRef.current) {
      scrollRef.current.scrollTop = scrollRef.current.scrollHeight;
    }
  }, [messages]);

  const send = useCallback(async () => {
    const text = input.trim();
    if (!text || streaming || !selectedModel) return;

    const userMsg: PlaygroundMsg = { id: crypto.randomUUID(), role: "user", content: text };
    const assistantId = crypto.randomUUID();
    const assistantMsg: PlaygroundMsg = { id: assistantId, role: "assistant", content: "", streaming: true };
    const history: ChatMessage[] = [...messages, userMsg].map((m) => ({
      role: m.role,
      content: m.content,
    }));

    setMessages((prev) => [...prev, userMsg, assistantMsg]);
    setInput("");
    setStreaming(true);

    const controller = new AbortController();
    abortRef.current = controller;
    const start = performance.now();
    let finalUsage: { prompt_tokens: number; completion_tokens: number; total_tokens: number } | undefined;
    let finalModel: string | undefined;
    let accumulated = "";
    const comboName = combos.some((c) => c.name === selectedModel) ? selectedModel : undefined;

    try {
      await streamChat(
        history,
        selectedModel,
        (chunk) => {
          if (chunk.delta) {
            accumulated += chunk.delta;
            setMessages((prev) =>
              prev.map((m) =>
                m.id === assistantId ? { ...m, content: accumulated } : m
              )
            );
          }
          if (chunk.usage) {
            finalUsage = chunk.usage;
          }
          if (chunk.model) {
            finalModel = chunk.model;
          }
        },
        controller.signal,
      );

      const elapsedMs = performance.now() - start;
      const completionTokens = finalUsage?.completion_tokens ?? 0;
      const tps = completionTokens > 0 && elapsedMs > 0
        ? (completionTokens / (elapsedMs / 1000))
        : undefined;

      setMessages((prev) =>
        prev.map((m) =>
          m.id === assistantId
            ? {
                ...m,
                streaming: false,
                model: finalModel,
                combo: comboName,
                latencyMs: Math.round(elapsedMs),
                tokens: finalUsage,
                tps,
              }
            : m
        )
      );
    } catch (err: any) {
      if (err?.name === "AbortError") {
        setMessages((prev) =>
          prev.map((m) =>
            m.id === assistantId ? { ...m, streaming: false, content: m.content + "\n\n[cancelado]" } : m
          )
        );
      } else {
        setMessages((prev) =>
          prev.map((m) =>
            m.id === assistantId ? { ...m, streaming: false, error: err?.message ?? "erro" } : m
          )
        );
      }
    } finally {
      setStreaming(false);
      abortRef.current = null;
    }
  }, [input, streaming, selectedModel, messages, combos]);

  const stop = () => {
    abortRef.current?.abort();
  };

  const clear = () => {
    setMessages([]);
  };

  const onKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === "Enter" && !e.shiftKey) {
      e.preventDefault();
      send();
    }
  };

  // Build combined options for the autocomplete
  const options: { id: string; label: string; kind: string; isCombo: boolean }[] = [
    ...combos.map((c) => ({ id: c.name, label: c.name, kind: c.kind || "llm", isCombo: true })),
    ...models.map((m) => ({ id: m.id, label: m.id, kind: m.kind || "llm", isCombo: false })),
  ];

  return (
    <div className="flex flex-col h-full max-h-[calc(100vh-8rem)]">
      {/* Header: model selector + actions */}
      <div className="flex items-center gap-3 mb-3">
        <Autocomplete
          label="Modelo / Combo"
          placeholder="Selecionar..."
          selectedKey={selectedModel || null}
          onSelectionChange={(key) => setSelectedModel((key as string) ?? "")}
          className="flex-1 max-w-md"
          size="sm"
          isLoading={loadingOpts}
        >
          {options.map((opt) => (
            <AutocompleteItem key={opt.id} textValue={opt.id}>
              <div className="flex items-center justify-between w-full gap-2">
                <div className="flex items-center gap-2">
                  {opt.isCombo && <IconStack className="w-3 h-3 text-secondary" />}
                  <span className="font-mono text-xs">{opt.label}</span>
                </div>
                <Chip
                  size="sm"
                  variant="flat"
                  color={opt.isCombo ? "secondary" : KIND_COLORS[opt.kind] ?? "default"}
                  className="text-[10px]"
                >
                  {opt.isCombo ? "combo" : opt.kind}
                </Chip>
              </div>
            </AutocompleteItem>
          ))}
        </Autocomplete>
        {messages.length > 0 && (
          <Button size="sm" variant="flat" onPress={clear} isDisabled={streaming}>
            Limpar
          </Button>
        )}
      </div>

      {/* Chat area */}
      <div
        ref={scrollRef}
        className="flex-1 overflow-y-auto space-y-4 pb-4"
      >
        {messages.length === 0 ? (
          <div className="flex flex-col items-center justify-center h-full text-default-400 gap-3">
            <IconChat className="w-12 h-12 opacity-30" />
            <p className="text-sm">Selecione um modelo ou combo e envie uma mensagem para testar.</p>
          </div>
        ) : (
          messages.map((msg) => (
            <div
              key={msg.id}
              className={`flex ${msg.role === "user" ? "justify-end" : "justify-start"}`}
            >
              <div
                className={`max-w-[85%] rounded-2xl px-4 py-3 ${
                  msg.role === "user"
                    ? "bg-primary text-primary-foreground"
                    : "bg-content2"
                }`}
              >
                {/* Content */}
                <div className="text-sm whitespace-pre-wrap break-words">
                  {msg.content || (msg.streaming && !msg.error ? "…" : "")}
                  {msg.streaming && !msg.error && (
                    <span className="inline-block w-1.5 h-4 bg-primary/60 ml-0.5 animate-pulse rounded-sm align-middle" />
                  )}
                </div>
                {msg.error && (
                  <div className="text-xs text-danger mt-1">⚠ {msg.error}</div>
                )}

                {/* Metrics bar (assistant only, after streaming) */}
                {msg.role === "assistant" && !msg.streaming && !msg.error && (
                  <div className="flex flex-wrap gap-1.5 mt-2 pt-2 border-t border-default-200/50">
                    {msg.combo && (
                      <Chip size="sm" variant="flat" color="secondary" className="text-[10px]">
                        combo: {msg.combo}
                      </Chip>
                    )}
                    {msg.model && (
                      <Chip size="sm" variant="flat" color="primary" className="text-[10px]">
                        {msg.model}
                      </Chip>
                    )}
                    {msg.latencyMs != null && (
                      <Chip size="sm" variant="flat" className="text-[10px]">
                        {msg.latencyMs}ms
                      </Chip>
                    )}
                    {msg.tps != null && msg.tps > 0 && (
                      <Chip size="sm" variant="flat" color="success" className="text-[10px]">
                        {msg.tps.toFixed(1)} tok/s
                      </Chip>
                    )}
                    {msg.tokens && (
                      <Chip size="sm" variant="flat" className="text-[10px]">
                        {msg.tokens.prompt}p + {msg.tokens.completion}c = {msg.tokens.total} tok
                      </Chip>
                    )}
                  </div>
                )}
              </div>
            </div>
          ))
        )}
      </div>

      {/* Input area */}
      <div className="flex gap-2 items-end pt-2 border-t border-default-100">
        <Textarea
          value={input}
          onValueChange={setInput}
          onKeyDown={onKeyDown}
          placeholder={selectedModel ? "Digite sua mensagem... (Enter para enviar, Shift+Enter para nova linha)" : "Selecione um modelo primeiro..."}
          isDisabled={streaming || !selectedModel}
          minRows={1}
          maxRows={5}
          className="flex-1"
          size="sm"
        />
        {streaming ? (
          <Button color="danger" variant="flat" onPress={stop} className="shrink-0">
            Parar
          </Button>
        ) : (
          <Button
            color="primary"
            onPress={send}
            isDisabled={!input.trim() || !selectedModel}
            className="shrink-0"
          >
            Enviar
          </Button>
        )}
      </div>
    </div>
  );
}

function IconStack() {
  return (
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" className="w-3 h-3">
      <polygon points="12 2 2 7 12 12 22 7 12 2" />
      <polyline points="2 17 12 22 22 17" />
      <polyline points="2 12 12 17 22 12" />
    </svg>
  );
}

function IconChat({ className }: { className?: string }) {
  return (
    <svg className={className} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
      <path d="M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2z" />
    </svg>
  );
}