import React from "react";
import { Box, useInput } from "ink";
import type { Message } from "../../backend/types.ts";
import { MessageList } from "./message-list.tsx";
import { Prompt } from "./prompt.tsx";

interface ChatViewProps {
  messages: Message[];
  sendMessage: (text: string) => Promise<void>;
  clearChat: () => void;
  isLoading: boolean;
  streamingText?: string;
  model?: string;
  provider?: string;
  onOpenPalette: () => void;
  isDialogOpen: boolean;
}

export function ChatView({
  messages,
  sendMessage,
  clearChat,
  isLoading,
  streamingText,
  model,
  provider,
  onOpenPalette,
  isDialogOpen,
}: ChatViewProps): React.JSX.Element {
  useInput((input, key) => {
    if (isDialogOpen) return;

    if (key.ctrl && input === "l") {
      clearChat();
    }
    if (key.ctrl && input === "k") {
      onOpenPalette();
    }
  });

  return (
    <Box flexDirection="column" flexGrow={1}>
      <MessageList messages={messages} streamingText={streamingText} />
      <Prompt
        onSubmit={sendMessage}
        disabled={isLoading}
        model={model}
        provider={provider}
        showKeybindHint={!isDialogOpen}
      />
    </Box>
  );
}
