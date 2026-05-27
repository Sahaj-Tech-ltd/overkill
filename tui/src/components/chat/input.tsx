import React, { useState } from "react";
import { MultilineInput } from "./multiline-input";

interface ChatInputProps {
  onSubmit: (text: string) => void;
  disabled?: boolean;
}

export function ChatInput({ onSubmit, disabled }: ChatInputProps): React.JSX.Element {
  const [value, setValue] = useState("");

  return (
    <MultilineInput
      value={value}
      onChange={setValue}
      onSubmit={() => {
        const trimmed = value.trim();
        if (trimmed.length > 0) {
          onSubmit(trimmed);
          setValue("");
        }
      }}
      placeholder={disabled ? "Thinking..." : "Type a message... (Ctrl+Enter to send)"}
      focus={!disabled}
    />
  );
}
