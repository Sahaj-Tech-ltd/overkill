import React, { useState, useRef, useCallback, useEffect } from "react";
import { Box, Text, useInput, useStdout } from "ink";

export interface ToolCall {
  id: string;
  name: string;
  status: "running" | "success" | "error";
  output: string;
  timestamp: number;
}

interface ToolOutputProps {
  toolCalls: ToolCall[];
}

function truncate(text: string, maxLen: number): string {
  if (text.length <= maxLen) return text;
  return text.slice(0, maxLen - 3) + "...";
}

function StatusIcon({
  status,
}: {
  status: ToolCall["status"];
}): React.JSX.Element {
  switch (status) {
    case "running":
      return <Text color="cyan">⟳</Text>;
    case "success":
      return <Text color="green">✓</Text>;
    case "error":
      return <Text color="red">✗</Text>;
  }
}

export function ToolOutput({ toolCalls }: ToolOutputProps): React.JSX.Element {
  const [expandedId, setExpandedId] = useState<string | null>(null);
  const [scrollOffset, setScrollOffset] = useState(0);
  const { stdout } = useStdout();
  const termHeight = stdout.rows ?? 24;
  const maxVisible = Math.max(5, termHeight - 10);

  useInput((input, key) => {
    if (key.upArrow && expandedId) {
      setScrollOffset((prev) => Math.max(0, prev - 1));
    } else if (key.downArrow && expandedId) {
      setScrollOffset((prev) => prev + 1);
    } else if (key.escape && expandedId) {
      setExpandedId(null);
      setScrollOffset(0);
    } else if (key.return) {
      if (expandedId) {
        setExpandedId(null);
        setScrollOffset(0);
      }
    }
  });

  if (toolCalls.length === 0) {
    return (
      <Box paddingX={1}>
        <Text dimColor>No tool calls yet</Text>
      </Box>
    );
  }

  const expanded = toolCalls.find((tc) => tc.id === expandedId);

  if (expanded) {
    const lines = expanded.output.split("\n");
    const visibleLines = lines.slice(scrollOffset, scrollOffset + maxVisible);

    return (
      <Box flexDirection="column" overflow="hidden">
        <Box paddingX={1}>
          <StatusIcon status={expanded.status} />
          <Text bold> {expanded.name}</Text>
        </Box>
        <Box paddingX={1}>
          <Text dimColor>{"─".repeat(26)}</Text>
        </Box>
        {visibleLines.map((line, i) => (
          <Box key={i} paddingX={1}>
            <Text color="white">{truncate(line, 26)}</Text>
          </Box>
        ))}
        {lines.length > maxVisible && (
          <Box paddingX={1}>
            <Text dimColor>
              {scrollOffset + 1}-{scrollOffset + visibleLines.length}/
              {lines.length}
            </Text>
          </Box>
        )}
        <Box paddingX={1} marginTop={1}>
          <Text dimColor>Esc to collapse</Text>
        </Box>
      </Box>
    );
  }

  return (
    <Box flexDirection="column" overflow="hidden">
      {toolCalls.map((tc) => (
        <Box key={tc.id} paddingX={1}>
          <StatusIcon status={tc.status} />
          <Text> </Text>
          <Box flexDirection="column" flexGrow={1}>
            <Text bold>{tc.name.slice(0, 22)}</Text>
            <Text dimColor>{truncate(tc.output.split("\n")[0] ?? "", 24)}</Text>
          </Box>
        </Box>
      ))}
      <Box paddingX={1} marginTop={1}>
        <Text dimColor>Enter to expand · Esc close</Text>
      </Box>
    </Box>
  );
}
