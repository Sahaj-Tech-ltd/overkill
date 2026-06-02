import React, { useState, useEffect, useCallback, useRef } from "react";
import { Box, Text } from "ink";
import type { BackendClient } from "../../backend/client.ts";
import type { SelfEvalStatus } from "../../backend/types.ts";
import { useTheme } from "../../hooks/use-theme.ts";
import type { Theme } from "../../themes/definitions.ts";

interface SelfEvalPanelProps {
  backend: BackendClient;
}

const PHASE_LABELS: Record<string, string> = {
  idle: "Idle",
  planning: "Planning",
  executing: "Executing",
  reflecting: "Reflecting",
  red_team_check: "Red Team Check",
};

function getPhaseColor(phase: string, theme: Theme): string {
  switch (phase) {
    case "idle":
      return theme.muted;
    case "planning":
      return theme.accent;
    case "executing":
      return theme.warning;
    case "reflecting":
      return theme.highlight;
    case "red_team_check":
      return theme.error;
    default:
      return theme.text;
  }
}

function formatConfidence(score: number): string {
  return `${Math.round(score * 100)}%`;
}

function formatTime(iso?: string): string {
  if (!iso) return "—";
  try {
    const d = new Date(iso);
    return d.toLocaleTimeString("en-US", {
      hour: "2-digit",
      minute: "2-digit",
      second: "2-digit",
    });
  } catch {
    return iso;
  }
}

export function SelfEvalPanel({
  backend,
}: SelfEvalPanelProps): React.JSX.Element {
  const { theme } = useTheme();
  const [status, setStatus] = useState<SelfEvalStatus | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const intervalRef = useRef<ReturnType<typeof setInterval> | null>(null);

  const fetchStatus = useCallback(() => {
    backend
      .call<SelfEvalStatus>("self.eval.status")
      .then((result) => {
        setStatus(result);
        setError(null);
      })
      .catch((err: unknown) => {
        if ((err as Error).message !== "AbortError") {
          setError((err as Error).message);
        }
      })
      .finally(() => {
        setLoading(false);
      });
  }, [backend]);

  useEffect(() => {
    setLoading(true);
    fetchStatus();

    // Poll every 2s for live updates
    intervalRef.current = setInterval(fetchStatus, 2000);

    return () => {
      if (intervalRef.current) {
        clearInterval(intervalRef.current);
      }
    };
  }, [fetchStatus]);

  const phase = status?.phase ?? "idle";
  const phaseColor = getPhaseColor(phase, theme);
  const phaseLabel = PHASE_LABELS[phase] ?? phase;

  return (
    <Box flexDirection="column" overflow="hidden">
      {/* Header */}
      <Box paddingX={1}>
        <Text color={theme.accent} bold>
          Self-Evaluation
        </Text>
      </Box>

      {/* Loading */}
      {loading && !status && (
        <Box paddingX={1}>
          <Text color={theme.warning}>Loading...</Text>
        </Box>
      )}

      {/* Error with fallback state */}
      {error && !status && (
        <Box paddingX={1} flexDirection="column">
          <Text color={theme.error}>Error: {error}</Text>
          <Box marginTop={1}>
            <Text dimColor>
              Ensure self-evaluation is enabled in config (walls.self_eval).
            </Text>
          </Box>
        </Box>
      )}

      {/* Status display */}
      {status && (
        <Box flexDirection="column" paddingX={1}>
          {/* Phase indicator */}
          <Box marginTop={1}>
            <Text bold>Phase: </Text>
            <Text color={phaseColor} bold>
              {phaseLabel}
            </Text>
          </Box>

          {/* Confidence score with gauge */}
          <Box marginTop={1} flexDirection="column">
            <Box>
              <Text bold>Confidence: </Text>
              <Text
                color={
                  status.confidence >= 0.7
                    ? theme.success
                    : status.confidence >= 0.4
                      ? theme.warning
                      : theme.error
                }
              >
                {formatConfidence(status.confidence)}
              </Text>
            </Box>
            {/* Mini gauge bar */}
            <Box>
              <Text dimColor>[</Text>
              <Text
                color={
                  status.confidence >= 0.7
                    ? theme.success
                    : status.confidence >= 0.4
                      ? theme.warning
                      : theme.error
                }
              >
                {"█".repeat(Math.floor(status.confidence * 20))}
              </Text>
              <Text dimColor>
                {"░".repeat(20 - Math.floor(status.confidence * 20))}
              </Text>
              <Text dimColor>]</Text>
            </Box>
          </Box>

          {/* Iteration counter */}
          <Box marginTop={1}>
            <Text bold>Iteration: </Text>
            <Text color={theme.accent}>
              {status.iteration}/{status.max_iterations}
            </Text>
          </Box>

          {/* Red-team gate */}
          <Box marginTop={1}>
            <Text bold>Red-Team Gate: </Text>
            {status.red_team_passed === undefined ? (
              <Text dimColor>pending</Text>
            ) : status.red_team_passed ? (
              <Text color={theme.success}>✓ Passed</Text>
            ) : (
              <Text color={theme.error}>✕ Failed</Text>
            )}
          </Box>

          {/* Reflection notes */}
          {status.reflection_notes && (
            <Box marginTop={1} flexDirection="column">
              <Text bold>Reflection: </Text>
              <Box paddingLeft={2}>
                <Text dimColor>{status.reflection_notes}</Text>
              </Box>
            </Box>
          )}

          {/* Started at */}
          {status.started_at && (
            <Box marginTop={1}>
              <Text dimColor>Started: {formatTime(status.started_at)}</Text>
            </Box>
          )}
        </Box>
      )}

      {/* Empty state when no status and no error */}
      {!loading && !status && !error && (
        <Box paddingX={1} marginTop={1}>
          <Text dimColor>No evaluation in progress.</Text>
        </Box>
      )}

      {/* Footer hint */}
      <Box paddingX={1} marginTop={1}>
        <Text dimColor>Auto-refreshes every 2s</Text>
      </Box>
    </Box>
  );
}
