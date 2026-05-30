import React from "react";
import { MultilineInput } from "./multiline-input";
import { useInputHistory } from "../../hooks/useInputHistory";

interface ChatInputProps {
  onSubmit: (text: string) => void;
  disabled?: boolean;
}

export function ChatInput({
  onSubmit,
  disabled,
}: ChatInputProps): React.JSX.Element {
  const { value, setValue, handleKeyDown, recordSubmit } = useInputHistory();

  return (
    <MultilineInput
      value={value}
      onChange={setValue}
      onKeyDown={handleKeyDown}
      onSubmit={() => {
        const trimmed = value.trim();
        if (trimmed.length > 0) {
          recordSubmit(trimmed);
          onSubmit(trimmed);
        }
      }}
      placeholder={
        disabled ? "Thinking..." : "Type a message... (Ctrl+Enter to send)"
      }
      focus={!disabled}
    />
  );
}
