import React, { useState, useEffect, useCallback } from "react";
import { Box, Text, useInput } from "ink";
import type { OnboardingProviderConfig, ModelInfo } from "../../backend/types.ts";

interface StepVisionProps {
  providers: OnboardingProviderConfig[];
  visionProvider: string;
  setVisionProvider: (model: string) => void;
  onNext: () => void;
  onBack: () => void;
}

/** Known vision-capable models per provider (fallback when API doesn't provide supports_vision). */
const FALLBACK_VISION_MODELS: Record<string, string[]> = {
  openai: ["gpt-4o", "gpt-4o-mini", "gpt-4-turbo"],
  anthropic: ["claude-sonnet-4-20250514", "claude-opus-4-20250514", "claude-3.5-sonnet"],
  google: ["gemini-2.0-flash", "gemini-1.5-pro", "gemini-2.5-pro"],
  deepseek: ["deepseek-chat"],
  ollama: ["llava", "llama3.2-vision", "bakllava", "minicpm-v"],
  groq: ["llama-3.2-11b-vision", "llama-3.2-90b-vision"],
  openrouter: ["openai/gpt-4o", "anthropic/claude-sonnet-4-20250514", "google/gemini-2.0-flash"],
  mistral: ["pixtral-large", "pixtral-12b"],
  xai: ["grok-2-vision"],
};

function getVisionModels(
  providers: OnboardingProviderConfig[],
): ModelInfo[] {
  const models: ModelInfo[] = [];
  for (const provider of providers) {
    const names = FALLBACK_VISION_MODELS[provider.name];
    if (!names) continue;
    for (const name of names) {
      models.push({
        id: `${provider.name}/${name}`,
        name: `${provider.name}/${name}`,
        supports_vision: true,
      });
    }
  }
  return models;
}

export function StepVision({
  providers,
  visionProvider,
  setVisionProvider,
  onNext,
  onBack,
}: StepVisionProps): React.JSX.Element {
  const visionModels = getVisionModels(providers);
  const hasModels = visionModels.length > 0;

  // Option 0 = "Same as default model", then visionModels offset by 1
  const totalItems = hasModels ? visionModels.length + 1 : 1;

  const [selectedIdx, setSelectedIdx] = useState(() => {
    if (visionProvider === "") return 0;
    const idx = visionModels.findIndex((m) => m.id === visionProvider);
    return idx >= 0 ? idx + 1 : 0;
  });

  // Sync selected index when visionProvider changes
  useEffect(() => {
    if (visionProvider === "") {
      setSelectedIdx(0);
    } else {
      const idx = visionModels.findIndex((m) => m.id === visionProvider);
      if (idx >= 0) setSelectedIdx(idx + 1);
    }
  }, [visionProvider, visionModels]);

  // Group models by provider for display
  const groupedModels = useCallback(() => {
    const groups: Record<string, ModelInfo[]> = {};
    for (const m of visionModels) {
      const provider = m.id.split("/")[0];
      if (!groups[provider]) groups[provider] = [];
      groups[provider].push(m);
    }
    return groups;
  }, [visionModels]);

  useInput((input, key) => {
    if (key.upArrow) {
      setSelectedIdx((prev) => Math.max(0, prev - 1));
    } else if (key.downArrow) {
      setSelectedIdx((prev) => Math.min(totalItems - 1, prev + 1));
    } else if (key.return) {
      if (selectedIdx === 0) {
        // "Same as default model"
        setVisionProvider("");
      } else if (visionModels[selectedIdx - 1]) {
        setVisionProvider(visionModels[selectedIdx - 1].id);
      }
    } else if (key.rightArrow) {
      onNext();
    } else if (key.leftArrow) {
      onBack();
    }
  });

  const groups = groupedModels();
  let globalIdx = 1; // start after "Same as default model"

  return (
    <Box flexDirection="column">
      <Box flexDirection="column" marginBottom={1}>
        <Text bold>Choose Vision Provider (Optional)</Text>
        <Text dimColor>
          Select a vision-capable model for image understanding.
          You can use your default model if it supports vision.
        </Text>
      </Box>

      {providers.length === 0 ? (
        <Box marginY={2}>
          <Text color="red">
            No providers configured. Go back and add at least one.
          </Text>
        </Box>
      ) : (
        <Box flexDirection="column" marginBottom={1}>
          {/* "Same as default model" option */}
          <Box key="__same_as_default" marginBottom={1}>
            <Text
              color={selectedIdx === 0 ? "cyan" : undefined}
            >
              {selectedIdx === 0 ? "▸ " : "  "}
            </Text>
            <Text
              color={visionProvider === "" ? "green" : undefined}
              bold={visionProvider === ""}
            >
              {visionProvider === "" ? "✓ " : "  "}
            </Text>
            <Text
              color={visionProvider === "" ? "green" : undefined}
              bold={selectedIdx === 0}
            >
              Same as default model
            </Text>
            <Text dimColor> (use default for vision too)</Text>
          </Box>

          {/* Vision-capable models grouped by provider */}
          {Object.entries(groups).map(([provider, providerModels]) => (
            <Box key={provider} flexDirection="column" marginBottom={1}>
              <Text bold color="yellow">
                {provider}
              </Text>
              {providerModels.map((m) => {
                const idx = globalIdx++;
                const isHighlighted = idx === selectedIdx;
                const isSelected = m.id === visionProvider;

                return (
                  <Box key={m.id}>
                    <Text
                      color={isHighlighted ? "cyan" : undefined}
                    >
                      {isHighlighted ? "▸ " : "  "}
                    </Text>
                    <Text
                      color={isSelected ? "green" : undefined}
                      bold={isSelected}
                    >
                      {isSelected ? "✓ " : "  "}
                    </Text>
                    <Text
                      color={isSelected ? "green" : undefined}
                    >
                      {m.name.split("/").slice(1).join("/")}
                    </Text>
                  </Box>
                );
              })}
            </Box>
          ))}

          {!hasModels && (
            <Box marginY={1}>
              <Text dimColor>
                No known vision-capable models for your providers.
                Select "Same as default model" to use your default model.
              </Text>
            </Box>
          )}
        </Box>
      )}

      {/* Current selection */}
      {visionProvider && (
        <Box marginBottom={1}>
          <Text color="green">Vision: {visionProvider}</Text>
        </Box>
      )}
      {visionProvider === "" && (
        <Box marginBottom={1}>
          <Text dimColor>
            Using default model for vision tasks.
          </Text>
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
