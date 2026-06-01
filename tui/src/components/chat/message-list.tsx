import React, { useMemo, useEffect, useRef } from "react";
import { Box, Text, useStdout, useInput } from "ink";
import type { Message, FileChange } from "../../backend/types.ts";
import type { Theme } from "../../themes/definitions.ts";
import { MessageBubble } from "./message.tsx";
import { useScrollHandler, useClickZone } from "../../hooks/use-mouse.tsx";

interface MessageListProps {
  messages: Message[];
  streamingText?: string;
  /** Virtual scroll offset (0 = bottom / latest) */
  scrollOffset: number;
  /** Called when user scrolls up/down */
  onScrollChange: (offset: number) => void;
  /** Files changed during active tool execution */
  fileChanges: FileChange[];
  /** Whether agent is currently loading (controls file bar visibility) */
  isLoading: boolean;
  /** Theme for styling */
  theme: Theme;
}

/** How many messages to keep rendered above the visible area as buffer */
const SCROLL_BUFFER = 5;

export function MessageList({
  messages,
  streamingText,
  scrollOffset,
  onScrollChange,
  fileChanges,
  isLoading,
  theme,
}: MessageListProps): React.JSX.Element {
  const { stdout } = useStdout();
  // Reserve space for: prompt (~3), memo banner (~6), status bar (~1), file bar header (~1)
  const terminalHeight = stdout.rows - 12;
  const terminalWidth = stdout.columns;

  // Keyboard scrolling
  useInput((input, key) => {
    if (key.pageUp) {
      onScrollChange(Math.min(messages.length - 1, scrollOffset + 5));
    }
    if (key.pageDown) {
      onScrollChange(Math.max(0, scrollOffset - 5));
    }
    if (key.upArrow && !key.ctrl && !key.meta) {
      onScrollChange(Math.min(messages.length - 1, scrollOffset + 1));
    }
    if (key.downArrow && !key.ctrl && !key.meta) {
      onScrollChange(Math.max(0, scrollOffset - 1));
    }
    // Home/End for quick navigation
    if (key.home) {
      // Scroll to top (show oldest messages)
      onScrollChange(Math.max(0, messages.length - terminalHeight));
    }
    if (key.end) {
      // Scroll to bottom (latest)
      onScrollChange(0);
    }
  });

  // Mouse wheel scrolling
  useScrollHandler((direction) => {
    if (direction === "up") {
      onScrollChange(Math.min(messages.length - 1, scrollOffset + 3));
    } else {
      onScrollChange(Math.max(0, scrollOffset - 3));
    }
  });

  const hasStreaming = streamingText !== undefined && streamingText.length > 0;
  const totalMessages = messages.length + (hasStreaming ? 1 : 0);

  // Determine visible message range
  const visibleMessages = useMemo(() => {
    // Calculate how many messages fit in the terminal
    const maxVisible = Math.max(1, terminalHeight);

    if (totalMessages <= maxVisible) {
      // All messages fit — return all
      return messages;
    }

    // Virtual window: from (end - maxVisible - scrollOffset) to (end - scrollOffset)
    const endIdx = messages.length - scrollOffset;
    const startIdx = Math.max(0, endIdx - maxVisible - SCROLL_BUFFER);

    return messages.slice(startIdx, endIdx);
  }, [messages, totalMessages, terminalHeight, scrollOffset]);

  // How many messages are hidden above the visible window?
  const hiddenAbove = Math.max(
    0,
    messages.length - scrollOffset - visibleMessages.length,
  );
  // Adjust for the case where scrollOffset causes overlap
  const actuallyHiddenAbove = Math.max(
    0,
    messages.length - (scrollOffset + visibleMessages.length),
  );

  // Show file change bar when loading and there are file changes
  const showFileBar = isLoading && fileChanges.length > 0;

  // Sort file changes by recency (most recent first)
  const sortedChanges = useMemo(
    () => [...fileChanges].sort((a, b) => b.timestamp - a.timestamp),
    [fileChanges],
  );

  return (
    <Box flexDirection="row" flexGrow={1} overflow="hidden">
      {/* Message area */}
      <Box flexDirection="column" flexGrow={1} paddingX={1} overflow="hidden">
        {/* Scroll indicator: earlier messages hidden */}
        {actuallyHiddenAbove > 0 && (
          <Box>
            <Text dimColor color={theme.muted}>
              ↑ {actuallyHiddenAbove} earlier messages
              {scrollOffset > 0 ? ` (scrolled ${scrollOffset})` : ""}
            </Text>
          </Box>
        )}

        {/* Visible messages */}
        {visibleMessages.map((msg, i) => (
          <Box key={msg.id ?? `msg-${i}`} marginBottom={1}>
            <MessageBubble
              role={msg.role}
              content={msg.content}
              terminalWidth={showFileBar ? terminalWidth - 26 : terminalWidth}
              reasoning={msg.reasoning}
              reasoningDuration={msg.reasoningDuration}
              turnDuration={msg.turnDuration}
            />
          </Box>
        ))}

        {/* Streaming message */}
        {hasStreaming && (
          <Box marginBottom={1}>
            <MessageBubble
              role="assistant"
              content={streamingText}
              streaming
              terminalWidth={showFileBar ? terminalWidth - 26 : terminalWidth}
            />
          </Box>
        )}

        {/* Scroll indicator: at bottom */}
        {scrollOffset > 0 && messages.length > terminalHeight && (
          <Box>
            <Text dimColor color={theme.muted}>
              ↓ {scrollOffset} more recent messages (End to jump)
            </Text>
          </Box>
        )}
      </Box>

      {/* File change scroll bar — right column */}
      {showFileBar && (
        <Box
          flexDirection="column"
          width={26}
          borderStyle="single"
          borderColor={theme.border}
          borderLeft
          paddingX={1}
          overflow="hidden"
        >
          {/* Header with count badge */}
          <Box marginBottom={1}>
            <Text bold color={theme.accent}>
              📁 {fileChanges.length} file{fileChanges.length !== 1 ? "s" : ""}
            </Text>
          </Box>

          {/* File entries */}
          {sortedChanges.map((fc, i) => {
            // Truncate path to fit
            const displayPath =
              fc.path.length > 20
                ? "..." + fc.path.slice(fc.path.length - 17)
                : fc.path;

            return (
              <Box key={`fc-${i}`} flexDirection="column" marginBottom={1}>
                <Text color={theme.text}>{displayPath}</Text>
                <Box>
                  <Text color={theme.success}>+{fc.added}</Text>
                  <Text color={theme.text}>/</Text>
                  <Text color={theme.error}>-{fc.removed}</Text>
                </Box>
              </Box>
            );
          })}
        </Box>
      )}
    </Box>
  );
}
