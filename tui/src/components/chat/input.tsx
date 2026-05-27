import React, { useState } from "react";
import { Text, Box, useInput } from "ink";
import TextInput from "ink-text-input";

interface ChatInputProps {
  onSubmit: (text: string) => void;
  disabled?: boolean;
}

export function ChatInput({
  onSubmit,
  disabled,
}: ChatInputProps): React.JSX.Element {
  const [value, setValue] = useState("");

  useInput((input, key) => {
    if (key.return && !disabled) {
      const trimmed = value.trim();
      if (trimmed.length > 0) {
        onSubmit(trimmed);
        setValue("");
      }
    }
  });

  return (
    <Box>
      <Text color="gray">{"> "}</Text>
      <TextInput
        value={value}
        onChange={setValue}
        placeholder={disabled ? "Thinking..." : "Type a message..."}
        focus={!disabled}
      />
    </Box>
  );
}
