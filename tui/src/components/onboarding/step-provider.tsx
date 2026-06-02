import React, { useState, useEffect, useCallback } from "react";
import { Box, Text, useInput } from "ink";
import type { BackendClient } from "../../backend/client.ts";
import type {
  OnboardingProviderConfig,
  ProviderInfo,
} from "../../backend/types.ts";
import { useTheme } from "../../hooks/use-theme.ts";

interface StepProviderProps {
  backend: BackendClient;
  providers: OnboardingProviderConfig[];
  setProviders: (providers: OnboardingProviderConfig[]) => void;
  onNext: () => void;
  onBack: () => void;
}

type CustomField = "name" | "baseUrl" | "apiKey";

/** Detect provider from API key prefix. Returns provider name or null if ambiguous. */
function detectProvider(key: string): string | null {
  if (key.startsWith("sk-ant-")) return "anthropic";
  if (key.startsWith("sk-proj-")) return "openai";
  if (key.startsWith("sk-or-")) return "openrouter";
  if (key.startsWith("gsk_")) return "groq";
  if (key.startsWith("xai-")) return "xai";
  // All other sk- prefixes are ambiguous (OpenAI legacy, DeepSeek, Together, etc.)
  return null;
}

const AMBIGUOUS_SK_PROVIDERS = [
  { id: "openai", label: "OpenAI" },
  { id: "deepseek", label: "DeepSeek" },
  { id: "together", label: "Together.ai" },
  { id: "deepinfra", label: "DeepInfra" },
  { id: "openai-compat", label: "Other (OpenAI-compatible)" },
] as const;

/** Validate an API key format for the given provider. Returns error message or null if valid. */
function validateKeyFormat(provider: string, key: string): string | null {
  if (!key || key.trim().length === 0) return "API key cannot be empty";
  const trimmed = key.trim();
  switch (provider) {
    case "openai":
      if (!trimmed.startsWith("sk-") || trimmed.length < 20)
        return "OpenAI keys must start with 'sk-' and be at least 20 characters";
      return null;
    case "anthropic":
      if (!trimmed.startsWith("sk-ant-") || trimmed.length < 30)
        return "Anthropic keys must start with 'sk-ant-' and be at least 30 characters";
      return null;
    case "deepseek":
      if (!trimmed.startsWith("sk-") || trimmed.length < 20)
        return "DeepSeek keys must start with 'sk-' and be at least 20 characters";
      return null;
    case "groq":
      if (!trimmed.startsWith("gsk_") || trimmed.length < 20)
        return "Groq keys must start with 'gsk_' and be at least 20 characters";
      return null;
    case "xai":
      if (!trimmed.startsWith("xai-") || trimmed.length < 20)
        return "xAI keys must start with 'xai-' and be at least 20 characters";
      return null;
    case "google":
      if (trimmed.length < 20)
        return "Google API keys must be at least 20 characters";
      return null;
    default:
      // Custom providers: minimum length check only
      if (trimmed.length < 8) return "API key must be at least 8 characters";
      return null;
  }
}

/** Validate a base URL string. Returns error message or null if valid. */
function validateBaseURL(url: string): string | null {
  if (!url || url.trim().length === 0) return null; // optional field
  const trimmed = url.trim();
  try {
    const parsed = new URL(trimmed);
    if (!["http:", "https:"].includes(parsed.protocol))
      return "Base URL must use http:// or https://";
    return null;
  } catch {
    return "Invalid URL format (e.g. http://localhost:1234/v1)";
  }
}

