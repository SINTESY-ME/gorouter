import { useEffect, useRef, useState, useCallback } from "react";
import {
  Button, Chip, Autocomplete, AutocompleteItem, Textarea, Tooltip,
} from "@heroui/react";
import {
  api, streamChat, type ChatMessage, type ModelEntry, type Combo,
} from "../api";

interface PlaygroundMsg {
  id: string;
  role: "user" | "assistant";
  content: string;
  model?: string;
  combo?: string;
  latencyMs?: number;
  ttftMs?: number;
  tokens?: { prompt: number; completion: number; total: number };
  tps?: number;
  streaming?: boolean;
  error?: string;
}

const KIND_COLORS: Record<string, "primary" | "success" | "warning" | "secondary" | "danger" | "default"> = {
  llm: "primary", embedding: "success", image: "warning", tts: "secondary", stt: "danger",
  rerank: "default", ocr: "default", video: "default",
};

const SUGGESTIONS = [
  "Explique como funciona recursão em programação",
  "Escreva um poema sobre observabilidade",
  "Compare Rust vs Go para sistemas embarcados",
  "Crie uma função SQL que calcule retenção semanal",
];

export default function Playground() {
  const [models, setModels] = useState<ModelEntry[]>([]);
  const [combos, setCombos] = useState<Combo[]>([]);
  const [loadingOpts, setLoadingOpts] = useState(true);
  const [selectedModel, setSelectedModel] = useState("");
  const [messages, setMessages] = useState<PlaygroundMsg[]>([]);
  const [input, setInput] = useState("");
  const [streaming, setStreaming] = useState(false);
  const abortRef = useRef<AbortController | null>(null);
  const scrollerRef = useRef<HTMLDivElement>(null);
  const textareaRef = useRef<HTMLTextAreaElement | null>(null);

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
        setModels(allModels.filter((m) => m.kind === "llm" || !m.kind));
        setCombos(combosList.filter((c) => !c.kind || c.kind === "llm"));
      } finally {
        if (!cancelled) setLoadingOpts(false);
      }
    })();
    return () => { cancelled = true; };
  }, []);

  // Auto-scroll: only scroll to bottom if user is already near the bottom.
  // Otherwise (scrolled up reading history), leave them where they are.
  useEffect(() => {
    const el = scrollerRef.current;
    if (!el) return;
    const distanceFromBottom = el.scrollHeight - el.scrollTop - el.clientHeight;
    if (distanceFromBottom < 120) {
      el.scrollTop = el.scrollHeight;
    }
  }, [messages]);

  const send = useCallback(async (overrideText?: string) => {
    const text = (overrideText ?? input).trim();
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
    let firstChunkAt = 0;
    let finalUsage: { prompt_tokens: number; completion_tokens: number; total_tokens: number } | undefined;
    let finalModel: string | undefined;
    let accumulated = "";
    const isCombo = combos.some((c) => c.name === selectedModel);
    const comboName = isCombo ? selectedModel : undefined;

    try {
      await streamChat(
        history,
        selectedModel,
        (chunk) => {
          if (chunk.delta) {
            if (firstChunkAt === 0) firstChunkAt = performance.now();
            accumulated += chunk.delta;
            setMessages((prev) =>
              prev.map((m) => (m.id === assistantId ? { ...m, content: accumulated } : m))
            );
          }
          if (chunk.usage) finalUsage = chunk.usage;
          if (chunk.model) finalModel = chunk.model;
        },
        controller.signal,
      );

      const elapsedMs = performance.now() - start;
      const ttftMs = firstChunkAt > 0 ? Math.round(firstChunkAt - start) : 0;
      const genMs = ttftMs > 0 && elapsedMs > ttftMs ? elapsedMs - ttftMs : elapsedMs;
      const completionTokens = finalUsage?.completion_tokens ?? 0;
      const tps = completionTokens > 0 && genMs > 0
        ? (completionTokens / (genMs / 1000))
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
                ttftMs,
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

  const stop = () => abortRef.current?.abort();
  const clear = () => setMessages([]);

  const onKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === "Enter" && !e.shiftKey && !e.nativeEvent.isComposing) {
      e.preventDefault();
      send();
    }
  };

  const options: { id: string; label: string; kind: string; isCombo: boolean }[] = [
    ...combos.map((c) => ({ id: c.name, label: c.name, kind: c.kind || "llm", isCombo: true })),
    ...models.map((m) => ({ id: m.id, label: m.id, kind: m.kind || "llm", isCombo: false })),
  ];

  return (
    <div className="flex flex-col h-full bg-default-50">
      {/* Top bar — model selector + new chat (ChatGPT-style header) */}
      <div className="shrink-0 h-12 border-b border-default-100 bg-content1/80 backdrop-blur flex items-center justify-between px-4 gap-4">
        <div className="flex items-center gap-2 min-w-0">
          <IconChat className="w-4 h-4 text-primary shrink-0" />
          <span className="font-semibold text-sm shrink-0">Playground</span>
          <span className="text-default-300 shrink-0">/</span>
          <Autocomplete
            aria-label="Modelo"
            selectedKey={selectedModel || null}
            onSelectionChange={(key) => setSelectedModel((key as string) ?? "")}
            size="sm"
            variant="flat"
            className="w-72"
            classNames={{
              base: "min-h-0",
              input: "text-sm",
              inputWrapper: "h-8 min-h-8 bg-content2/60",
            }}
            placeholder={loadingOpts ? "Carregando..." : "Selecione um modelo ou combo..."}
            isDisabled={loadingOpts}
            inputValue={selectedModel}
            onInputChange={(v) => setSelectedModel(v)}
            allowsCustomValue={false}
          >
            {options.map((opt) => (
              <AutocompleteItem key={opt.id} textValue={opt.id}>
                <div className="flex items-center justify-between w-full gap-2">
                  <div className="flex items-center gap-2 min-w-0">
                    {opt.isCombo && <IconStack className="w-3 h-3 text-secondary shrink-0" />}
                    <span className="font-mono text-xs truncate">{opt.label}</span>
                  </div>
                  <Chip
                    size="sm"
                    variant="flat"
                    color={opt.isCombo ? "secondary" : KIND_COLORS[opt.kind] ?? "default"}
                    className="text-[10px] shrink-0"
                  >
                    {opt.isCombo ? "combo" : opt.kind}
                  </Chip>
                </div>
              </AutocompleteItem>
            ))}
          </Autocomplete>
        </div>
        <div className="flex items-center gap-1">
          {messages.length > 0 && (
            <Tooltip content="Nova conversa">
              <Button isIconOnly size="sm" variant="light" onPress={clear} isDisabled={streaming}>
                <IconPlus className="w-4 h-4" />
              </Button>
            </Tooltip>
          )}
        </div>
      </div>

      {/* Scrolling conversation area */}
      <div ref={scrollerRef} className="flex-1 overflow-y-auto">
        {messages.length === 0 ? (
          <WelcomeScreen
            disabled={!selectedModel}
            onPick={(text) => {
              setInput(text);
              textareaRef.current?.focus();
            }}
          />
        ) : (
          <div className="mx-auto w-full max-w-3xl px-4 py-6 space-y-6">
            {messages.map((m) => (
              <MessageRow key={m.id} msg={m} />
            ))}
          </div>
        )}
      </div>

      {/* Sticky composer at bottom (Open WebUI / ChatGPT style) */}
      <div className="shrink-0 bg-gradient-to-t from-default-50 via-default-50 to-transparent pt-6 pb-4 px-4">
        <div className="mx-auto max-w-3xl">
          <div className="bg-content1 border border-default-200 rounded-2xl shadow-md focus-within:border-primary/50 focus-within:shadow-lg transition-all">
            <Textarea
              ref={textareaRef}
              value={input}
              onValueChange={setInput}
              onKeyDown={onKeyDown}
              placeholder={
                selectedModel
                  ? "Mensagem"
                  : "Selecione um modelo ou combo acima para começar"
              }
              isDisabled={!selectedModel}
              minRows={1}
              maxRows={12}
              variant="flat"
              classNames={{
                base: "rounded-2xl",
                inputWrapper:
                  "bg-transparent shadow-none border-0 group-data-[focus-within=true]:border-0 px-4 py-3",
                input: "text-[15px] resize-none py-1 leading-snug",
              }}
              autoFocus
            />
            <div className="flex items-center justify-between px-2 pb-2">
              <div className="text-[11px] text-default-400 pl-2">
                Enter envia · Shift+Enter nova linha
              </div>
              <div className="flex items-center gap-1">
                {streaming ? (
                  <Button
                    size="sm"
                    color="danger"
                    variant="flat"
                    onPress={stop}
                    isIconOnly
                    className="h-8 w-8 min-w-8"
                  >
                    <IconStop className="w-4 h-4" />
                  </Button>
                ) : (
                  <Button
                    size="sm"
                    color="primary"
                    isIconOnly
                    onPress={() => send()}
                    isDisabled={!input.trim() || !selectedModel}
                    className="h-8 w-8 min-w-8"
                  >
                    <IconArrowUp className="w-4 h-4" />
                  </Button>
                )}
              </div>
            </div>
          </div>
        </div>
      </div>
    </div>
  );
}

