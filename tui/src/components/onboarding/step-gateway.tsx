import React, { useState, useCallback } from "react";
import { Box, Text, useInput } from "ink";
import type { OnboardingGatewayConfig } from "../../backend/types.ts";

interface StepGatewayProps {
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
  gateway,
  setGateway,
  onNext,
  onBack,
  saving,
  error,
}: StepGatewayProps): React.JSX.Element {
  const [selectedIdx, setSelectedIdx] = useState(0);
  const [editingField, setEditingField] = useState<GatewayField | null>(null);
  const [tokenInput, setTokenInput] = useState("");

  const getToken = (field: GatewayField): string => {
    if (!gateway) return "";
    if (field === "discord") return gateway.discordToken ?? "";
    return gateway.telegramToken ?? "";
  };

  const toggleGateway = useCallback(
    (field: GatewayField) => {
      const currentToken = getToken(field);
      if (currentToken) {
        // Remove token for this gateway
        if (field === "discord") {
          const updated: OnboardingGatewayConfig = {
            ...(gateway ?? {}),
            discordToken: undefined,
          };
          // If no tokens remain, clear gateway entirely
          if (!updated.discordToken && !updated.telegramToken) {
            setGateway(null);
          } else {
            setGateway(updated);
          }
        } else {
          const updated: OnboardingGatewayConfig = {
            ...(gateway ?? {}),
            telegramToken: undefined,
          };
          if (!updated.discordToken && !updated.telegramToken) {
            setGateway(null);
          } else {
            setGateway(updated);
          }
        }
        if (editingField === field) {
          setEditingField(null);
          setTokenInput("");
        }
      } else {
        // Start editing token
        setEditingField(field);
        setTokenInput("");
      }
    },
    [gateway, editingField, setGateway],
  );

  const handleTokenSubmit = useCallback(() => {
    if (!editingField) return;
    const updated: OnboardingGatewayConfig = { ...(gateway ?? {}) };
    if (editingField === "discord") {
      updated.discordToken = tokenInput || undefined;
    } else {
      updated.telegramToken = tokenInput || undefined;
    }
    setGateway(updated);
    setEditingField(null);
    setTokenInput("");
  }, [editingField, tokenInput, gateway, setGateway]);

  const hasAnyToken = !!(
    gateway?.discordToken || gateway?.telegramToken
  );

  useInput((input, key) => {
    if (editingField) {
      if (key.return) {
        handleTokenSubmit();
      } else if (key.escape) {
        setEditingField(null);
        setTokenInput("");
      } else if (key.delete || key.backspace) {
        setTokenInput((prev) => prev.slice(0, -1));
      } else if (input.length === 1) {
        setTokenInput((prev) => prev + input);
      }
      return;
    }

    if (key.upArrow) {
      setSelectedIdx((prev) => Math.max(0, prev - 1));
    } else if (key.downArrow) {
      setSelectedIdx((prev) =>
        Math.min(GATEWAY_OPTIONS.length - 1, prev + 1),
      );
    } else if (key.return || input === " ") {
      toggleGateway(GATEWAY_OPTIONS[selectedIdx].id);
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
                <Text
                  color={isHighlighted ? "cyan" : undefined}
                >
                  {isHighlighted ? "▸ " : "  "}
                </Text>
                <Text
                  color={isConfigured ? "green" : undefined}
                  bold={isHighlighted}
                >
                  [{isConfigured ? "✓" : " "}] {opt.label}
                </Text>
                {isConfigured && (
                  <Text dimColor> → {maskToken(token)}</Text>
                )}
              </Box>

              {/* Token input */}
              {editingField === opt.id && (
                <Box marginLeft={4}>
                  <Text color="yellow">Token: </Text>
                  <Text color="white">{tokenInput}</Text>
                  <Text dimColor>
                    {tokenInput.length > 0
                      ? "▌"
                      : "(paste token, Enter to confirm, Esc to cancel)"}
                  </Text>
                </Box>
              )}
            </Box>
          );
        })}
      </Box>

      {/* Status */}
      {hasAnyToken && (
        <Box marginBottom={1}>
          <Text color="green">
            Gateways:{" "}
            {[
              gateway?.discordToken ? "Discord" : null,
              gateway?.telegramToken ? "Telegram" : null,
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
          <Text color="yellow">Saving configuration...</Text>
        </Box>
      )}

      {error && (
        <Box marginBottom={1}>
          <Text color="red">Error: {error}</Text>
        </Box>
      )}

      {/* Navigation */}
      <Box flexDirection="column" marginTop={1}>
        <Text dimColor>
          ↑↓ navigate · space/enter toggle · right arrow finish · left arrow
          back
        </Text>
        <Text color="cyan" bold>
          Press right arrow to save and finish setup!
        </Text>
      </Box>
    </Box>
  );
}
