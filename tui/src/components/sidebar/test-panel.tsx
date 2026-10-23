import React, { useState } from "react";
import { Box, Text, useInput } from "ink";
import type { BackendClient } from "../../backend/client.ts";
import type { TestResultsResult, TestResult } from "../../backend/types.ts";
import { useSessionStatus } from "../../hooks/use-session-status.ts";
import { useTheme } from "../../hooks/use-theme.ts";

interface TestPanelProps {
  backend: BackendClient;
  sessionId: string | null;
}

function formatDuration(ms?: number): string {
  if (ms === undefined) return "";
  if (ms < 1000) return `${ms}ms`;
  return `${(ms / 1000).toFixed(1)}s`;
}

export function TestPanel({ backend, sessionId }: TestPanelProps): React.JSX.Element {
  const { theme } = useTheme();
  const { value: results, loading, error } = useSessionStatus<TestResultsResult>(backend, sessionId, "tests.results", 3000);
  const [expandedId, setExpandedId] = useState<string | null>(null);
  const [selectedTestIdx, setSelectedTestIdx] = useState(0);

  const toggleExpand = (id: string) => {
    setExpandedId((prev) => (prev === id ? null : id));
  };

  useInput((_input, key) => {
    const tests = results?.tests ?? [];
    if (key.upArrow) {
      setSelectedTestIdx((prev) => Math.max(0, prev - 1));
    } else if (key.downArrow) {
      setSelectedTestIdx((prev) => Math.min(tests.length - 1, prev + 1));
    } else if (key.return) {
      const test = tests[selectedTestIdx];
      if (test) {
        toggleExpand(test.id);
      }
    }
  });

  return (
    <Box flexDirection="column" overflow="hidden">
      {/* Header */}
      <Box paddingX={1}>
        <Text color={theme.accent} bold>
          Verification Checks
        </Text>
      </Box>

      {/* Loading */}
      {loading && !results && (
        <Box paddingX={1}>
          <Text color={theme.warning}>Loading...</Text>
        </Box>
      )}

      {/* Error */}
      {error && !results && (
        <Box paddingX={1} flexDirection="column">
          <Text color={theme.error}>Error: {error}</Text>
          <Box marginTop={1}>
            <Text dimColor>
              Results describe actual post-write checks in the selected session.
            </Text>
          </Box>
        </Box>
      )}

      {/* Summary bar */}
      {results && (
        <Box flexDirection="column" paddingX={1}>
          <Box marginTop={1}>
            <Box marginRight={2}>
              <Text dimColor>Total: </Text>
              <Text bold>{results.total}</Text>
            </Box>
            <Box marginRight={2}>
              <Text dimColor>Passed: </Text>
              <Text color={theme.success} bold>
                {results.passed}
              </Text>
            </Box>
            <Box>
              <Text dimColor>Failed: </Text>
              <Text color={theme.error} bold>
                {results.failed}
              </Text>
            </Box>
          </Box>

          {/* Running indicator */}
          {results.running && (
            <Box marginTop={1}>
              <Text color={theme.warning}>● Running verifier...</Text>
            </Box>
          )}

          {/* Pass rate gauge */}
          <Box marginTop={1}>
            <Text dimColor>Pass rate: </Text>
            <Text
              color={
                results.total > 0 && results.passed === results.total
                  ? "green"
                  : results.failed > 0
                    ? "red"
                    : "yellow"
              }
            >
              {results.total > 0
                ? `${Math.round((results.passed / results.total) * 100)}%`
                : "—"}
            </Text>
          </Box>
        </Box>
      )}

      {/* Test list */}
      {results && results.tests.length > 0 && (
        <Box flexDirection="column" marginTop={1}>
          <Box paddingX={1}>
            <Text dimColor>{"─".repeat(28)}</Text>
          </Box>
          {results.tests.map((test: TestResult, i: number) => (
            <Box key={test.id} flexDirection="column" paddingX={1}>
              <Box>
                <Text color={i === selectedTestIdx ? "cyan" : undefined}>
                  {i === selectedTestIdx ? "▸ " : "  "}
                </Text>
                <Text color={test.passed ? "green" : test.status === "failed" ? "red" : theme.muted} bold>
                  {test.passed ? "✓" : test.status === "failed" ? "✕" : "—"}
                </Text>
                <Text> </Text>
                <Text
                  color={expandedId === test.id ? "cyan" : undefined}
                  underline={expandedId === test.id}
                >
                  {test.name}{test.status ? ` (${test.status})` : ""}{test.output_truncated ? " [display truncated]" : ""}
                </Text>
                {test.duration_ms !== undefined && (
                  <Text dimColor> ({formatDuration(test.duration_ms)})</Text>
                )}
                {test.category && (
                  <Text color={theme.muted}> [{test.category}]</Text>
                )}
              </Box>

              {/* Expanded error details */}
              {!test.passed && test.error && expandedId === test.id && (
                <Box flexDirection="column" paddingLeft={2} marginY={1}>
                  <Text color={theme.error} bold>
                    Error:
                  </Text>
                  <Box paddingLeft={2}>
                    <Text color={theme.error}>{test.error}</Text>
                  </Box>
                </Box>
              )}
            </Box>
          ))}
        </Box>
      )}

      {/* Empty state */}
      {!loading && !results && !error && (
        <Box paddingX={1} marginTop={1}>
          <Text dimColor>No test results yet.</Text>
        </Box>
      )}

      {/* Footer */}
      <Box paddingX={1} marginTop={1}>
        <Text dimColor>
          {results?.running
            ? "Auto-refreshes every 3s"
            : results
              ? "Enter on a test to expand"
              : ""}
        </Text>
      </Box>
    </Box>
  );
}
