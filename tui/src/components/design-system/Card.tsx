import React from "react";
import { Box, Text } from "ink";
import { useTheme } from "../../lib/theme.ts";

// ─── Types ─────────────────────────────────────────────────────────────────

export interface CardProps {
  readonly title?: string;
  readonly children: React.ReactNode;
  readonly padding?: number;
}

// ─── Component ────────────────────────────────────────────────────────────

export function Card({
  title,
  children,
  padding = 1,
}: CardProps): React.JSX.Element {
  const theme = useTheme();

  return (
    <Box
      flexDirection="column"
      borderStyle="round"
      borderColor={theme.border}
      paddingX={padding}
      paddingY={title ? 0 : padding}
    >
      {title ? (
        <Box marginBottom={1}>
          <Text bold color={theme.accent}>
            {title}
          </Text>
        </Box>
      ) : null}
      <Box paddingY={title ? padding : 0} paddingX={title ? padding : 0}>
        {children}
      </Box>
    </Box>
  );
}
