import React, { useState, useRef, useCallback, useEffect } from "react";
import { Text, Box, useInput } from "ink";

interface MultilineInputProps {
  value: string;
  onChange: (value: string) => void;
  onSubmit: () => void;
  placeholder?: string;
  focus?: boolean;
}

export function MultilineInput({
  value,
  onChange,
  onSubmit,
  placeholder = "",
  focus = true,
}: MultilineInputProps): React.JSX.Element {
  const [cursor, setCursor] = useState(value.length);
  const historyRef = useRef<string[]>([]);
  const historyIdxRef = useRef(-1);
  const savedDraftRef = useRef("");

  // Keep cursor in bounds when value changes externally
  useEffect(() => {
    setCursor((c) => Math.min(c, value.length));
  }, [value]);

  const commitToHistory = useCallback((text: string) => {
    if (text.trim().length === 0) return;
    const h = historyRef.current;
    // Don't duplicate consecutive identical entries
    if (h.length === 0 || h[h.length - 1] !== text) {
      h.push(text);
      if (h.length > 100) h.shift();
    }
    historyIdxRef.current = -1;
    savedDraftRef.current = "";
  }, []);

  useInput((input, key) => {
    if (!focus) return;

    // Ctrl+Enter = submit
    if (key.return && key.ctrl) {
      if (value.trim().length > 0) {
        commitToHistory(value);
        onSubmit();
      }
      return;
    }

    // Enter = newline
    if (key.return) {
      const pos = cursor;
      onChange(value.slice(0, pos) + "\n" + value.slice(pos));
      setCursor(pos + 1);
      return;
    }

    // Tab = 2 spaces
    if (key.tab) {
      const pos = cursor;
      onChange(value.slice(0, pos) + "  " + value.slice(pos));
      setCursor(pos + 2);
      return;
    }

    // Arrow keys
    if (key.upArrow) {
      const before = value.lastIndexOf("\n", cursor - 1);
      if (before === -1 && historyIdxRef.current === -1) {
        // At top of input — go to history
        savedDraftRef.current = value;
        historyIdxRef.current = historyRef.current.length - 1;
        if (historyIdxRef.current >= 0) {
          onChange(historyRef.current[historyIdxRef.current]);
          setCursor(historyRef.current[historyIdxRef.current].length);
        }
        return;
      }
      if (before === -1) {
        setCursor(0);
        return;
      }
      const lineLen = cursor - before - 1;
      const prevBefore = value.lastIndexOf("\n", before - 1);
      const prevLineLen = before - prevBefore - 1;
      setCursor(
        Math.min(prevBefore + 1 + Math.min(lineLen, prevLineLen), before),
      );
      return;
    }

    if (key.downArrow) {
      // Exit history mode
      if (historyIdxRef.current >= 0) {
        historyIdxRef.current--;
        if (historyIdxRef.current >= 0) {
          onChange(historyRef.current[historyIdxRef.current]);
          setCursor(historyRef.current[historyIdxRef.current].length);
        } else {
          onChange(savedDraftRef.current);
          setCursor(savedDraftRef.current.length);
        }
        return;
      }
      const after = value.indexOf("\n", cursor);
      if (after === -1) return;
      const nextAfter = value.indexOf("\n", after + 1);
      const lineEnd = nextAfter === -1 ? value.length : nextAfter;
      const col = cursor - (value.lastIndexOf("\n", cursor - 1) + 1);
      setCursor(Math.min(after + 1 + col, lineEnd));
      return;
    }

    if (key.leftArrow) {
      if (cursor > 0) setCursor(cursor - 1);
      return;
    }

    if (key.rightArrow) {
      if (cursor < value.length) setCursor(cursor + 1);
      return;
    }

    // Ctrl+A = beginning of line
    if (input === "\x01") {
      const lineStart = value.lastIndexOf("\n", cursor - 1) + 1;
      setCursor(lineStart);
      return;
    }

    // Ctrl+E = end of line
    if (input === "\x05") {
      const lineEnd = value.indexOf("\n", cursor);
      setCursor(lineEnd === -1 ? value.length : lineEnd);
      return;
    }

    // Ctrl+K = kill to end of line
    if (input === "\x0b") {
      const lineEnd = value.indexOf("\n", cursor);
      const end = lineEnd === -1 ? value.length : lineEnd;
      onChange(value.slice(0, cursor) + value.slice(end));
      return;
    }

    // Ctrl+U = kill to start of line
    if (input === "\x15") {
      const lineStart = value.lastIndexOf("\n", cursor - 1) + 1;
      onChange(value.slice(0, lineStart) + value.slice(cursor));
      setCursor(lineStart);
      return;
    }

    // Ctrl+W = kill word backward
    if (input === "\x17") {
      let i = cursor - 1;
      while (i > 0 && value[i] === " ") i--;
      while (i > 0 && value[i - 1] !== " ") i--;
      onChange(value.slice(0, i) + value.slice(cursor));
      setCursor(i);
      return;
    }

    // Backspace
    if (key.backspace || key.delete) {
      if (cursor > 0) {
        onChange(value.slice(0, cursor - 1) + value.slice(cursor));
        setCursor(cursor - 1);
      }
      return;
    }

    // Printable characters
    if (input.length === 1 && input >= " ") {
      onChange(value.slice(0, cursor) + input + value.slice(cursor));
      setCursor(cursor + 1);
    }
  });

  // Render with cursor inverted
  const renderLine = (line: string, lineIdx: number, lineStart: number) => {
    const lineEnd = lineStart + line.length;
    const cursorInLine = cursor >= lineStart && cursor <= lineEnd;
    if (!cursorInLine) return <Text key={lineIdx}>{line}</Text>;

    const localCursor = cursor - lineStart;
    const before = line.slice(0, localCursor);
    const at = line[localCursor] || " ";
    const after = line.slice(localCursor + 1);

    return (
      <Text key={lineIdx}>
        {before}
        <Text inverse>{at}</Text>
        {after}
      </Text>
    );
  };

  const lines = value.split("\n");
  let charCount = 0;

  if (lines.length === 0 && placeholder) {
    return (
      <Box>
        <Text color="gray">{"> "}</Text>
        <Text dimColor>{placeholder}</Text>
      </Box>
    );
  }

  return (
    <Box flexDirection="column">
      {lines.map((line, i) => {
        const lineStart = charCount;
        charCount += line.length + 1; // +1 for newline
        return (
          <Box key={i}>
            <Text color="gray">{i === 0 ? "> " : "  "}</Text>
            {renderLine(line, i, lineStart)}
          </Box>
        );
      })}
    </Box>
  );
}
