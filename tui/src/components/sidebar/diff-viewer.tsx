import React from "react";
import { Box, Text } from "ink";

interface DiffViewerProps {
  oldText: string;
  newText: string;
  maxWidth?: number;
}

const MAX_LINES = 20;

interface DiffLine {
  type: "context" | "added" | "removed";
  content: string;
}

function computeDiff(oldLines: string[], newLines: string[]): DiffLine[] {
  const result: DiffLine[] = [];

  // Simple line-by-line diff using LCS approach
  const oldSet = new Set(oldLines);
  const newSet = new Set(newLines);

  // Lines only in old: removed
  for (const line of oldLines) {
    if (!newSet.has(line)) {
      result.push({ type: "removed", content: line });
    } else {
      result.push({ type: "context", content: line });
    }
  }

  // Lines only in new: added
  for (const line of newLines) {
    if (!oldSet.has(line)) {
      result.push({ type: "added", content: line });
    }
  }

  return result.slice(0, MAX_LINES);
}

function DiffLine({ type, content }: DiffLine): React.JSX.Element {
  switch (type) {
    case "added":
      return (
        <Box>
          <Text color="green">+ </Text>
          <Text color="green">{content}</Text>
        </Box>
      );
    case "removed":
      return (
        <Box>
          <Text color="red">- </Text>
          <Text color="red">{content}</Text>
        </Box>
      );
    default:
      return (
        <Box>
          <Text dimColor> </Text>
          <Text dimColor>{content}</Text>
        </Box>
      );
  }
}

export function DiffViewer({
  oldText,
  newText,
  maxWidth = 26,
}: DiffViewerProps): React.JSX.Element {
  const oldLines = oldText.split("\n");
  const newLines = newText.split("\n");
  const diffLines = computeDiff(oldLines, newLines);

  const addedCount = diffLines.filter((l) => l.type === "added").length;
  const removedCount = diffLines.filter((l) => l.type === "removed").length;

  return (
    <Box flexDirection="column" overflow="hidden">
      <Box paddingX={1}>
        <Text color="green">+{addedCount}</Text>
        <Text dimColor> </Text>
        <Text color="red">-{removedCount}</Text>
      </Box>
      <Box paddingX={1}>
        <Text dimColor>{"─".repeat(26)}</Text>
      </Box>
      {diffLines.length === 0 && (
        <Box paddingX={1}>
          <Text dimColor>No changes</Text>
        </Box>
      )}
      {diffLines.map((line, i) => (
        <DiffLine
          key={i}
          type={line.type}
          content={line.content.slice(0, maxWidth)}
        />
      ))}
    </Box>
  );
}
