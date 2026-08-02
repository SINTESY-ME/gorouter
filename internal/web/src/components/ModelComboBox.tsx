import { ComboBox, Input, ListBox } from "@heroui/react";

export interface ModelComboBoxItem {
  id: string;
  itemType: "model" | "combo";
  kind: string;
  isActive: boolean;
}

interface ModelComboBoxProps {
  items: ModelComboBoxItem[];
  className?: string;
  ariaLabel: string;
  inputPlaceholder: string;
  inputVariant?: "primary" | "secondary";
  inputGroupClassName?: string;
  inputClassName?: string;
  selectionMode?: "single" | "multiple";
  selectedKey?: string | null;
  onSelectionChange?: (key: string) => void;
  selectedKeys?: string[];
  onSelectedKeysChange?: (keys: string[]) => void;
  inputValue?: string;
  onInputChange?: (value: string) => void;
  valuePlaceholder?: string;
  isDisabled?: boolean;
}

const KIND_TEXT: Record<string, string> = {
  llm: "text-accent",
  embedding: "text-success",
  image: "text-warning",
  tts: "text-muted",
  stt: "text-danger",
  rerank: "text-muted",
  ocr: "text-muted",
  video: "text-muted",
};

export function ModelComboBox({
  items,
  className,
  ariaLabel,
  inputPlaceholder,
  inputVariant,
  inputGroupClassName,
  inputClassName,
  selectionMode = "single",
  selectedKey,
  onSelectionChange,
  selectedKeys,
  onSelectedKeysChange,
  inputValue,
  onInputChange,
  valuePlaceholder,
  isDisabled,
}: ModelComboBoxProps) {
  const isMultiple = selectionMode === "multiple";

  return (
    <ComboBox
      className={className}
      aria-label={ariaLabel}
      selectionMode={selectionMode}
      selectedKey={isMultiple ? undefined : selectedKey}
      onSelectionChange={isMultiple ? undefined : (key) => onSelectionChange?.(String(key))}
      value={isMultiple ? selectedKeys : undefined}
      onChange={isMultiple ? (keys) => onSelectedKeysChange?.(keys == null ? [] : Array.isArray(keys) ? keys.map(String) : [String(keys)]) : undefined}
      inputValue={inputValue}
      onInputChange={onInputChange}
      isDisabled={isDisabled}
    >
      <ComboBox.InputGroup className={inputGroupClassName}>
        <Input placeholder={inputPlaceholder} variant={inputVariant} className={inputClassName} />
        <ComboBox.Trigger />
      </ComboBox.InputGroup>
      {valuePlaceholder && <ComboBox.Value placeholder={valuePlaceholder} />}
      <ComboBox.Popover>
        <ListBox items={items} className="max-h-80 overflow-y-auto">
          {(item) => (
            <ListBox.Item id={item.id} textValue={item.id}>
              <div className="flex items-center justify-between w-full gap-2 min-w-0">
                <div className="flex items-center gap-2 min-w-0">
                  {item.itemType === "combo" && <IconStack />}
                  <span className="font-mono text-xs truncate">{item.id}</span>
                </div>
                <div className="flex items-center gap-1 shrink-0">
                  {item.itemType === "model" && !item.isActive && (
                    <span className="rounded-full bg-warning/15 px-1.5 py-0.5 text-[10px] font-medium text-warning">inativo</span>
                  )}
                  <span className={`rounded-full bg-surface-secondary px-1.5 py-0.5 text-[10px] font-medium ${item.itemType === "combo" ? "text-muted" : (KIND_TEXT[item.kind] ?? "text-muted")}`}>
                    {item.itemType === "combo" ? "combo" : item.kind}
                  </span>
                </div>
              </div>
              <ListBox.ItemIndicator />
            </ListBox.Item>
          )}
        </ListBox>
      </ComboBox.Popover>
    </ComboBox>
  );
}

function IconStack() {
  return (
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" className="w-3 h-3 shrink-0 text-muted">
      <polygon points="12 2 2 7 12 12 22 7 12 2" />
      <polyline points="2 17 12 22 12 17" />
      <polyline points="2 12 12 17 22 12" />
    </svg>
  );
}
