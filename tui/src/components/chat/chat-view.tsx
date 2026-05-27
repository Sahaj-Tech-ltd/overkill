import React from "react";
import { Box, useInput } from "ink";
import type { BackendClient } from "../../backend/client.ts";
import { MessageList } from "./message-list.tsx";
import { Prompt } from "./prompt.tsx";
import { useChat } from "../../hooks/use-chat.ts";

interface ChatViewProps {
  backend: BackendClient;
  onOpenPalette: () => void;
  isDialogOpen: boolean;
}

export function ChatView({
  backend,
  onOpenPalette,
  isDialogOpen,
}: ChatViewProps): React.JSX.Element {
  const { messages, sendMessage, clearChat, isLoading, model, provider } =
    useChat(backend);

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
      <MessageList messages={messages} />
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