export function StepProvider({
  backend,
  providers,
  setProviders,
  onNext,
  onBack,
}: StepProviderProps): React.JSX.Element {
  const { theme } = useTheme();
  const [availableProviders, setAvailableProviders] = useState<string[]>([]);
  const [loading, setLoading] = useState(true);
  const [fetchError, setFetchError] = useState<string | null>(null);
  const [selectedIdx, setSelectedIdx] = useState(0);
  const [editingProvider, setEditingProvider] = useState<string | null>(null);
  const [apiKeyInput, setApiKeyInput] = useState("");
  const [detectedProvider, setDetectedProvider] = useState<string | null>(null);
  const [validationError, setValidationError] = useState<string | null>(null);
  // When the key prefix is an ambiguous sk-, ask user to clarify
  const [ambiguousSkMode, setAmbiguousSkMode] = useState(false);
  const [ambiguousSkIdx, setAmbiguousSkIdx] = useState(0);

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
        const resp = await backend.call<{ providers: ProviderInfo[] }>(
          "providers.list",
        );
        const result = resp?.providers ?? [];
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
      // Ambiguous sk- key — ask the user which provider it belongs to
      if (!detected && input.startsWith("sk-") && input.length >= 8) {
        setAmbiguousSkMode(true);
        setAmbiguousSkIdx(0);
      } else {
        setAmbiguousSkMode(false);
        // If detected matches a different provider than the one being edited,
        // auto-highlight that provider
        if (detected && editingProvider !== detected) {
          const idx = displayList.indexOf(detected);
          if (idx >= 0) {
            setSelectedIdx(idx);
          }
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
          setAmbiguousSkMode(false);
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
        setAmbiguousSkMode(false);
      } else {
        setProviders([...providers, { name, apiKey: "" }]);
        setEditingProvider(name);
        setApiKeyInput("");
        setDetectedProvider(null);
        setAmbiguousSkMode(false);
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
    setValidationError(null);

    if (editingProvider === "custom") {
      // Complete custom provider
      if (customName.trim() && customApiKey.trim()) {
        // Check for duplicate provider names
        const nameLower = customName.trim().toLowerCase();
        const duplicate = providers.find(
          (p) => p.name.toLowerCase() === nameLower,
        );
        if (duplicate) {
          setValidationError(`Provider "${customName.trim()}" already exists`);
          return;
        }
        // Validate key format
        const keyErr = validateKeyFormat("custom", customApiKey.trim());
        if (keyErr) {
          setValidationError(keyErr);
          return;
        }
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
      setAmbiguousSkMode(false);
      resetCustomFields();
    } else {
      // Validate key before accepting
      const trimmed = apiKeyInput.trim();
      const keyErr = validateKeyFormat(editingProvider, trimmed);
      if (keyErr) {
        setValidationError(keyErr);
        return;
      }
      setProviders(
        providers.map((p) =>
          p.name === editingProvider ? { ...p, apiKey: trimmed } : p,
        ),
      );
      setEditingProvider(null);
      setApiKeyInput("");
      setDetectedProvider(null);
      setAmbiguousSkMode(false);
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
    setValidationError(null);
    if (customField === "name") {
      if (customName.trim()) {
        // Check for duplicate names early
        const nameLower = customName.trim().toLowerCase();
        const duplicate = providers.find(
          (p) => p.name.toLowerCase() === nameLower,
        );
        if (duplicate) {
          setValidationError(`Provider "${customName.trim()}" already exists`);
          return;
        }
        setCustomField("baseUrl");
      }
    } else if (customField === "baseUrl") {
      // Validate base URL format before proceeding
      if (customBaseUrl.trim()) {
        const urlErr = validateBaseURL(customBaseUrl.trim());
        if (urlErr) {
          setValidationError(urlErr);
          return;
        }
      }
      setCustomField("apiKey");
    } else {
      // apiKey field — finish
      handleKeySubmit();
    }
  }, [customField, customName, customBaseUrl, handleKeySubmit, providers]);

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
        setValidationError(null);
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
      // Ambiguous sk- picker is active — intercept nav/confirm
      if (ambiguousSkMode) {
        if (key.upArrow) {
          setAmbiguousSkIdx((prev) => Math.max(0, prev - 1));
          return;
        } else if (key.downArrow) {
          setAmbiguousSkIdx((prev) =>
            Math.min(AMBIGUOUS_SK_PROVIDERS.length - 1, prev + 1),
          );
          return;
        } else if (key.return) {
          const chosen = AMBIGUOUS_SK_PROVIDERS[ambiguousSkIdx].id;
          setDetectedProvider(chosen);
          setAmbiguousSkMode(false);
          // Auto-switch highlighted row to chosen provider if available
          const idx = displayList.indexOf(chosen);
          if (idx >= 0) setSelectedIdx(idx);
          return;
        } else if (key.escape) {
          setAmbiguousSkMode(false);
          return;
        }
        // While picker is open, still let typing update the key
      }

      if (key.return) {
        handleKeySubmit();
      } else if (key.escape) {
        setEditingProvider(null);
        setApiKeyInput("");
        setDetectedProvider(null);
        setAmbiguousSkMode(false);
        setValidationError(null);
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
      setSelectedIdx((prev) => Math.min(displayList.length - 1, prev + 1));
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
          <Text color={theme.warning}>Fetching available providers...</Text>
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
          <Text color={theme.error}>
            Failed to fetch providers: {fetchError}
          </Text>
        </Box>
      )}
      {!fetchError && availableProviders.length === 0 && (
        <Box marginBottom={1}>
          <Text color={theme.warning}>
            No providers discovered. Add a custom provider below or check your
            config.
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
                    <Text color={theme.warning}>API Key: </Text>
                    <Text color={theme.text}>{apiKeyInput}</Text>
                    <Text dimColor>
                      {apiKeyInput.length > 0
                        ? "▌"
                        : "(type key, Enter to confirm, Esc to cancel)"}
                    </Text>
                  </Box>
                  {detectedProvider && !ambiguousSkMode && (
                    <Box marginLeft={9}>
                      <Text color={theme.accent} bold>
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
                  {/* Ambiguous sk- key: ask user to pick provider */}
                  {ambiguousSkMode && (
                    <Box marginLeft={9} flexDirection="column">
                      <Text color={theme.warning}>
                        Ambiguous key — select provider:
                      </Text>
                      {AMBIGUOUS_SK_PROVIDERS.map((p, pi) => (
                        <Box key={p.id}>
                          <Text
                            color={pi === ambiguousSkIdx ? "cyan" : undefined}
                            bold={pi === ambiguousSkIdx}
                          >
                            {pi === ambiguousSkIdx ? "▸ " : "  "}
                            {p.label}
                          </Text>
                        </Box>
                      ))}
                      <Text dimColor>
                        ↑↓ navigate · Enter confirm · Esc dismiss
                      </Text>
                    </Box>
                  )}
                  {validationError && editingProvider !== "custom" && (
                    <Box marginLeft={9}>
                      <Text color={theme.error}>✗ {validationError}</Text>
                    </Box>
                  )}
                </Box>
              )}

              {/* Custom provider multi-field input */}
              {isEditing && name === "custom" && (
                <Box marginLeft={4} flexDirection="column">
                  <Box>
                    <Text color={theme.warning}>
                      Provider Name{customField === "name" ? ": " : ": "}
                    </Text>
                    <Text
                      color={customField === "name" ? "white" : "gray"}
                      dimColor={customField !== "name"}
                    >
                      {customName || "(enter name)"}
                    </Text>
                    {customField === "name" && <Text dimColor> ▌</Text>}
                  </Box>
                  {customField !== "name" && (
                    <Box>
                      <Text color={theme.warning}>
                        Base URL{customField === "baseUrl" ? ": " : ": "}
                      </Text>
                      <Text
                        color={customField === "baseUrl" ? "white" : "gray"}
                        dimColor={customField !== "baseUrl"}
                      >
                        {customBaseUrl || "(e.g. http://localhost:1234/v1)"}
                      </Text>
                      {customField === "baseUrl" && <Text dimColor> ▌</Text>}
                    </Box>
                  )}
                  {customField === "apiKey" && (
                    <Box flexDirection="column">
                      <Box>
                        <Text color={theme.warning}>API Key: </Text>
                        <Text color={theme.text}>{customApiKey}</Text>
                        <Text dimColor> ▌</Text>
                      </Box>
                      {detectedProvider && (
                        <Box marginLeft={9}>
                          <Text color={theme.accent} bold>
                            Detected: {detectedProvider}
                          </Text>
                        </Box>
                      )}
                    </Box>
                  )}
                  <Text dimColor>Enter to confirm field · Esc to cancel</Text>
                  {validationError && editingProvider === "custom" && (
                    <Box>
                      <Text color={theme.error}>✗ {validationError}</Text>
                    </Box>
                  )}
                </Box>
              )}
            </Box>
          );
        })}
      </Box>

      {/* Navigation */}
      <Box flexDirection="column" marginTop={1}>
        <Text dimColor>
          ↑↓ navigate · space/enter toggle · right arrow next · left arrow back
        </Text>
        {providers.length > 0 && (
          <Text color={theme.success}>
            Selected: {providers.map((p) => p.name).join(", ")}
          </Text>
        )}
      </Box>
    </Box>
  );
}
