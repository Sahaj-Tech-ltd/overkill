import React, { Component, type ReactNode } from "react";
import { Box, Text, useInput } from "ink";
import { useTheme } from "../hooks/use-theme.ts";
import { log } from "../logger.ts";

interface ErrorBoundaryProps {
  children: ReactNode;
}

interface ErrorBoundaryState {
  hasError: boolean;
  error: Error | null;
}

function ErrorScreen({
  error,
  onRestart,
}: {
  error: Error;
  onRestart: () => void;
}): React.JSX.Element {
  const { theme } = useTheme();
  useInput(() => {
    onRestart();
  });

  return (
    <Box
      flexDirection="column"
      alignItems="center"
      justifyContent="center"
      height="100%"
    >
      <Box
        flexDirection="column"
        borderStyle="round"
        borderColor="red"
        paddingX={2}
        paddingY={1}
      >
        <Box marginBottom={1}>
          <Text color={theme.error} bold>
            ⚠ Something went wrong
          </Text>
        </Box>
        <Box marginBottom={1}>
          <Text color={theme.error}>{error.message}</Text>
        </Box>
        {error.stack && (
          <Box marginBottom={1} flexDirection="column">
            {error.stack
              .split("\n")
              .slice(0, 5)
              .map((line, i) => (
                <Text key={i} dimColor>
                  {line}
                </Text>
              ))}
          </Box>
        )}
        <Box>
          <Text color={theme.muted} italic>
            Press any key to restart...
          </Text>
        </Box>
      </Box>
    </Box>
  );
}

export class ErrorBoundary extends Component<
  ErrorBoundaryProps,
  ErrorBoundaryState
> {
  constructor(props: ErrorBoundaryProps) {
    super(props);
    this.state = { hasError: false, error: null };
  }

  static getDerivedStateFromError(error: Error): ErrorBoundaryState {
    return { hasError: true, error };
  }

  override componentDidCatch(error: Error, errorInfo: React.ErrorInfo): void {
    log.error(
      `ErrorBoundary caught: ${error.message}`,
      errorInfo?.componentStack,
    );
  }

  handleRestart = (): void => {
    this.setState({ hasError: false, error: null });
  };

  override render(): ReactNode {
    if (this.state.hasError && this.state.error) {
      return (
        <ErrorScreen error={this.state.error} onRestart={this.handleRestart} />
      );
    }

    return this.props.children;
  }
}
