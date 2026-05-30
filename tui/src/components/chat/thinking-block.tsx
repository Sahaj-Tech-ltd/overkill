import React, { useState } from "react";
import { Text, Box } from "ink";
import type { Theme } from "../../themes/definitions.ts";

interface ThinkingBlockProps {
  reasoning: string;
  collapsed?: boolean;
  /** Duration of thinking in milliseconds */
  duration?: number;
  /** Current theme for accent colors */
  theme: Theme;
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
}: ThinkingBlockProps): React.JSX.Element {
  const [collapsed, setCollapsed] = useState(initialCollapsed);

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
