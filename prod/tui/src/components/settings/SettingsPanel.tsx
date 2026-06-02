import React, { useState, useEffect } from "react";
import { Box, Text, useStdout } from "ink";
import { DialogContainer } from "../dialogs/dialog-container.tsx";
import { Tabs, Tab } from "../design-system/Tabs.tsx";
import { ConfigTab } from "./ConfigTab.tsx";
import { SystemPromptTab } from "./SystemPromptTab.tsx";
import type { BackendClient } from "../../backend/client.ts";
import type { SessionUsageResult } from "../../backend/types.ts";
import { useTheme } from "../../hooks/use-theme.ts";

interface SettingsPanelProps {
  open: boolean;
  onClose: () => void;
  backend: BackendClient;
}

interface HealthStatus {
  status: string;
  version: number;
}

function StatusTab({ backend }: { backend: BackendClient }): React.JSX.Element {
  const { theme } = useTheme();
  const [health, setHealth] = useState<HealthStatus | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    setLoading(true);
    setError(null);
    backend
      .call<HealthStatus>("status.health")
      .then((result) => {
        setHealth(result);
      })
      .catch((err: unknown) => {
        setError((err as Error).message);
      })
      .finally(() => {
        setLoading(false);
      });
  }, [backend]);

  if (loading) {
    return (
      <Box paddingX={1}>
        <Text color={theme.warning}>Checking health...</Text>
      </Box>
    );
  }

  if (error) {
    return (
      <Box paddingX={1}>
        <Text color={theme.error}>Error: {error}</Text>
      </Box>
    );
  }

  return (
    <Box flexDirection="column" paddingX={1}>
      <Box marginBottom={1}>
        <Text bold>Connection Status</Text>
      </Box>
      <Box>
        <Text>Status: </Text>
        <Text color={health?.status === "ok" ? "green" : "red"}>
          {health?.status === "ok" ? "✓ Connected" : "✗ Disconnected"}
        </Text>
      </Box>
      {health?.version != null && (
        <Box>
          <Text>Version: </Text>
          <Text>{String(health.version)}</Text>
        </Box>
      )}
    </Box>
  );
}

function formatCount(n: number): string {
  const { theme } = useTheme();
  if (n < 1000) return String(n);
  if (n < 1_000_000) return (n / 1000).toFixed(1) + "k";
  if (n < 1_000_000_000) return (n / 1_000_000).toFixed(1) + "M";
  if (n < 1_000_000_000_000) return (n / 1_000_000_000).toFixed(1) + "B";
  return (n / 1_000_000_000_000).toFixed(1) + "T";
}

function formatUSD(n: number): string {
  if (n < 1000) return "$" + n.toFixed(2);
  return "$" + formatCount(n);
}

function UsageTab({ backend }: { backend: BackendClient }): React.JSX.Element {
  const [report, setReport] = useState<SessionUsageResult | null>(null);
  const [loading, setLoading] = useState(true);
  const [scope, setScope] = useState<"session" | "daily" | "all">("session");

  useEffect(() => {
    setLoading(true);
    backend
      .call<SessionUsageResult>("session.usage", { scope })
      .then(setReport)
      .catch(() => setReport(null))
      .finally(() => setLoading(false));
  }, [scope, backend]);

  const s = report?.report?.summary;
  const dailyS = report?.daily;

  return (
    <Box flexDirection="column" paddingX={1}>
      <Box marginBottom={1} flexDirection="row" justifyContent="space-between">
        <Text bold>Usage & Cost</Text>
        <Box>
          {(["session", "daily", "all"] as const).map((s, i) => (
            <React.Fragment key={s}>
              {i > 0 && <Text dimColor> | </Text>}
              <Text color={scope === s ? "cyan" : undefined} bold={scope === s}>
                {s}
              </Text>
            </React.Fragment>
          ))}
        </Box>
      </Box>

      {loading && <Text dimColor>Loading...</Text>}
      {!loading && !s && <Text dimColor>No usage data available yet.</Text>}
      {!loading && s && (
        <Box flexDirection="column">
          <Box marginBottom={1}>
            <Text>Input: </Text>
            <Text bold>{formatCount(s.input_tokens)}</Text>
            <Text> Output: </Text>
            <Text bold>{formatCount(s.output_tokens)}</Text>
            <Text> Cost: </Text>
            <Text bold>{formatUSD(s.total_usd)}</Text>
          </Box>
          <Text dimColor>{s.request_count} requests</Text>

          {report?.report.by_model &&
            Object.keys(report.report.by_model).length > 0 && (
              <Box flexDirection="column" marginTop={1}>
                <Text bold>By model:</Text>
                {Object.entries(report.report.by_model).map(([model, m]) => (
                  <Box key={model}>
                    <Text dimColor>{model}: </Text>
                    <Text>
                      {formatCount(m.input_tokens)} in /{" "}
                      {formatCount(m.output_tokens)} out /{" "}
                      {formatUSD(m.total_usd)}
                    </Text>
                  </Box>
                ))}
              </Box>
            )}

          {dailyS && scope !== "session" && (
            <Box flexDirection="column" marginTop={1}>
              <Text bold>Today:</Text>
              <Text>
                {formatCount(dailyS.input_tokens)} in /{" "}
                {formatCount(dailyS.output_tokens)} out /{" "}
                {formatUSD(dailyS.total_usd)}
              </Text>
            </Box>
          )}
        </Box>
      )}
    </Box>
  );
}

export function SettingsPanel({
  open,
  onClose,
  backend,
}: SettingsPanelProps): React.JSX.Element | null {
  const { stdout } = useStdout();
  const termHeight = stdout.rows ?? 24;

  if (!open) return null;

  // Calculate content height from terminal dimensions
  // DialogContainer uses ~4 rows for borders + padding + header + footer
  const dialogHeight = Math.min(termHeight - 4, 16);
  const contentHeight = dialogHeight - 4; // room for tab header + padding

  return (
    <DialogContainer open={open} onClose={onClose} title="Settings">
      <Tabs defaultTab="config" contentHeight={contentHeight}>
        <Tab id="status" title="Status">
          <StatusTab backend={backend} />
        </Tab>
        <Tab id="config" title="Config">
          <ConfigTab backend={backend} contentHeight={contentHeight} />
        </Tab>
        <Tab id="system" title="System">
          <SystemPromptTab backend={backend} />
        </Tab>
        <Tab id="usage" title="Usage">
          <UsageTab backend={backend} />
        </Tab>
      </Tabs>
    </DialogContainer>
  );
}
