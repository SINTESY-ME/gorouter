import { useEffect, useRef, useState, useCallback } from "react";
import {
  Button, TextArea, TextField, Tooltip,
} from "@heroui/react";
import { ModelComboBox, type ModelComboBoxItem } from "../components/ModelComboBox";
import {
  api, streamChat, type ChatMessage, type ModelEntry, type Combo, type Provider,
} from "../api";
import { IconChat, IconSparkles, IconPlus, IconStop, IconArrowUp } from "../icons";
import { useTranslation } from "react-i18next";

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

const SUGGESTIONS = [
  "playground.suggestionRecursion",
  "playground.suggestionPoem",
  "playground.suggestionRustVsGo",
  "playground.suggestionSql",
];

interface ModelGroup {
  provider: Provider;
  models: ModelEntry[];
}

export default function Playground() {
  const { t } = useTranslation();
  const [modelGroups, setModelGroups] = useState<ModelGroup[]>([]);
  const [combos, setCombos] = useState<Combo[]>([]);
  const [loadingOpts, setLoadingOpts] = useState(true);
  const [selectedModel, setSelectedModel] = useState("");
  const [messages, setMessages] = useState<PlaygroundMsg[]>([]);
  const [input, setInput] = useState("");
  const [streaming, setStreaming] = useState(false);
  const abortRef = useRef<AbortController | null>(null);
  const scrollerRef = useRef<HTMLDivElement>(null);
  const textareaRef = useRef<HTMLTextAreaElement | null>(null);

  useEffect(() => {
    let cancelled = false;
    (async () => {
      setLoadingOpts(true);
      try {
        const combosList = await api.combos.list().catch(() => []);
        if (!cancelled) setCombos(combosList.filter((c) => !c.kind || c.kind === "llm"));
        const ps = await api.providers.list();
        const providerModels = await Promise.allSettled(ps.map((p) => api.providers.models(p.id)));
        if (cancelled) return;
        const groups: ModelGroup[] = [];
        ps.forEach((p, i) => {
          const r = providerModels[i];
          if (r.status !== "fulfilled") return;
          const models = r.value.filter((m) => m.kind === "llm" || !m.kind);
          if (models.length > 0) groups.push({ provider: p, models });
        });
        setModelGroups(groups);
      } finally {
        if (!cancelled) setLoadingOpts(false);
      }
    })();
    return () => { cancelled = true; };
  }, []);

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
                tokens: finalUsage ? { prompt: finalUsage.prompt_tokens, completion: finalUsage.completion_tokens, total: finalUsage.total_tokens } : undefined,
                tps,
              }
            : m
        )
      );
    } catch (err: any) {
      if (err?.name === "AbortError") {
        setMessages((prev) =>
          prev.map((m) =>
            m.id === assistantId ? { ...m, streaming: false, content: m.content + "\n\n" + t("playground.cancelled") } : m
          )
        );
      } else {
        setMessages((prev) =>
          prev.map((m) =>
            m.id === assistantId ? { ...m, streaming: false, error: err?.message ?? t("playground.error") } : m
          )
        );
      }
    } finally {
      setStreaming(false);
      abortRef.current = null;
    }
  }, [input, streaming, selectedModel, messages, combos, t]);

  const stop = () => abortRef.current?.abort();
  const clear = () => setMessages([]);

  const onKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === "Enter" && !e.shiftKey && !e.nativeEvent.isComposing) {
      e.preventDefault();
      send();
    }
  };

  const listItems: ModelComboBoxItem[] = [
    ...combos.map((c) => ({ id: c.name, itemType: "combo" as const, kind: c.kind || "llm", isActive: true })),
    ...modelGroups.flatMap((g) => g.models.map((m) => ({
      id: m.id,
      itemType: "model" as const,
      kind: m.kind || "llm",
      isActive: true,
    }))),
  ];

  return (
    <div className="flex flex-col h-full bg-background">
      <div className="shrink-0 h-12 border-b border-border bg-surface/80 backdrop-blur flex items-center justify-between px-4 gap-4">
        <div className="flex items-center gap-2 min-w-0">
          <IconChat className="w-4 h-4 text-accent shrink-0" />
          <span className="font-semibold text-sm shrink-0">{t("playground.title")}</span>
          <span className="text-muted/70 shrink-0">/</span>
          <ModelComboBox
            ariaLabel={t("playground.modelAria")}
            selectedKey={selectedModel || null}
            onSelectionChange={setSelectedModel}
            items={listItems}
            isDisabled={loadingOpts && combos.length === 0}
            className="w-72"
            inputPlaceholder={loadingOpts ? t("playground.modelPlaceholderLoading") : t("playground.modelPlaceholder")}
            inputGroupClassName="h-8 min-h-8"
            inputClassName="h-8 min-h-8 text-sm"
          />
        </div>
        <div className="flex items-center gap-1">
          {messages.length > 0 && (
            <Tooltip>
              <Tooltip.Trigger>
                <Button isIconOnly size="sm" variant="ghost" onPress={clear} isDisabled={streaming} aria-label={t("playground.newConversationAria")}>
                  <IconPlus className="w-4 h-4" />
                </Button>
              </Tooltip.Trigger>
              <Tooltip.Content>{t("playground.newConversation")}</Tooltip.Content>
            </Tooltip>
          )}
        </div>
      </div>

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

      <div className="shrink-0 bg-linear-to-t from-background via-background to-transparent pt-6 pb-4 px-4">
        <div className="mx-auto max-w-3xl">
          <div className="bg-surface border border-border rounded-2xl shadow-md focus-within:border-accent/50 focus-within:shadow-lg transition-all p-1">
            <TextField
              value={input}
              onChange={setInput}
              aria-label={t("playground.messageAria")}
              className="w-full"
            >
              <TextArea
                ref={textareaRef}
                onKeyDown={onKeyDown}
                placeholder={
                  selectedModel
                    ? t("playground.messagePlaceholder")
                    : t("playground.messagePlaceholderNoModel")
                }
                disabled={!selectedModel}
                rows={2}
                autoFocus
                className="resize-none"
                variant="secondary"
              />
            </TextField>
            <div className="flex items-center justify-between px-2 pb-2">
              <div className="text-[11px] text-muted pl-2">
                {t("playground.hint")}
              </div>
              <div className="flex items-center gap-1">
                {streaming ? (
                  <Button
                    size="sm"
                    variant="danger-soft"
                    onPress={stop}
                    isIconOnly
                    aria-label={t("playground.stopAria")}
                    className="h-8 w-8 min-w-8"
                  >
                    <IconStop className="w-4 h-4" />
                  </Button>
                ) : (
                  <Button
                    size="sm"
                    variant="primary"
                    isIconOnly
                    onPress={() => send()}
                    isDisabled={!input.trim() || !selectedModel}
                    aria-label={t("playground.sendAria")}
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

function WelcomeScreen({ disabled, onPick }: { disabled: boolean; onPick: (text: string) => void }) {
  const { t } = useTranslation();
  return (
    <div className="h-full flex items-center justify-center px-4">
      <div className="mx-auto max-w-2xl w-full text-center">
        <div className="inline-flex items-center justify-center w-12 h-12 rounded-2xl bg-accent/10 mb-5">
          <IconSparkles className="w-6 h-6 text-accent" />
        </div>
        <h1 className="text-2xl font-semibold tracking-tight mb-1">{t("playground.welcome")}</h1>
        <p className="text-sm text-muted mb-7">
          {t("playground.welcomeDesc")}
        </p>
        <div className="grid grid-cols-1 sm:grid-cols-2 gap-2 text-left">
          {SUGGESTIONS.map((s) => {
            const text = t(s);
            return (
              <Button
                key={s}
                variant="outline"
                isDisabled={disabled}
                onPress={() => onPick(text)}
                className="justify-start text-sm h-auto py-2.5"
              >
                <IconSparkles className="w-3.5 h-3.5 text-muted" />
                {text}
              </Button>
            );
          })}
        </div>
      </div>
    </div>
  );
}

function MessageRow({ msg }: { msg: PlaygroundMsg }) {
  const { t } = useTranslation();
  const isUser = msg.role === "user";
  return (
    <div className={`flex ${isUser ? "justify-end" : "justify-start"}`}>
      <div
        className={`min-w-0 ${
          isUser
            ? "max-w-[85%] bg-surface-secondary border border-border/60 text-foreground rounded-2xl px-4 py-3 shadow-sm"
            : "w-full text-foreground py-1"
        }`}
      >
        <div className="text-[15px] leading-relaxed whitespace-pre-wrap break-words text-foreground font-normal">
          {msg.error ? (
            <span className="text-danger font-medium">⚠ {msg.error}</span>
          ) : (
            <>
              {msg.content || (msg.streaming ? "" : "")}
              {msg.streaming && !msg.error && (
                <span className="inline-block w-1.5 h-4 bg-accent ml-0.5 animate-pulse rounded-sm align-middle" />
              )}
              {msg.streaming === false && msg.content === "" && !msg.error && (
                <span className="text-muted italic">{t("playground.emptyResponse")}</span>
              )}
            </>
          )}
        </div>

        {!isUser && !msg.streaming && !msg.error && (
          <div className="mt-2.5 flex flex-wrap items-center gap-x-3 gap-y-1 text-[11px] text-muted font-mono">
            {msg.combo && <span className="text-muted font-medium">{t("playground.comboLabel", { combo: msg.combo })}</span>}
            {msg.model && <span>{msg.model}</span>}
            {msg.ttftMs != null && msg.ttftMs > 0 && <span>ttft {formatLatency(msg.ttftMs)}</span>}
            {msg.latencyMs != null && <span>{formatLatency(msg.latencyMs)}</span>}
            {msg.tps != null && msg.tps > 0 && (
              <span className="text-success font-medium">{msg.tps.toFixed(1)} {t("playground.tokPerSec")}</span>
            )}
            {msg.tokens && (
              <span>
                {t("playground.tokensLabel", { prompt: msg.tokens.prompt, completion: msg.tokens.completion })}
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
