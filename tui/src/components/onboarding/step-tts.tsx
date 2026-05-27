import React, { useState, useCallback } from "react";
import { Box, Text, useInput } from "ink";
import type { OnboardingTTSConfig } from "../../backend/types.ts";

interface StepTTSProps {
  tts: OnboardingTTSConfig | null;
  setTTS: (config: OnboardingTTSConfig | null) => void;
  onNext: () => void;
  onBack: () => void;
}

const TTS_PROVIDERS = [
  { id: "none", label: "None (skip TTS)" },
  { id: "openai-tts", label: "OpenAI TTS" },
  { id: "elevenlabs", label: "ElevenLabs" },
  { id: "edge-tts", label: "Edge TTS (free)" },
  { id: "kittentts", label: "KittenTTS (local, free)" },
  { id: "play.ht", label: "Play.ht" },
];

export function StepTTS({
  tts,
  setTTS,
  onNext,
  onBack,
}: StepTTSProps): React.JSX.Element {
  const [selectedIdx, setSelectedIdx] = useState(
    tts ? TTS_PROVIDERS.findIndex((p) => p.id === tts.provider) : 0,
  );
  const [editingKey, setEditingKey] = useState(false);
  const [apiKeyInput, setApiKeyInput] = useState(tts?.apiKey ?? "");
  const [enterConfirm, setEnterConfirm] = useState(false);

  const handleSelect = useCallback(() => {
    const selected = TTS_PROVIDERS[selectedIdx];
    if (!selected || selected.id === "none") {
      setTTS(null);
    } else if (selected.id === "edge-tts" || selected.id === "kittentts") {
      // Free providers that don't need an API key
      setTTS({ provider: selected.id });
    } else {
      // Providers that need API keys
      setEditingKey(true);
      setEnterConfirm(true);
    }
  }, [selectedIdx, setTTS]);

  const confirmKey = useCallback(() => {
    setEditingKey(false);
    setEnterConfirm(false);
    const selected = TTS_PROVIDERS[selectedIdx];
    if (selected && selected.id !== "none") {
      setTTS({ provider: selected.id, apiKey: apiKeyInput || undefined });
    }
  }, [selectedIdx, apiKeyInput, setTTS]);

  useInput((input, key) => {
    if (editingKey) {
      if (key.return) {
        confirmKey();
      } else if (key.escape) {
        setEditingKey(false);
        setEnterConfirm(false);
        setApiKeyInput("");
      } else if (key.delete || key.backspace) {
        setApiKeyInput((prev) => prev.slice(0, -1));
      } else if (input.length === 1) {
        setApiKeyInput((prev) => prev + input);
      }
      return;
    }

    if (enterConfirm) {
      if (key.return) {
        handleSelect();
      } else {
        setEnterConfirm(false);
      }
      return;
    }

    if (key.upArrow) {
      setSelectedIdx((prev) => Math.max(0, prev - 1));
    } else if (key.downArrow) {
      setSelectedIdx((prev) =>
        Math.min(TTS_PROVIDERS.length - 1, prev + 1),
      );
    } else if (key.return) {
      handleSelect();
    } else if (key.rightArrow) {
      onNext();
    } else if (key.leftArrow) {
      onBack();
    }
  });

  return (
    <Box flexDirection="column">
      <Box flexDirection="column" marginBottom={1}>
        <Text bold>Text-to-Speech (Optional)</Text>
        <Text dimColor>
          Configure text-to-speech for voice output. You can skip this step.
        </Text>
      </Box>

      {/* TTS provider list */}
      <Box flexDirection="column" marginBottom={1}>
        {TTS_PROVIDERS.map((provider, i) => {
          const isHighlighted = i === selectedIdx;
          const isSelected =
            provider.id === "none"
              ? !tts
              : tts?.provider === provider.id;

          return (
            <Box key={provider.id}>
              <Text
                color={isHighlighted && !editingKey ? "cyan" : undefined}
              >
                {isHighlighted && !editingKey ? "▸ " : "  "}
              </Text>
              <Text
                color={isSelected ? "green" : undefined}
                bold={isSelected}
              >
                {isSelected ? "✓ " : "  "}
              </Text>
              <Text
                color={isSelected ? "green" : undefined}
                bold={isHighlighted && !editingKey}
              >
                {provider.label}
              </Text>
            </Box>
          );
        })}
      </Box>

      {/* API key input for providers that need it */}
      {editingKey && (
        <Box marginY={1} flexDirection="column">
          <Text color="yellow">
            Enter API key for {TTS_PROVIDERS[selectedIdx]?.label}:
          </Text>
          <Box>
            <Text color="white">{apiKeyInput}</Text>
            <Text dimColor>▌</Text>
          </Box>
          <Text dimColor>Press Enter to confirm, Esc to cancel</Text>
        </Box>
      )}

      {/* Current selection */}
      {tts && (
        <Box marginBottom={1}>
          <Text color="green">
            TTS: {tts.provider}
            {tts.apiKey ? " (key set)" : ""}
          </Text>
        </Box>
      )}

      {!tts && (
        <Box marginBottom={1}>
          <Text dimColor>No TTS configured (you can change this later)</Text>
        </Box>
      )}

      {/* Navigation */}
      <Box flexDirection="column" marginTop={1}>
        <Text dimColor>
          ↑↓ navigate · enter select · right arrow next · left arrow back
        </Text>
      </Box>
    </Box>
  );
}
