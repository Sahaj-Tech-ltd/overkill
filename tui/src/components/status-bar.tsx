import React, { useState, useEffect } from "react";
import { Text, Box } from "ink";
import type { ConnectionState } from "../backend/types.ts";
import type { Theme } from "../themes/definitions.ts";

interface StatusBarProps {
  connectionState: ConnectionState;
  model?: string;
  provider?: string;
  sessionName?: string;
  theme: Theme;
  gitBranch?: string;
  queuedMessages?: number;
  statusPhase?: string;
}

const STATUS_SYMBOLS: Record<
  ConnectionState,
  { symbol: string; color: string }
> = {
  connected: { symbol: "●", color: "green" },
  connecting: { symbol: "◌", color: "yellow" },
  disconnected: { symbol: "○", color: "red" },
};

const VERSION = "v0.2.0";

function formatTime(): string {
  const now = new Date();
  const h = now.getHours().toString().padStart(2, "0");
  const m = now.getMinutes().toString().padStart(2, "0");
  return `${h}:${m}`;
}

export function StatusBar({
  connectionState,
  model,
  provider,
  sessionName,
  theme,
  gitBranch,
  queuedMessages,
  statusPhase,
}: StatusBarProps): React.JSX.Element {
  const status = STATUS_SYMBOLS[connectionState];
  const [time, setTime] = useState(formatTime);

  useEffect(() => {
    const interval = setInterval(() => {
      setTime(formatTime());
    }, 30000);
    return () => clearInterval(interval);
  }, []);

  return (
    <Box
      borderStyle="round"
      borderColor={theme.border}
      backgroundColor={theme.inputBg}
      paddingX={1}
      justifyContent="space-between"
      width="100%"
    >
      <Box>
        <Text color={status.color}>{status.symbol} </Text>
        <Text color={theme.text}>{connectionState}</Text>
        {statusPhase && (
          <Text color={theme.warning}> [{statusPhase.replace(/_/g, " ")}]</Text>
        )}
        {provider && model && (
          <>
            <Text color={theme.muted}> │ </Text>
            <Text color={theme.text}>
              {provider}/{model}
            </Text>
          </>
        )}
        {sessionName && (
          <>
            <Text color={theme.muted}> │ </Text>
            <Text color={theme.text}>{sessionName}</Text>
          </>
        )}
        {queuedMessages !== undefined && queuedMessages > 0 && (
          <>
            <Text color={theme.muted}> │ </Text>
            <Text color={theme.warning}>
              [{queuedMessages} queued]
            </Text>
          </>
        )}
      </Box>
      <Box>
        {gitBranch && (
          <>
            <Text color={theme.accent}>{gitBranch}</Text>
            <Text color={theme.muted}> │ </Text>
          </>
        )}
        <Text color={theme.muted}>{time} </Text>
        <Text color={theme.accent}>{VERSION}</Text>
      </Box>
    </Box>
  );
}
