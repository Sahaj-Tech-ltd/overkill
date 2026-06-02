import React from "react";
import { Text } from "ink";

interface SearchBoxProps {
  query: string;
  placeholder: string;
  isFocused: boolean;
  isTerminalFocused: boolean;
  cursorOffset: number;
  borderless?: boolean;
}

export function SearchBox({
  query,
  placeholder,
  isFocused,
  isTerminalFocused,
  cursorOffset,
  borderless = false,
}: SearchBoxProps): React.JSX.Element {
  const showCursor = isFocused && isTerminalFocused;
  const prefix = borderless ? "  " : "⌕ ";

  // Empty query: show dim placeholder (⌕ or just text)
  if (query.length === 0 && placeholder) {
    return (
      <Text dimColor>
        {prefix}
        {placeholder}
      </Text>
    );
  }

  // Empty query, no placeholder
  if (query.length === 0) {
    return (
      <Text>
        {prefix}
        {showCursor ? <Text inverse> </Text> : " "}
      </Text>
    );
  }

  const safeOffset = Math.min(cursorOffset, query.length);
  const before = query.slice(0, safeOffset);
  const at = query[safeOffset] || " ";
  const after = query.slice(safeOffset + 1);

  return (
    <Text>
      {prefix}
      {before}
      {showCursor ? <Text inverse>{at}</Text> : at}
      {after}
    </Text>
  );
}
