import type { TooltipProps } from "recharts";

export function ChartTooltip({ active, payload, label }: TooltipProps<number, string>) {
  if (!active || !payload || payload.length === 0) return null;
  return (
    <div
      style={{
        background: "var(--overlay)",
        border: "1px solid var(--border)",
        borderRadius: "calc(var(--radius) * 1.5)",
        boxShadow: "var(--overlay-shadow)",
        padding: "0.5rem 0.75rem",
        fontSize: "0.75rem",
        color: "var(--overlay-foreground)",
      }}
    >
      {label != null && (
        <div style={{ color: "var(--muted)", marginBottom: "0.25rem", fontSize: "0.6875rem" }}>
          {label}
        </div>
      )}
      {payload.map((entry, i) => (
        <div key={i} style={{ display: "flex", alignItems: "center", gap: "0.5rem" }}>
          {entry.color && (
            <span
              style={{
                display: "inline-block",
                width: "0.5rem",
                height: "0.5rem",
                borderRadius: "9999px",
                background: entry.color,
              }}
            />
          )}
          <span style={{ color: "var(--overlay-foreground)" }}>
            {entry.name}: <strong style={{ fontVariantNumeric: "tabular-nums" }}>
              {typeof entry.value === "number" ? entry.value.toLocaleString("en-US") : entry.value}
            </strong>
          </span>
        </div>
      ))}
    </div>
  );
}