// ---- Welcome screen (centered, like ChatGPT empty state) ----
function WelcomeScreen({ disabled, onPick }: { disabled: boolean; onPick: (text: string) => void }) {
  return (
    <div className="h-full flex items-center justify-center px-4">
      <div className="mx-auto max-w-2xl w-full text-center">
        <div className="inline-flex items-center justify-center w-12 h-12 rounded-2xl bg-primary/10 mb-5">
          <IconSparkles className="w-6 h-6 text-primary" />
        </div>
        <h1 className="text-2xl font-semibold tracking-tight mb-1">Como posso ajudar hoje?</h1>
        <p className="text-sm text-default-500 mb-7">
          Escolha um modelo ou combo no topo e comece a testar.
        </p>
        <div className="grid grid-cols-1 sm:grid-cols-2 gap-2 text-left">
          {SUGGESTIONS.map((s) => (
            <button
              key={s}
              type="button"
              disabled={disabled}
              onClick={() => onPick(s)}
              className="text-sm text-left px-3 py-2.5 rounded-xl border border-default-200 bg-content1 hover:bg-content2 hover:border-default-300 transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
            >
              <IconSparkles className="w-3.5 h-3.5 inline-block text-default-400 mr-1.5 -mt-0.5" />
              {s}
            </button>
          ))}
        </div>
      </div>
    </div>
  );
}

