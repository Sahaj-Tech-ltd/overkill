import React, { useState, useCallback } from "react";
import { Box, Text, useInput } from "ink";
import type { OnboardingProviderConfig } from "../../backend/types.ts";

interface StepProviderProps {
  providers: OnboardingProviderConfig[];
  setProviders: (providers: OnboardingProviderConfig[]) => void;
  onNext: () => void;
  onBack: () => void;
}

const AVAILABLE_PROVIDERS = [
  "openai",
  "anthropic",
  "deepseek",
  "ollama",
  "google",
  "groq",
  "openrouter",
  "mistral",
  "xai",
];

export function StepProvider({
  providers,
  setProviders,
  onNext,
  onBack,
}: StepProviderProps): React.JSX.Element {
  const [selectedIdx, setSelectedIdx] = useState(0);
  const [editingProvider, setEditingProvider] = useState<string | null>(null);
  const [apiKeyInput, setApiKeyInput] = useState("");

  const toggleProvider = useCallback(
    (name: string) => {
      const existing = providers.find((p) => p.name === name);
      if (existing) {
        setProviders(providers.filter((p) => p.name !== name));
        if (editingProvider === name) {
          setEditingProvider(null);
          setApiKeyInput("");
        }
      } else {
        setProviders([...providers, { name, apiKey: "" }]);
        setEditingProvider(name);
        setApiKeyInput("");
      }
    },
    [providers, editingProvider, setProviders],
  );

  const handleKeySubmit = useCallback(() => {
    if (editingProvider) {
      setProviders(
        providers.map((p) =>
          p.name === editingProvider ? { ...p, apiKey: apiKeyInput } : p,
        ),
      );
      setEditingProvider(null);
      setApiKeyInput("");
    }
  }, [editingProvider, apiKeyInput, providers, setProviders]);

  const maskApiKey = (key: string): string => {
    if (!key) return "(no key set)";
    if (key.length <= 6) return "*".repeat(key.length);
    const visible = 4;
    return key.slice(0, visible) + "*".repeat(key.length - visible);
  };

  useInput((input, key) => {
    if (editingProvider) {
      if (key.return) {
        handleKeySubmit();
      } else if (key.escape) {
        setEditingProvider(null);
        setApiKeyInput("");
      } else if (key.delete || key.backspace) {
        setApiKeyInput((prev) => prev.slice(0, -1));
      } else if (input.length === 1) {
        setApiKeyInput((prev) => prev + input);
      }
      return;
    }

    if (key.upArrow) {
      setSelectedIdx((prev) => Math.max(0, prev - 1));
    } else if (key.downArrow) {
      setSelectedIdx((prev) =>
        Math.min(AVAILABLE_PROVIDERS.length - 1, prev + 1),
      );
    } else if (key.return || input === " ") {
      toggleProvider(AVAILABLE_PROVIDERS[selectedIdx]);
    } else if (key.rightArrow && providers.length > 0) {
      onNext();
    } else if (key.leftArrow) {
      onBack();
    }
  });

  const isSelected = (name: string): boolean =>
    providers.some((p) => p.name === name);

  return (
    <Box flexDirection="column">
      <Box flexDirection="column" marginBottom={1}>
        <Text bold>Select AI Providers</Text>
        <Text dimColor>
          Choose at least one provider and enter your API key.
        </Text>
      </Box>

      {/* Provider list */}
      <Box flexDirection="column" marginBottom={1}>
        {AVAILABLE_PROVIDERS.map((name, i) => {
          const selected = isSelected(name);
          const provider = providers.find((p) => p.name === name);
          const isEditing = editingProvider === name;
          const isHighlighted = i === selectedIdx;

          return (
            <Box key={name} flexDirection="column">
              <Box>
                <Text
                  color={isHighlighted && !editingProvider ? "cyan" : undefined}
                >
                  {isHighlighted && !editingProvider ? "▸ " : "  "}
                </Text>
                <Text
                  color={selected ? "green" : undefined}
                  bold={isHighlighted && !editingProvider}
                >
                  [{selected ? "✓" : " "}] {name}
                </Text>
                {selected && provider && (
                  <Text dimColor> → {maskApiKey(provider.apiKey)}</Text>
                )}
              </Box>

              {/* API key input */}
              {isEditing && (
                <Box marginLeft={4}>
                  <Text color="yellow">API Key: </Text>
                  <Text color="white">{apiKeyInput}</Text>
                  <Text dimColor>
                    {apiKeyInput.length > 0
                      ? "▌"
                      : "(type key, Enter to confirm, Esc to cancel)"}
                  </Text>
                </Box>
              )}
            </Box>
          );
        })}
      </Box>

      {/* Navigation */}
      <Box flexDirection="column" marginTop={1}>
        <Text dimColor>
          ↑↓ navigate · space/enter toggle · right arrow next · left arrow
          back
        </Text>
        {providers.length > 0 && (
          <Text color="green">Selected: {providers.map((p) => p.name).join(", ")}</Text>
        )}
      </Box>
    </Box>
  );
}
