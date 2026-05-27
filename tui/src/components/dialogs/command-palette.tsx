import React, { useState, useMemo, useCallback } from "react";
import { Box, Text, useInput } from "ink";
import { TextInput } from "../text-input.tsx";
import { DialogContainer } from "./dialog-container.tsx";

interface Command {
  id: string;
  title: string;
  description: string;
  keybind?: string;
  action: () => void;
}

interface CommandPaletteProps {
  open: boolean;
  onClose: () => void;
  commands: Command[];
}

function fuzzyMatch(
  query: string,
  text: string,
): { score: number; indices: number[] } {
  const lowerQuery = query.toLowerCase();
  const lowerText = text.toLowerCase();

  if (lowerQuery.length === 0) return { score: 1, indices: [] };

  let qi = 0;
  const indices: number[] = [];
  let score = 0;
  let lastMatchIdx = -1;

  for (let ti = 0; ti < lowerText.length && qi < lowerQuery.length; ti++) {
    if (lowerText[ti] === lowerQuery[qi]) {
      indices.push(ti);
      // Consecutive match bonus
      if (lastMatchIdx === ti - 1) {
        score += 2;
      } else {
        score += 1;
      }
      // Start of word bonus
      if (ti === 0 || lowerText[ti - 1] === " " || lowerText[ti - 1] === "/") {
        score += 2;
      }
      lastMatchIdx = ti;
      qi++;
    }
  }

  if (qi < lowerQuery.length) return { score: -1, indices: [] };
  return { score, indices };
}

function highlightText(text: string, indices: number[]): React.JSX.Element[] {
  if (indices.length === 0) return [<Text key={0}>{text}</Text>];

  const parts: React.JSX.Element[] = [];
  let lastIdx = 0;
  const indexSet = new Set(indices);

  for (let i = 0; i < text.length; i++) {
    if (indexSet.has(i)) {
      if (i > lastIdx) {
        parts.push(<Text key={`t-${lastIdx}`}>{text.slice(lastIdx, i)}</Text>);
      }
      parts.push(
        <Text key={`h-${i}`} color="cyan" bold>
          {text[i]}
        </Text>,
      );
      lastIdx = i + 1;
    }
  }

  if (lastIdx < text.length) {
    parts.push(<Text key={`t-${lastIdx}`}>{text.slice(lastIdx)}</Text>);
  }

  return parts;
}

export function CommandPalette({
  open,
  onClose,
  commands,
}: CommandPaletteProps): React.JSX.Element | null {
  const [query, setQuery] = useState("");
  const [selectedIdx, setSelectedIdx] = useState(0);

  const filtered = useMemo(() => {
    if (query.length === 0) {
      return commands.map((cmd) => ({
        cmd,
        score: 1,
        indices: [] as number[],
        field: "title" as const,
      }));
    }

    return commands
      .map((cmd) => {
        const titleMatch = fuzzyMatch(query, cmd.title);
        const descMatch = fuzzyMatch(query, cmd.description);
        const best =
          titleMatch.score >= descMatch.score
            ? { ...titleMatch, field: "title" as const }
            : { ...descMatch, field: "description" as const };
        return {
          cmd,
          score: best.score,
          indices: best.indices,
          field: best.field,
        };
      })
      .filter((r) => r.score >= 0)
      .sort((a, b) => b.score - a.score);
  }, [query, commands]);

  useInput((_input, key) => {
    if (key.upArrow) {
      setSelectedIdx((prev) => Math.max(0, prev - 1));
    } else if (key.downArrow) {
      setSelectedIdx((prev) => Math.min(filtered.length - 1, prev + 1));
    } else if (key.return) {
      if (filtered.length > 0 && selectedIdx < filtered.length) {
        const selected = filtered[selectedIdx];
        if (selected) {
          onClose();
          selected.cmd.action();
        }
      }
    }
  });

  // Reset selection when query changes
  const handleQueryChange = useCallback((val: string) => {
    setQuery(val);
    setSelectedIdx(0);
  }, []);

  if (!open) return null;

  return (
    <DialogContainer open={open} onClose={onClose} title="Commands">
      <Box marginBottom={1}>
        <Text color="gray">{"> "}</Text>
        <TextInput
          value={query}
          onChange={handleQueryChange}
          placeholder="Type to search commands..."
        />
      </Box>

      <Box flexDirection="column">
        {filtered.length === 0 && (
          <Box paddingX={1}>
            <Text dimColor>No matching commands</Text>
          </Box>
        )}
        {filtered.slice(0, 8).map((result, i) => {
          const isSelected = i === selectedIdx;
          const titleIndices = result.field === "title" ? result.indices : [];
          const descIndices =
            result.field === "description" ? result.indices : [];

          return (
            <Box key={result.cmd.id} paddingX={1}>
              <Text color={isSelected ? "cyan" : undefined}>
                {isSelected ? "▸ " : "  "}
              </Text>
              <Box flexDirection="column">
                <Box>
                  <Text
                    color={isSelected ? "white" : undefined}
                    bold={isSelected}
                  >
                    {titleIndices.length > 0
                      ? highlightText(result.cmd.title, titleIndices)
                      : result.cmd.title}
                  </Text>
                  {result.cmd.keybind && (
                    <Text dimColor> {result.cmd.keybind}</Text>
                  )}
                </Box>
                <Text dimColor>
                  {descIndices.length > 0
                    ? highlightText(result.cmd.description, descIndices)
                    : result.cmd.description}
                </Text>
              </Box>
            </Box>
          );
        })}
      </Box>
    </DialogContainer>
  );
}
