import React from "react";
import { Text } from "ink";
import { useTheme } from "../../lib/theme.ts";

// ─── Types ─────────────────────────────────────────────────────────────────

export type BadgeVariant = "info" | "success" | "warning" | "danger";

export interface BadgeProps {
  readonly variant?: BadgeVariant;
  readonly label: string;
}

// ─── Variant color mapping ────────────────────────────────────────────────

function variantColor(
  variant: BadgeVariant,
  theme: ReturnType<typeof useTheme>,
): { bg: string; fg: string } {
  switch (variant) {
    case "info":
      return { bg: theme.accent, fg: theme.background };
    case "success":
      return { bg: theme.success, fg: theme.background };
    case "warning":
      return { bg: theme.warning, fg: theme.background };
    case "danger":
      return { bg: theme.error, fg: theme.background };
  }
}

// ─── Component ────────────────────────────────────────────────────────────

export function Badge({
  variant = "info",
  label,
}: BadgeProps): React.JSX.Element {
  const theme = useTheme();
  const colors = variantColor(variant, theme);

  return (
    <Text color={colors.fg} backgroundColor={colors.bg}>
      {" "}
      {label}{" "}
    </Text>
  );
}
