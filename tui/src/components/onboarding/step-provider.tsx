import React, { useState, useEffect, useCallback } from "react";
import { Box, Text, useInput } from "ink";
import type { BackendClient } from "../../backend/client.ts";
import type { OnboardingProviderConfig, ProviderInfo } from "../../backend/types.ts";

interface StepProviderProps {
  backend: BackendClient;
  providers: OnboardingProviderConfig[];
  setProviders: (providers: OnboardingProviderConfig[]) => void;
  onNext: () => void;
  onBack: () => void;
}

type CustomField = "name" | "baseUrl" | "apiKey";

/** Detect provider from API key prefix. Returns provider name or null. */
function detectProvider(key: string): string | null {
  if (key.startsWith("sk-ant")) return "anthropic";
  if (key.startsWith("sk-deepseek")) return "deepseek";
  if (key.startsWith("sk-")) return "openai";
  if (key.startsWith("gsk_")) return "groq";
  if (key.startsWith("xai-")) return "xai";
  return null;
}

export function StepProvider({
  backend,
  providers,
  setProviders,
  onNext,
  onBack,
}: StepProviderProps): React.JSX.Element {
  const [availableProviders, setAvailableProviders] = useState<string[]>([]);
  const [loading, setLoading] = useState(true);
  const [fetchError, setFetchError] = useState<string | null>(null);
  const [selectedIdx, setSelectedIdx] = useState(0);
  const [editingProvider, setEditingProvider] = useState<string | null>(null);
  const [apiKeyInput, setApiKeyInput] = useState("");
  const [detectedProvider, setDetectedProvider] = useState<string | null>(null);

  // Custom provider multi-field state
  const [customField, setCustomField] = useState<CustomField>("name");
  const [customName, setCustomName] = useState("");
  const [customBaseUrl, setCustomBaseUrl] = useState("");
  const [customApiKey, setCustomApiKey] = useState("");

  // Fetch available providers from the API on mount
  useEffect(() => {
    let cancelled = false;

    async function fetchProviders() {
      setLoading(true);
      setFetchError(null);
      try {
        const result = await backend.call<ProviderInfo[]>("providers.list");
        if (cancelled) return;
        const names = result.map((p) => p.name);
        setAvailableProviders(names);
      } catch (err) {
        if (cancelled) return;
        setFetchError((err as Error).message);
        setAvailableProviders([]);
      } finally {
        if (!cancelled) setLoading(false);
      }
    }

    fetchProviders();

    return () => {
      cancelled = true;
    };
  }, [backend]);

  // Full list with "custom" appended
  const displayList = [...availableProviders, "custom"];

  const detectFromInput = useCallback(
    (input: string) => {
      const detected = detectProvider(input);
      setDetectedProvider(detected);
      // If detected matches a different provider than the one being edited,
      // auto-highlight that provider
      if (detected && editingProvider !== detected) {
        const idx = displayList.indexOf(detected);
        if (idx >= 0) {
          setSelectedIdx(idx);
        }
      }
    },
    [editingProvider, displayList],
  );

  const toggleProvider = useCallback(
    (name: string) => {
      const existing = providers.find((p) => p.name === name);
      if (existing) {
        setProviders(providers.filter((p) => p.name !== name));
        if (editingProvider === name) {
          setEditingProvider(null);
          setApiKeyInput("");
          setDetectedProvider(null);
          resetCustomFields();
        }
      } else if (name === "custom") {
        // Custom provider: start multi-field flow
        setEditingProvider("custom");
        setCustomField("name");
        setCustomName("");
        setCustomBaseUrl("");
        setCustomApiKey("");
        setDetectedProvider(null);
      } else {
        setProviders([...providers, { name, apiKey: "" }]);
        setEditingProvider(name);
        setApiKeyInput("");
        setDetectedProvider(null);
      }
    },
    [providers, editingProvider, setProviders],
  );

  const resetCustomFields = useCallback(() => {
    setCustomField("name");
    setCustomName("");
    setCustomBaseUrl("");
    setCustomApiKey("");
  }, []);

  const handleKeySubmit = useCallback(() => {
    if (!editingProvider) return;
    if (editingProvider === "custom") {
      // Complete custom provider
      if (customName.trim() && customApiKey.trim()) {
        setProviders([
          ...providers,
          {
            name: customName.trim(),
            apiKey: customApiKey.trim(),
            baseUrl: customBaseUrl.trim() || undefined,
          },
        ]);
      }
      setEditingProvider(null);
      setApiKeyInput("");
      setDetectedProvider(null);
      resetCustomFields();
    } else {
      setProviders(
        providers.map((p) =>
          p.name === editingProvider ? { ...p, apiKey: apiKeyInput } : p,
        ),
      );
      setEditingProvider(null);
      setApiKeyInput("");
      setDetectedProvider(null);
    }
  }, [
    editingProvider,
    apiKeyInput,
    providers,
    setProviders,
    customName,
    customBaseUrl,
    customApiKey,
    resetCustomFields,
  ]);

  const handleCustomSubmit = useCallback(() => {
    if (customField === "name") {
      if (customName.trim()) {
        setCustomField("baseUrl");
      }
    } else if (customField === "baseUrl") {
      setCustomField("apiKey");
    } else {
      // apiKey field — finish
      handleKeySubmit();
    }
  }, [customField, customName, handleKeySubmit]);

  const getCustomFieldValue = (): string => {
    if (customField === "name") return customName;
    if (customField === "baseUrl") return customBaseUrl;
    return customApiKey;
  };

  const setCustomFieldValue = (value: string) => {
    if (customField === "name") setCustomName(value);
    else if (customField === "baseUrl") setCustomBaseUrl(value);
    else {
      setCustomApiKey(value);
      detectFromInput(value);
    }
  };

  const maskApiKey = (key: string): string => {
    if (!key) return "(no key set)";
    if (key.length <= 6) return "*".repeat(key.length);
    const visible = 4;
    return key.slice(0, visible) + "*".repeat(key.length - visible);
  };

  useInput((input, key) => {
    // Custom provider multi-field editing
    if (editingProvider === "custom") {
      if (key.return) {
        handleCustomSubmit();
      } else if (key.escape) {
        setEditingProvider(null);
        resetCustomFields();
        setDetectedProvider(null);
      } else if (key.delete || key.backspace) {
        setCustomFieldValue(getCustomFieldValue().slice(0, -1));
      } else if (input.length >= 1) {
        setCustomFieldValue(getCustomFieldValue() + input);
      }
      return;
    }

    if (editingProvider) {
      if (key.return) {
        handleKeySubmit();
      } else if (key.escape) {
        setEditingProvider(null);
        setApiKeyInput("");
        setDetectedProvider(null);
      } else if (key.delete || key.backspace) {
        setApiKeyInput((prev) => {
          const next = prev.slice(0, -1);
          detectFromInput(next);
          return next;
        });
      } else if (input.length >= 1) {
        setApiKeyInput((prev) => {
          const next = prev + input;
          detectFromInput(next);
          return next;
        });
      }
      return;
    }

    if (loading) return;

    if (key.upArrow) {
      setSelectedIdx((prev) => Math.max(0, prev - 1));
    } else if (key.downArrow) {
      setSelectedIdx((prev) =>
        Math.min(displayList.length - 1, prev + 1),
      );
    } else if (key.return || input === " ") {
      toggleProvider(displayList[selectedIdx]);
    } else if (key.rightArrow && providers.length > 0) {
      onNext();
    } else if (key.leftArrow) {
      onBack();
    }
  });

  const isSelected = (name: string): boolean =>
    providers.some((p) => p.name === name);

  const getProviderDisplayName = (name: string): string => {
    if (name === "custom") return "Custom (OpenAI-compatible)";
    return name;
  };

  // Loading state
  if (loading) {
    return (
      <Box flexDirection="column">
        <Box flexDirection="column" marginBottom={1}>
          <Text bold>Select AI Providers</Text>
          <Text dimColor>
            Choose at least one provider and enter your API key.
          </Text>
        </Box>
        <Box marginY={2}>
          <Text color="yellow">Fetching available providers...</Text>
        </Box>
        <Box flexDirection="column" marginTop={1}>
          <Text dimColor>Loading from config and models.dev catalog...</Text>
        </Box>
      </Box>
    );
  }

  return (
    <Box flexDirection="column">
      <Box flexDirection="column" marginBottom={1}>
        <Text bold>Select AI Providers</Text>
        <Text dimColor>
          Choose at least one provider and enter your API key.
        </Text>
      </Box>

      {/* Error banner */}
      {fetchError && (
        <Box marginBottom={1}>
          <Text color="red">Failed to fetch providers: {fetchError}</Text>
        </Box>
      )}
      {!fetchError && availableProviders.length === 0 && (
        <Box marginBottom={1}>
          <Text color="yellow">
            No providers discovered. Add a custom provider below or check
            your config.
          </Text>
        </Box>
      )}

      {/* Provider list */}
      <Box flexDirection="column" marginBottom={1}>
        {displayList.map((name, i) => {
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
                  [{selected ? "✓" : " "}] {getProviderDisplayName(name)}
                </Text>
                {selected && provider && (
                  <Text dimColor> → {maskApiKey(provider.apiKey)}</Text>
                )}
              </Box>

              {/* Standard API key input */}
              {isEditing && name !== "custom" && (
                <Box marginLeft={4} flexDirection="column">
                  <Box>
                    <Text color="yellow">API Key: </Text>
                    <Text color="white">{apiKeyInput}</Text>
                    <Text dimColor>
                      {apiKeyInput.length > 0
                        ? "▌"
                        : "(type key, Enter to confirm, Esc to cancel)"}
                    </Text>
                  </Box>
                  {detectedProvider && (
                    <Box marginLeft={9}>
                      <Text color="cyan" bold>
                        Detected: {detectedProvider}
                      </Text>
                      {detectedProvider !== editingProvider && (
                        <Text dimColor>
                          {" "}
                          (auto-switch to {detectedProvider})
                        </Text>
                      )}
                    </Box>
                  )}
                </Box>
              )}

              {/* Custom provider multi-field input */}
              {isEditing && name === "custom" && (
                <Box marginLeft={4} flexDirection="column">
                  <Box>
                    <Text color="yellow">
                      Provider Name{customField === "name" ? ": " : ": "}
                    </Text>
                    <Text
                      color={customField === "name" ? "white" : "gray"}
                      dimColor={customField !== "name"}
                    >
                      {customName || "(enter name)"}
                    </Text>
                    {customField === "name" && (
                      <Text dimColor> ▌</Text>
                    )}
                  </Box>
                  {customField !== "name" && (
                    <Box>
                      <Text color="yellow">
                        Base URL{customField === "baseUrl" ? ": " : ": "}
                      </Text>
                      <Text
                        color={
                          customField === "baseUrl" ? "white" : "gray"
                        }
                        dimColor={customField !== "baseUrl"}
                      >
                        {customBaseUrl || "(e.g. http://localhost:1234/v1)"}
                      </Text>
                      {customField === "baseUrl" && (
                        <Text dimColor> ▌</Text>
                      )}
                    </Box>
                  )}
                  {customField === "apiKey" && (
                    <Box flexDirection="column">
                      <Box>
                        <Text color="yellow">API Key: </Text>
                        <Text color="white">{customApiKey}</Text>
                        <Text dimColor> ▌</Text>
                      </Box>
                      {detectedProvider && (
                        <Box marginLeft={9}>
                          <Text color="cyan" bold>
                            Detected: {detectedProvider}
                          </Text>
                        </Box>
                      )}
                    </Box>
                  )}
                  <Text dimColor>
                    Enter to confirm field · Esc to cancel
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
          <Text color="green">
            Selected: {providers.map((p) => p.name).join(", ")}
          </Text>
        )}
      </Box>
    </Box>
  );
}
