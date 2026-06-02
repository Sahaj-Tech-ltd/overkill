import React, { useState, useCallback } from "react";
import { Box, Text, useInput } from "ink";
import type {
  OnboardingGatewayConfig,
  GatewayTestResult,
} from "../../backend/types.ts";
import type { BackendClient } from "../../backend/client.ts";
import { useTheme } from "../../hooks/use-theme.ts";

interface StepGatewayProps {
  backend: BackendClient;
  gateway: OnboardingGatewayConfig | null;
  setGateway: (config: OnboardingGatewayConfig | null) => void;
  onNext: () => void;
  onBack: () => void;
  saving: boolean;
  error: string | null;
}

type GatewayField = "discord" | "telegram";

interface GatewayOption {
  id: GatewayField;
  label: string;
  envVar: string;
}

const GATEWAY_OPTIONS: GatewayOption[] = [
  { id: "discord", label: "Discord", envVar: "DISCORD_TOKEN" },
  { id: "telegram", label: "Telegram", envVar: "TELEGRAM_TOKEN" },
];

export function StepGateway({
  backend,
  gateway,
  setGateway,
  onNext,
  onBack,
  saving,
  error,
}: StepGatewayProps): React.JSX.Element {
  const { theme } = useTheme();
  const [selectedIdx, setSelectedIdx] = useState(0);
  const [editingField, setEditingField] = useState<GatewayField | null>(null);
  const [tokenInput, setTokenInput] = useState("");

  // Test state
  const [testing, setTesting] = useState(false);
  const [testResult, setTestResult] = useState<GatewayTestResult | null>(null);

  const getToken = (field: GatewayField): string => {
    if (!gateway) return "";
    if (field === "discord") return gateway?.discord?.bot_token ?? "";
    return gateway?.telegram?.bot_token ?? "";
  };

  const toggleGateway = useCallback(
    (field: GatewayField) => {
      const currentToken = getToken(field);
      if (currentToken) {
        // Remove token for this gateway
        if (field === "discord") {
          const updated: OnboardingGatewayConfig = {
            ...(gateway ?? {}),
            discord: undefined,
          };
          if (!updated.discord?.bot_token && !updated.telegram?.bot_token) {
            setGateway(null);
          } else {
            setGateway(updated);
          }
        } else {
          const updated: OnboardingGatewayConfig = {
            ...(gateway ?? {}),
            telegram: undefined,
          };
          if (!updated.discord?.bot_token && !updated.telegram?.bot_token) {
            setGateway(null);
          } else {
            setGateway(updated);
          }
        }
        if (editingField === field) {
          setEditingField(null);
          setTokenInput("");
          setTestResult(null);
        }
      } else {
        // Start editing token
        setEditingField(field);
        setTokenInput("");
        setTestResult(null);
      }
    },
    [gateway, editingField, setGateway],
  );

  const handleTokenSubmit = useCallback(() => {
    if (!editingField) return;
    const updated: OnboardingGatewayConfig = { ...(gateway ?? {}) };
    if (editingField === "discord") {
      updated.discord = tokenInput ? { bot_token: tokenInput } : undefined;
    } else {
      updated.telegram = tokenInput ? { bot_token: tokenInput } : undefined;
    }
    setGateway(updated);
    setEditingField(null);
    setTokenInput("");
    setTestResult(null);
  }, [editingField, tokenInput, gateway, setGateway]);

  const runTest = useCallback(
    async (field: GatewayField) => {
      // Use the token from the input if currently editing, otherwise from saved config
      const token = editingField === field ? tokenInput : getToken(field);
      if (!token) return;

      setTesting(true);
      setTestResult(null);

      try {
        const result = await backend.call<GatewayTestResult>("gateway.test", {
          gateway: field,
          token,
        });
        setTestResult(result);
      } catch (err) {
        setTestResult({
          ok: false,
          error: (err as Error).message,
        });
      } finally {
        setTesting(false);
      }
    },
    [backend, editingField, tokenInput, gateway],
  );

  const hasAnyToken = !!(
    gateway?.discord?.bot_token || gateway?.telegram?.bot_token
  );

  useInput((input, key) => {
    if (editingField) {
      if (key.return) {
        handleTokenSubmit();
      } else if (key.escape) {
        setEditingField(null);
        setTokenInput("");
        setTestResult(null);
      } else if (key.delete || key.backspace) {
        setTokenInput((prev) => prev.slice(0, -1));
      } else if (input === "t" || input === "T") {
        // Test the current token
        const currentIdx = GATEWAY_OPTIONS.findIndex(
          (o) => o.id === editingField,
        );
        if (currentIdx >= 0) {
          void runTest(GATEWAY_OPTIONS[currentIdx].id);
        }
      } else if (input.length === 1) {
        setTokenInput((prev) => prev + input);
      }
      return;
    }

    if (key.upArrow) {
      setSelectedIdx((prev) => Math.max(0, prev - 1));
    } else if (key.downArrow) {
      setSelectedIdx((prev) => Math.min(GATEWAY_OPTIONS.length - 1, prev + 1));
    } else if (key.return || input === " ") {
      toggleGateway(GATEWAY_OPTIONS[selectedIdx].id);
    } else if (input === "t" || input === "T") {
      // Test saved token for currently highlighted gateway
      const field = GATEWAY_OPTIONS[selectedIdx].id;
      const token = getToken(field);
      if (token) {
        void runTest(field);
      }
    } else if (key.rightArrow) {
      onNext();
    } else if (key.leftArrow) {
      onBack();
    }
  });

  const maskToken = (token: string): string => {
    if (!token) return "";
    if (token.length <= 6) return "*".repeat(token.length);
    return token.slice(0, 4) + "*".repeat(token.length - 4);
  };

  return (
    <Box flexDirection="column">
      <Box flexDirection="column" marginBottom={1}>
        <Text bold>Gateway Configuration (Optional)</Text>
        <Text dimColor>
          Configure Discord/Telegram bot tokens to enable chat via messengers.
        </Text>
      </Box>

      {/* Gateway options */}
      <Box flexDirection="column" marginBottom={1}>
        {GATEWAY_OPTIONS.map((opt, i) => {
          const isHighlighted = i === selectedIdx && !editingField;
          const token = getToken(opt.id);
          const isConfigured = !!token;

          return (
            <Box key={opt.id} flexDirection="column">
              <Box>
                <Text color={isHighlighted ? "cyan" : undefined}>
                  {isHighlighted ? "▸ " : "  "}
                </Text>
                <Text
                  color={isConfigured ? "green" : undefined}
                  bold={isHighlighted}
                >
                  [{isConfigured ? "✓" : " "}] {opt.label}
                </Text>
                {isConfigured && <Text dimColor> → {maskToken(token)}</Text>}
                {isConfigured && !editingField && (
                  <Text color={theme.accent} dimColor={!isHighlighted}>
                    {" "}
                    [t:test]
                  </Text>
                )}
              </Box>

              {/* Token input */}
              {editingField === opt.id && (
                <Box marginLeft={4} flexDirection="column">
                  <Box>
                    <Text color={theme.warning}>Token: </Text>
                    <Text color={theme.text}>{tokenInput}</Text>
                    <Text dimColor>
                      {tokenInput.length > 0
                        ? "▌"
                        : "(paste token, Enter to confirm, Esc to cancel, t to test)"}
                    </Text>
                  </Box>
                </Box>
              )}

              {/* Test result for this gateway */}
              {testResult &&
                isConfigured &&
                opt.id ===
                  (editingField ?? GATEWAY_OPTIONS[selectedIdx]?.id) && (
                  <Box marginLeft={4}>
                    {testResult.ok ? (
                      <Text color={theme.success}>✓ Gateway test passed!</Text>
                    ) : (
                      <Text color={theme.error}>
                        ✗ Test failed: {testResult.error ?? "unknown error"}
                      </Text>
                    )}
                  </Box>
                )}
            </Box>
          );
        })}
      </Box>

      {/* Testing indicator */}
      {testing && (
        <Box marginBottom={1}>
          <Text color={theme.warning}>Testing gateway connection...</Text>
        </Box>
      )}

      {/* Status */}
      {hasAnyToken && (
        <Box marginBottom={1}>
          <Text color={theme.success}>
            Gateways:{" "}
            {[
              gateway?.discord?.bot_token ? "Discord" : null,
              gateway?.telegram?.bot_token ? "Telegram" : null,
            ]
              .filter(Boolean)
              .join(", ")}
          </Text>
        </Box>
      )}

      {!hasAnyToken && (
        <Box marginBottom={1}>
          <Text dimColor>No gateways configured (you can set these later)</Text>
        </Box>
      )}

      {/* Saving state */}
      {saving && (
        <Box marginBottom={1}>
          <Text color={theme.warning}>Saving configuration...</Text>
        </Box>
      )}

      {error && (
        <Box marginBottom={1}>
          <Text color={theme.error}>Error: {error}</Text>
        </Box>
      )}

      {/* Navigation */}
      <Box flexDirection="column" marginTop={1}>
        <Text dimColor>
          ↑↓ navigate · space/enter toggle · t test · right arrow finish · left
          arrow back
        </Text>
        <Text color={theme.accent} bold>
          Press right arrow to save and finish setup!
        </Text>
      </Box>
    </Box>
  );
}
