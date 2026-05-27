import React, { useState } from "react";
import { Text, Box, useInput, useFocus } from "ink";

interface ThinkingBlockProps {
  reasoning: string;
  collapsed?: boolean;
}

export function ThinkingBlock({
  reasoning,
  collapsed: initialCollapsed = true,
}: ThinkingBlockProps): React.JSX.Element {
  const [collapsed, setCollapsed] = useState(initialCollapsed);
  const { isFocused } = useFocus({ isActive: true, autoFocus: false });

  useInput(
    (input, key) => {
      if (key.return || input === " ") {
        setCollapsed((prev) => !prev);
      }
    },
    { isActive: isFocused },
  );

  if (!reasoning) {
    return <></>;
  }

  return (
    <Box
      flexDirection="column"
      borderStyle="round"
      borderColor="gray"
      borderDimColor
      paddingX={1}
      marginBottom={1}
    >
      <Box>
        <Text dimColor={!isFocused}>
          {"🤔 Thinking... "}
          {collapsed ? "[+]" : "[-]"}
          {isFocused && " ← Enter/Space to toggle"}
        </Text>
      </Box>
      {!collapsed && (
        <Box flexDirection="column" marginTop={1}>
          {reasoning.split("\n").map((line, i) => (
            <Text key={i} dimColor>
              {line}
            </Text>
          ))}
        </Box>
      )}
    </Box>
  );
}
