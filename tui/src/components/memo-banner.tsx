import React, { useState, useEffect, useMemo } from "react";
import { Text, Box } from "ink";
import { matchMemoPhrase, getActionPhrase } from "./memo-phrases.ts";
import type { Theme } from "../themes/definitions.ts";

interface MemoBannerProps {
  /** Whether the agent is currently processing */
  isLoading: boolean;
  /** The user's last message (for context-aware phrases) */
  userMessage?: string;
  /** Current agent status phase from backend */
  statusPhase?: string;
  /** Seconds elapsed since thinking started */
  elapsedSeconds: number;
  /** Theme colors */
  theme: Theme;
  /** Whether this is the banner mode (top of screen) or inline mode */
  mode?: "banner" | "inline";
}

/**
 * ASCII elephant frames for subtle animation.
 * Memo = two Postgres elephants. Strong, never-forgetting, frontier-fearing.
 */
const ELEPHANT_FRAMES = [
  // Frame 0 — neutral
  [
    "     ┌──────────────┐     ",
    "    /  ~~~~~~~~~~~  \\    ",
    "   /  ┌──────────┐  \\   ",
    "  │   │  o    o   │   │  ",
    "  │   │     v     │   │  ",
    "  │   └──┬────┬──┘   │  ",
    "  │      │    │      │  ",
    "  │   ┌──┘    └──┐   │  ",
    "  └───┘          └───┘  ",
  ],
  // Frame 1 — trunk raised (happy)
  [
    "     ┌──────────────┐     ",
    "    /  ~~~~~~~~~~~  \\    ",
    "   /  ┌──────────┐  \\   ",
    "  │   │  o    o   │   │  ",
    "  │   │    ^      │   │  ",
    "  │   └──┬────┬──┘   │  ",
    "  │      │   ┌┘     │  ",
    "  │   ┌──┘   └───┐  │  ",
    "  └───┘          └──┘  ",
  ],
];

const BOX_DRAWING_FRAME = [
  // Frame 0 — neutral (single-line, universally supported)
  [
    "    ┌──────────────┐    ",
    "   /  ~~~~~~~~~~~  \\   ",
    "  /  ┌──────────┐  \\  ",
    " │   │  o    o   │   │ ",
    " │   │     v     │   │ ",
    " │   └──┬────┬──┘   │ ",
    " │      │    │      │ ",
    " │   ┌──┘    └──┐   │ ",
    " └───┘          └───┘ ",
  ],
];

/**
 * Compact elephant — fits in narrow terminals.
 */
const COMPACT_ELEPHANT = [
  "  ┌────────┐  ",
  " /  o   o  \\ ",
  "│     v     │ ",
  "│  ┌────┐   │ ",
  " └──┘  └───┘  ",
  "   /│  │\\    ",
  "  / └──┘ \\   ",
];

function elephantArt(
  frame: number,
  color: string,
  compact: boolean,
): string[] {
  const frames = compact
    ? [COMPACT_ELEPHANT]
    : ELEPHANT_FRAMES;
  return frames[frame % frames.length];
}

/** "Trunk dots" — animated loading dots that look like elephant trunk spray */
const TRUNK_DOTS = ["", ".", "..", "...", " ╮", " ╮.", " ╮.."];

export function MemoBanner({
  isLoading,
  userMessage,
  statusPhase,
  elapsedSeconds,
  theme,
  mode = "banner",
}: MemoBannerProps): React.JSX.Element | null {
  const [frame, setFrame] = useState(0);
  const [dotIdx, setDotIdx] = useState(0);

  // Animate elephant frames
  useEffect(() => {
    if (!isLoading) return;
    const interval = setInterval(() => {
      setFrame((f) => (f + 1) % ELEPHANT_FRAMES.length);
    }, 1200);
    return () => clearInterval(interval);
  }, [isLoading]);

  // Animate trunk dots
  useEffect(() => {
    if (!isLoading) return;
    const interval = setInterval(() => {
      setDotIdx((d) => (d + 1) % TRUNK_DOTS.length);
    }, 250);
    return () => clearInterval(interval);
  }, [isLoading]);

  // Determine phrase based on context
  const phraseInfo = useMemo(() => {
    if (statusPhase && statusPhase.startsWith("tool:")) {
      const tool = statusPhase.slice(5);
      return { phrase: getActionPhrase(tool), category: "tool" };
    }
    if (userMessage) {
      return matchMemoPhrase(userMessage);
    }
    return { phrase: "Remembering everything...", category: "default" };
  }, [userMessage, statusPhase]);

  if (!isLoading && mode === "inline") return null;

  const isCompact = process.stdout.columns
    ? process.stdout.columns < 80
    : false;
  const lines = elephantArt(frame, theme.accent, isCompact);
  const timeStr =
    elapsedSeconds < 60
      ? `${elapsedSeconds}s`
      : `${Math.floor(elapsedSeconds / 60)}m ${elapsedSeconds % 60}s`;

  const titleColor = phraseInfo.category === "contextual" ? theme.accent : theme.warning;
  const bracketColor = theme.muted;

  if (mode === "inline") {
    // Inline mode: minimal — just phrase + dots + timer
    return (
      <Box flexDirection="row" marginY={0}>
        <Text color={titleColor}>🐘 </Text>
        <Text color={titleColor}>{phraseInfo.phrase}</Text>
        <Text color={theme.accent}>{TRUNK_DOTS[dotIdx]}</Text>
        <Text color={bracketColor}> ({timeStr})</Text>
      </Box>
    );
  }

  // Banner mode: full elephant art + phrase + user message
  return (
    <Box flexDirection="column" marginBottom={1}>
      {/* Elephant art */}
      <Box flexDirection="column">
        {lines.map((line, i) => (
          <Text key={i} color={theme.accent}>
            {line}
          </Text>
        ))}
      </Box>

      {/* Status phrase row */}
      <Box flexDirection="row" marginTop={0}>
        <Text color={titleColor} bold>
          {phraseInfo.phrase}
        </Text>
        <Text color={theme.accent}>{TRUNK_DOTS[dotIdx]}</Text>
      </Box>

      {/* User message echo + timer */}
      <Box flexDirection="row">
        {userMessage && (
          <>
            <Text color={bracketColor}>Processing: </Text>
            <Text color={theme.text} italic>
              {userMessage.length > 60
                ? userMessage.slice(0, 57) + "..."
                : userMessage}
            </Text>
            <Text color={bracketColor}> │ </Text>
          </>
        )}
        <Text color={bracketColor}>{timeStr} · esc to interrupt</Text>
      </Box>
    </Box>
  );
}
