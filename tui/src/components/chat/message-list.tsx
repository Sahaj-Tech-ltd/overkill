import React, { useMemo } from "react";
import { Box, useStdout } from "ink";
import type { Message } from "../../backend/types.ts";
import { MessageBubble } from "./message.tsx";

interface MessageListProps {
  messages: Message[];
  streamingText?: string;
}

export function MessageList({
  messages,
  streamingText,
}: MessageListProps): React.JSX.Element {
  const { stdout } = useStdout();
  const height = stdout.rows - 6;
  const width = stdout.columns;

  const hasStreaming = streamingText !== undefined && streamingText.length > 0;
  const totalMessages = messages.length + (hasStreaming ? 1 : 0);

  const visibleMessages = useMemo(() => {
    if (totalMessages <= height) {
      return messages;
    }
    const start = messages.length - (height - (hasStreaming ? 1 : 0));
    return messages.slice(start > 0 ? start : 0);
  }, [messages, totalMessages, height, hasStreaming]);

  return (
    <Box flexDirection="column" flexGrow={1} paddingX={1} overflow="hidden">
      {visibleMessages.map((msg, i) => (
        <Box key={i} marginBottom={1}>
          <MessageBubble
            role={msg.role}
            content={msg.content}
            terminalWidth={width}
          />
        </Box>
      ))}
      {hasStreaming && (
        <Box marginBottom={1}>
          <MessageBubble
            role="assistant"
            content={streamingText}
            streaming
            terminalWidth={width}
          />
        </Box>
      )}
    </Box>
  );
}
