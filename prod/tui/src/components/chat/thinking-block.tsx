import React, { useState } from "react";
import { Text, Box, useInput } from "ink";
import type { Theme } from "../../themes/definitions.ts";

interface ThinkingBlockProps {
  reasoning: string;
  collapsed?: boolean;
  /** Duration of thinking in milliseconds */
  duration?: number;
  /** Current theme for accent colors */
  theme: Theme;
  /** Called when user toggles collapse via keyboard */
  onToggleCollapse?: () => void;
}

function formatDuration(ms: number): string {
  if (ms < 1000) return `${ms}ms`;
  if (ms < 60000) return `${(ms / 1000).toFixed(1)}s`;
  const mins = Math.floor(ms / 60000);
  const secs = ((ms % 60000) / 1000).toFixed(1);
  return `${mins}m ${secs}s`;
}

export function ThinkingBlock({
  reasoning,
  collapsed: initialCollapsed = true,
  duration,
  theme,
  onToggleCollapse,
}: ThinkingBlockProps): React.JSX.Element {
  const [collapsed, setCollapsed] = useState(initialCollapsed);

  // Toggle collapse via keyboard: Ctrl+T or just "t" when no text input is focused
  useInput((input, key) => {
    if (input === "t" && key.ctrl) {
      setCollapsed((prev) => !prev);
      onToggleCollapse?.();
    }
  });

  if (!reasoning) {
    return <></>;
  }

  const timeStr = duration ? ` (${formatDuration(duration)})` : "";

  return (
    <Box
      flexDirection="column"
      borderStyle="round"
      borderColor={theme.accent}
      paddingX={1}
      marginBottom={1}
    >
      <Box>
        <Text color={theme.accent}>
          🤔 Thinking{timeStr}{" "}
          <Text dimColor>(Ctrl+T to {collapsed ? "expand" : "collapse"})</Text>{" "}
          {collapsed ? "[+]" : "[-]"}
        </Text>
      </Box>
      {!collapsed && (
        <Box flexDirection="column" marginTop={1}>
          {reasoning.split("\n").map((line, i) => (
            <Text key={i} dimColor color={theme.text}>
              {line}
            </Text>
          ))}
        </Box>
      )}
    </Box>
  );
}
