import React, { useState, useEffect } from "react";
import { Box, Text } from "ink";

interface BootAnimationProps {
  onComplete: () => void;
}

const LOGO_LINES = [
  "  ╔══╗ ╔═══╗ ╔═══╗ ╔═══╗ ╔═╗ ╔═╗ ╔═╗ ╔═╗",
  "  ║  ║ ║   ║ ║   ║ ║   ║ ║ ║ ║ ║ ║ ║ ║ ║",
  "  ╠══╬═╣   ║ ║   ║ ║   ║ ║ ║ ║ ║ ║ ║ ║ ║",
  "  ║  ║ ║   ║ ║   ║ ║   ║ ║ ║ ╚═╝ ║ ╚═╝ ╚═╝",
  "  ╚══╝ ╚═══╝ ╚═══╝ ╚═══╝ ╚═╝ ╚═══╝ ╚═══╝",
];

const SUBTITLE = "The vibe-coding agent";
const DURATION_MS = 2000;
const LOADING_WIDTH = 30;

export function BootAnimation({
  onComplete,
}: BootAnimationProps): React.JSX.Element {
  const [progress, setProgress] = useState(0);
  const [subtitleLen, setSubtitleLen] = useState(0);
  const [done, setDone] = useState(false);

  useEffect(() => {
    const startTime = Date.now();
    const interval = setInterval(() => {
      const elapsed = Date.now() - startTime;
      const ratio = Math.min(1, elapsed / DURATION_MS);

      setProgress(Math.floor(ratio * 100));
      setSubtitleLen(
        Math.min(SUBTITLE.length, Math.floor(ratio * SUBTITLE.length * 1.5)),
      );

      if (ratio >= 1) {
        clearInterval(interval);
        setDone(true);
      }
    }, 50);

    return () => clearInterval(interval);
  }, []);

  useEffect(() => {
    if (done) {
      const timeout = setTimeout(() => {
        onComplete();
      }, 200);
      return () => clearTimeout(timeout);
    }
    return undefined;
  }, [done, onComplete]);

  const filled = Math.floor((progress / 100) * LOADING_WIDTH);
  const empty = LOADING_WIDTH - filled;
  const bar = "█".repeat(filled) + "░".repeat(empty);

  return (
    <Box
      flexDirection="column"
      alignItems="center"
      justifyContent="center"
      height="100%"
    >
      <Box flexDirection="column" alignItems="center">
        {LOGO_LINES.map((line, i) => (
          <Text key={i} color="cyan" bold>
            {line}
          </Text>
        ))}

        <Box marginTop={1}>
          <Text color="magenta" italic>
            {SUBTITLE.slice(0, subtitleLen)}
            {subtitleLen < SUBTITLE.length && <Text color="white">▌</Text>}
          </Text>
        </Box>

        <Box marginTop={1}>
          <Text color="green">[</Text>
          <Text color={filled > LOADING_WIDTH / 2 ? "green" : "yellow"}>
            {bar}
          </Text>
          <Text color="green">]</Text>
          <Text color="gray"> {progress}%</Text>
        </Box>
      </Box>
    </Box>
  );
}
