import React from "react";
import { Text, Box } from "ink";
import { ChatInput } from "./input.tsx";

interface PromptProps {
  onSubmit: (text: string) => void;
  disabled?: boolean;
  model?: string;
  provider?: string;
  showKeybindHint?: boolean;
}

export function Prompt({
  onSubmit,
  disabled,
  model,
  provider,
  showKeybindHint,
}: PromptProps): React.JSX.Element {
  return (
    <Box flexDirection="column" paddingX={1} paddingY={1}>
      {(provider || model) && (
        <Box marginBottom={1}>
          <Text color="cyan">{"◉ "}</Text>
          <Text>
            {provider && model
              ? `${provider}/${model}`
              : (provider ?? model ?? "")}
          </Text>
        </Box>
      )}
      <Box>
        <Box flexGrow={1}>
          <ChatInput onSubmit={onSubmit} disabled={disabled} />
        </Box>
        {showKeybindHint && (
          <Box>
            <Text dimColor> Ctrl+K commands</Text>
          </Box>
        )}
      </Box>
    </Box>
  );
}