// ---- Single conversation row ----
function MessageRow({ msg }: { msg: PlaygroundMsg }) {
  const isUser = msg.role === "user";
  return (
    <div className={`flex ${isUser ? "justify-end" : "justify-start"}`}>
      <div
        className={`min-w-0 ${
          isUser
            ? "max-w-[85%] bg-content2 border border-default-200/60 text-foreground rounded-2xl px-4 py-3 shadow-sm"
            : "w-full text-foreground py-1"
        }`}
      >
        {/* Body */}
        <div className="text-[15px] leading-relaxed whitespace-pre-wrap break-words text-foreground font-normal">
          {msg.error ? (
            <span className="text-danger font-medium">⚠ {msg.error}</span>
          ) : (
            <>
              {msg.content || (msg.streaming ? "" : "")}
              {msg.streaming && !msg.error && (
                <span className="inline-block w-1.5 h-4 bg-primary ml-0.5 animate-pulse rounded-sm align-middle" />
              )}
              {msg.streaming === false && msg.content === "" && !msg.error && (
                <span className="text-default-400 italic">vazio</span>
              )}
            </>
          )}
        </div>

        {/* Metrics row (assistant, after stream) */}
        {!isUser && !msg.streaming && !msg.error && (
          <div className="mt-2.5 flex flex-wrap items-center gap-x-3 gap-y-1 text-[11px] text-default-400 font-mono">
            {msg.combo && <span className="text-secondary font-medium">combo: {msg.combo}</span>}
            {msg.model && <span>{msg.model}</span>}
            {msg.ttftMs != null && msg.ttftMs > 0 && <span>ttft {formatLatency(msg.ttftMs)}</span>}
            {msg.latencyMs != null && <span>{formatLatency(msg.latencyMs)}</span>}
            {msg.tps != null && msg.tps > 0 && (
              <span className="text-success font-medium">{msg.tps.toFixed(1)} tok/s</span>
            )}
            {msg.tokens && (
              <span>
                {msg.tokens.prompt}↑ / {msg.tokens.completion}↓
              </span>
            )}
          </div>
        )}
      </div>
    </div>
  );
}

function formatLatency(ms: number): string {
  if (ms < 1000) return `${ms}ms`;
  return `${(ms / 1000).toFixed(2)}s`;
}

// ---- Icons ----
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
function IconSparkles({ className }: { className?: string }) {
  return (
    <svg className={className} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
      <path d="m12 3-1.9 5.8a2 2 0 0 1-1.3 1.3L3 12l5.8 1.9a2 2 0 0 1 1.3 1.3L12 21l1.9-5.8a2 2 0 0 1 1.3-1.3L21 12l-5.8-1.9a2 2 0 0 1-1.3-1.3Z" />
    </svg>
  );
}
function IconPlus({ className }: { className?: string }) {
  return (
    <svg className={className} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
      <path d="M12 5v14M5 12h14" />
    </svg>
  );
}
function IconStop({ className }: { className?: string }) {
  return (
    <svg className={className} viewBox="0 0 24 24" fill="currentColor">
      <rect x="6" y="6" width="12" height="12" rx="1.5" />
    </svg>
  );
}
function IconArrowUp({ className }: { className?: string }) {
  return (
    <svg className={className} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
      <line x1="12" y1="19" x2="12" y2="5" />
      <polyline points="5 12 12 5 19 12" />
    </svg>
  );
}
function IconBot({ className }: { className?: string }) {
  return (
    <svg className={className} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
      <rect x="3" y="11" width="18" height="10" rx="2" />
      <circle cx="12" cy="5" r="2" />
      <line x1="12" y1="7" x2="12" y2="11" />
      <circle cx="8" cy="16" r="1" />
      <circle cx="16" cy="16" r="1" />
    </svg>
  );
}