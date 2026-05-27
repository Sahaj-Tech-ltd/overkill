import React, { useState, useEffect, useCallback } from "react";
import { Box, Text, useInput } from "ink";
import type { OnboardingProviderConfig, ModelInfo } from "../../backend/types.ts";

interface StepModelProps {
  providers: OnboardingProviderConfig[];
  defaultModel: string;
  setDefaultModel: (model: string) => void;
  onNext: () => void;
  onBack: () => void;
}

const FALLBACK_MODELS: Record<string, string[]> = {
  openai: ["gpt-4o", "gpt-4o-mini", "gpt-4-turbo", "o3-mini"],
  anthropic: ["claude-sonnet-4-20250514", "claude-3.5-sonnet", "claude-3.5-haiku", "claude-opus-4-20250514"],
  deepseek: ["deepseek-chat", "deepseek-reasoner"],
  ollama: ["llama3.1", "qwen2.5", "codellama", "mistral"],
  google: ["gemini-2.0-flash", "gemini-1.5-pro", "gemini-2.5-pro"],
  groq: ["llama-3.1-70b", "mixtral-8x7b", "gemma2-9b"],
  openrouter: ["openai/gpt-4o", "anthropic/claude-sonnet-4-20250514", "google/gemini-2.0-flash"],
  mistral: ["mistral-large", "mistral-medium", "mistral-small"],
  xai: ["grok-3", "grok-2"],
};

function getAvailableModels(
  providers: OnboardingProviderConfig[],
): ModelInfo[] {
  const models: ModelInfo[] = [];
  for (const provider of providers) {
    const names = FALLBACK_MODELS[provider.name] ?? [provider.name];
    for (const name of names) {
      models.push({ id: `${provider.name}/${name}`, name: `${provider.name}/${name}` });
    }
  }
  return models;
}

export function StepModel({
  providers,
  defaultModel,
  setDefaultModel,
  onNext,
  onBack,
}: StepModelProps): React.JSX.Element {
  const models = getAvailableModels(providers);
  const [selectedIdx, setSelectedIdx] = useState(() => {
    if (!defaultModel) return 0;
    const idx = models.findIndex((m) => m.id === defaultModel);
    return idx >= 0 ? idx : 0;
  });

  // Sync selected index when defaultModel changes
  useEffect(() => {
    if (defaultModel) {
      const idx = models.findIndex((m) => m.id === defaultModel);
      if (idx >= 0) setSelectedIdx(idx);
    }
  }, [defaultModel, models]);

  // Group models by provider for display
  const groupedModels = useCallback(() => {
    const groups: Record<string, ModelInfo[]> = {};
    for (const m of models) {
      const provider = m.id.split("/")[0];
      if (!groups[provider]) groups[provider] = [];
      groups[provider].push(m);
    }
    return groups;
  }, [models]);

  useInput((input, key) => {
    if (key.upArrow) {
      setSelectedIdx((prev) => Math.max(0, prev - 1));
    } else if (key.downArrow) {
      setSelectedIdx((prev) => Math.min(models.length - 1, prev + 1));
    } else if (key.return) {
      if (models[selectedIdx]) {
        setDefaultModel(models[selectedIdx].id);
      }
    } else if (key.rightArrow && defaultModel) {
      onNext();
    } else if (key.leftArrow) {
      onBack();
    }
  });

  const groups = groupedModels();
  let globalIdx = 0;

  return (
    <Box flexDirection="column">
      <Box flexDirection="column" marginBottom={1}>
        <Text bold>Select Default Model</Text>
        <Text dimColor>
          Choose the model to use when no specific model is requested.
        </Text>
      </Box>

      {providers.length === 0 ? (
        <Box marginY={2}>
          <Text color="red">No providers configured. Go back and add at least one.</Text>
        </Box>
      ) : (
        <Box flexDirection="column" marginBottom={1}>
          {Object.entries(groups).map(([provider, providerModels]) => (
            <Box key={provider} flexDirection="column" marginBottom={1}>
              <Text bold color="yellow">
                {provider}
              </Text>
              {providerModels.map((m) => {
                const idx = globalIdx++;
                const isHighlighted = idx === selectedIdx;
                const isSelected = m.id === defaultModel;

                return (
                  <Box key={m.id}>
                    <Text
                      color={
                        isHighlighted ? "cyan" : undefined
                      }
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
        </Box>
      )}

      {/* Current selection */}
      {defaultModel && (
        <Box marginBottom={1}>
          <Text color="green">
            Default: {defaultModel}
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
