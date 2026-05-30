import React, { useState, useEffect } from "react";
import { Box, Text } from "ink";

interface StepWelcomeProps {
  onNext: () => void;
}

const LOGO_LINES = [
  "  ┌──┐ ┌───┐ ┌───┐ ┌───┐ ┌─┐ ┌─┐ ┌─┐ ┌─┐",
  "  │  │ │   │ │   │ │   │ │ │ │ │ │ │ │ │ │",
  "  ├──┼─┤   │ │   │ │   │ │ │ │ │ │ │ │ │ │",
  "  │  │ │   │ │   │ │   │ │ │ └─┘ │ └─┘ └─┘",
  "  └──┘ └───┘ └───┘ └───┘ └─┘ └───┘ └───┘",
];

const SUBTITLE = "The vibe-coding agent";
const WELCOME_TEXT = [
  "Overkill combines AI with terminal tools, gateways (Discord,",
  "Telegram, WhatsApp), and automation to supercharge your workflow.",
  "",
  "Let's set up your environment in 5 quick steps.",
];

export function StepWelcome({ onNext }: StepWelcomeProps): React.JSX.Element {
  const [showContent, setShowContent] = useState(false);
  const [subtitleLen, setSubtitleLen] = useState(0);
  const [lineAnim, setLineAnim] = useState(0);
  const [showPrompt, setShowPrompt] = useState(false);

  useEffect(() => {
    // Subtle typewriter for subtitle
    const t1 = setInterval(() => {
      setSubtitleLen((prev) => {
        if (prev >= SUBTITLE.length) {
          clearInterval(t1);
          return prev;
        }
        return prev + 1;
      });
    }, 60);

    // Fade in logo lines
    const t2 = setInterval(() => {
      setLineAnim((prev) => {
        if (prev >= LOGO_LINES.length) {
          clearInterval(t2);
          return prev;
        }
        return prev + 1;
      });
    }, 80);

    // Show welcome text
    const t3 = setTimeout(() => {
      setShowContent(true);
    }, 800);

    // Show prompt
    const t4 = setTimeout(() => {
      setShowPrompt(true);
    }, 1200);

    return () => {
      clearInterval(t1);
      clearInterval(t2);
      clearTimeout(t3);
      clearTimeout(t4);
    };
  }, []);

  useEffect(() => {
    let frame = 0;
    const interval = setInterval(() => {
      frame++;
      // Eventually auto-advance if user is idle
      if (frame > 80 && showPrompt) {
        clearInterval(interval);
        onNext();
      }
    }, 200);

    return () => clearInterval(interval);
  }, [showPrompt, onNext]);

  return (
    <Box flexDirection="column">
      {/* Logo */}
      <Box flexDirection="column" marginBottom={1}>
        {LOGO_LINES.slice(0, lineAnim).map((line, i) => (
          <Text key={i} color="cyan" bold>
            {line}
          </Text>
        ))}
      </Box>

      {/* Subtitle typewriter */}
      {subtitleLen > 0 && (
        <Box marginBottom={1}>
          <Text color="magenta" italic>
            {SUBTITLE.slice(0, subtitleLen)}
            {subtitleLen < SUBTITLE.length && (
              <Text color="white">▌</Text>
            )}
          </Text>
        </Box>
      )}

      {/* Welcome text */}
      {showContent && (
        <Box flexDirection="column" marginBottom={1}>
          {WELCOME_TEXT.map((line, i) => (
            <Text key={i} dimColor={line === ""}>
              {line === "" ? " " : line}
            </Text>
          ))}
        </Box>
      )}

      {/* Prompt */}
      {showPrompt && (
        <Box marginTop={1}>
          <Text color="green">▶ Press any key to begin...</Text>
        </Box>
      )}
    </Box>
  );
}
