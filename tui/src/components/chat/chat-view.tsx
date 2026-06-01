import React from "react";
import { Box, useInput } from "ink";
import type { Message, FileChange } from "../../backend/types.ts";
import type { Theme } from "../../themes/definitions.ts";
import { MessageList } from "./message-list.tsx";
import { Prompt } from "./prompt.tsx";
import { MemoBanner } from "../memo-banner.tsx";

interface ChatViewProps {
  messages: Message[];
  sendMessage: (text: string) => Promise<void>;
  onCancel: () => void;
  clearChat: () => void;
  isLoading: boolean;
  streamingText?: string;
  model?: string;
  provider?: string;
  onOpenPalette: () => void;
  isDialogOpen: boolean;
  /** Memo banner props */
  userMessage?: string;
  statusPhase?: string;
  thinkingElapsed: number;
  theme: Theme;
  /** Virtual scroll */
  scrollOffset: number;
  onScrollChange: (offset: number) => void;
  /** File changes for the scroll bar */
  fileChanges: FileChange[];
}

export function ChatView({
  messages,
  sendMessage,
  onCancel,
  clearChat,
  isLoading,
  streamingText,
  model,
  provider,
  onOpenPalette,
  isDialogOpen,
  userMessage,
  statusPhase,
  thinkingElapsed,
  theme,
  scrollOffset,
  onScrollChange,
  fileChanges,
}: ChatViewProps): React.JSX.Element {
  useInput((input, key) => {
    if (isDialogOpen) return;

    if (key.ctrl && input === "c" && isLoading) {
      onCancel();
      return;
    }
    if (key.ctrl && input === "l") {
      clearChat();
    }
    if (key.ctrl && input === "k") {
      onOpenPalette();
    }
  });

  return (
    <Box flexDirection="column" flexGrow={1}>
      {/* Memo the Elephant — thinking indicator banner */}
      <MemoBanner
        isLoading={isLoading}
        userMessage={userMessage}
        statusPhase={statusPhase}
        elapsedSeconds={thinkingElapsed}
        theme={theme}
        mode="banner"
      />
      <MessageList
        messages={messages}
        streamingText={streamingText}
        scrollOffset={scrollOffset}
        onScrollChange={onScrollChange}
        fileChanges={fileChanges}
        isLoading={isLoading}
        theme={theme}
      />
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
