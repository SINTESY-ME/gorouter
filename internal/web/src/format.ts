// Number formatting utilities for the dashboard. Uses Intl.NumberFormat with
// compact notation (8.2K, 7.6M, 1.2B) for display, keeping full precision in
// tooltips/hover where the exact value matters.

const compactFmt = new Intl.NumberFormat("en-US", {
  notation: "compact",
  maximumFractionDigits: 1,
});

const fullFmt = new Intl.NumberFormat("en-US");

// formatCompact returns a short representation: 8207 -> "8.2K", 7624989 -> "7.6M".
export function formatCompact(n: number): string {
  if (n === 0) return "0";
  return compactFmt.format(n);
}

// formatFull returns the full number with grouping: 7624989 -> "7,624,989".
export function formatFull(n: number): string {
  return fullFmt.format(n);
}

// formatCost formats a dollar amount: small values get 4 decimals, large
// values use compact notation with a $ prefix.
export function formatCost(n: number): string {
  if (n === 0) return "$0.00";
  if (n < 0.0001) return `$${n.toExponential(2)}`;
  if (n < 1) return `$${n.toFixed(4)}`;
  if (n < 1000) return `$${n.toFixed(2)}`;
  return `$${compactFmt.format(n)}`;
}