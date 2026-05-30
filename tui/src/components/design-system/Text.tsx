import React from "react";
import { Text as InkText } from "ink";
import { useTheme } from "../../lib/theme.ts";

// ─── Types ─────────────────────────────────────────────────────────────────

export type TextVariant = "body" | "muted" | "heading" | "code";

export interface TextProps {
  readonly variant?: TextVariant;
  readonly bold?: boolean;
  readonly italic?: boolean;
  readonly underline?: boolean;
  readonly strikethrough?: boolean;
  readonly wrap?: "wrap" | "end" | "middle" | "truncate-end" | "truncate" | "truncate-middle" | "truncate-start";
  readonly children: React.ReactNode;
}

// ─── Component ────────────────────────────────────────────────────────────

export function Text({
  variant = "body",
  bold,
  italic,
  underline,
  strikethrough,
  wrap,
  children,
}: TextProps): React.JSX.Element {
  const theme = useTheme();

  const colorMap: Record<TextVariant, { color: string; dim?: boolean; b?: boolean }> = {
    body: { color: theme.text },
    muted: { color: theme.muted, dim: true },
    heading: { color: theme.text, b: true },
    code: { color: theme.accent },
  };

  const style = colorMap[variant];

  return (
    <InkText
      color={style.color}
      dimColor={style.dim}
      bold={bold ?? style.b}
      italic={italic}
      underline={underline}
      strikethrough={strikethrough}
      wrap={wrap}
    >
      {children}
    </InkText>
  );
}
