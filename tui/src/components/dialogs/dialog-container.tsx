import React from "react";
import { Box, Text, useInput, useStdout } from "ink";
import { useTheme } from "../../hooks/use-theme.ts";

interface DialogContainerProps {
  open: boolean;
  onClose: () => void;
  title: string;
  children: React.ReactNode;
}

const MIN_WIDTH = 40;
const HORIZONTAL_MARGIN = 4;

/** Input handler that only mounts when the dialog is open. */
function DialogInputHandler({ onClose }: { onClose: () => void }) {
  const { theme } = useTheme();
  useInput((_input, key) => {
    if (key.escape) {
      onClose();
    }
  });
  return null;
}

export function DialogContainer({
  open,
  onClose,
  title,
  children,
}: DialogContainerProps): React.JSX.Element | null {
  const { theme } = useTheme();
  const { stdout } = useStdout();
  const termWidth = stdout.columns ?? 80;
  const termHeight = stdout.rows ?? 24;

  if (!open) return null;

  const dialogWidth = Math.min(
    termWidth - HORIZONTAL_MARGIN,
    Math.max(MIN_WIDTH, Math.floor(termWidth * 0.7)),
  );

  const innerWidth = dialogWidth - 2; // borders
  const titleLen = title.length + 2; // padding
  const titlePad = Math.max(0, innerWidth - titleLen);

  const topBorder = `┌─ ${title} ${"─".repeat(titlePad)}┐`;
  const midBorder = `├${"─".repeat(innerWidth)}┤`;
  const botBorder = `└${"─".repeat(innerWidth)}┘`;

  // Position: overlay centered vertically, skip a few rows from top
  const dialogHeight = Math.min(termHeight - 4, 16);
  const topPad = Math.max(0, Math.floor((termHeight - dialogHeight) / 2) - 1);

  return (
    <Box
      flexDirection="column"
      position="absolute"
      width={termWidth}
      height={termHeight}
    >
      <DialogInputHandler onClose={onClose} />
      {/* Backdrop rows above */}
      {Array.from({ length: topPad }).map((_, i) => (
        <Box key={`top-${i}`} width="100%">
          <Text dimColor>{" ".repeat(termWidth)}</Text>
        </Box>
      ))}

      {/* Top border */}
      <Box width="100%">
        <Text backgroundColor={theme.muted} color={theme.text} bold>
          {topBorder}
        </Text>
      </Box>

      {/* Content area */}
      <Box
        flexDirection="column"
        backgroundColor={theme.muted}
        width="100%"
        paddingX={0}
      >
        <Box>
          <Text backgroundColor={theme.muted} color={theme.text}>
            {"│"}
          </Text>
          <Box flexDirection="column" width={innerWidth}>
            {children}
          </Box>
          <Text backgroundColor={theme.muted} color={theme.text}>
            {"│"}
          </Text>
        </Box>
      </Box>

      {/* Bottom border */}
      <Box width="100%">
        <Text backgroundColor={theme.muted} color={theme.text} bold>
          {botBorder}
        </Text>
      </Box>

      {/* Backdrop rows below (fill rest) */}
      <Box flexGrow={1} width="100%">
        <Text dimColor>{" ".repeat(termWidth)}</Text>
      </Box>
    </Box>
  );
}
