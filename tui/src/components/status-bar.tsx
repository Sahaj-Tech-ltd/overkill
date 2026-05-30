import React from "react";
import { Text, Box } from "ink";
import type { Theme } from "../themes/definitions.ts";
import type { ConnectionState } from "../backend/types.ts";

export interface StatusBarProps {
  /** Current working directory (e.g. ~/docker/overkill) */
  directory?: string;
  /** Git branch name */
  branch?: string;
  /** Number of LSP servers connected */
  lspCount?: number;
  /** Whether any LSP is connected */
  lspConnected?: boolean;
  /** Number of MCP servers connected */
  mcpCount?: number;
  /** Whether any MCP has errors */
  mcpError?: boolean;
  /** Whether any MCP is connected */
  mcpConnected?: boolean;
  /** Permissions pending count */
  permissionsPending?: number;
  /** Whether connected to backend */
  connected?: ConnectionState;
  theme: Theme;
}

function Sep({ theme }: { theme: Theme }): React.JSX.Element {
  return <Text color={theme.muted}> │ </Text>;
}

/**
 * Bottom status bar matching OpenCode's footer.tsx layout:
 *   ~/parent/name[:branch] │ ● N LSP │ ⊙ N MCP │ /status
 */
export function StatusBar({
  directory,
  branch,
  lspCount,
  lspConnected,
  mcpCount,
  mcpConnected,
  mcpError,
  permissionsPending,
  connected,
  theme,
}: StatusBarProps): React.JSX.Element {
  // Build directory display: ~/parent/name
  let dirDisplay = directory ?? ".";
  if (dirDisplay.startsWith(process.env.HOME ?? "/home/user")) {
    dirDisplay = "~" + dirDisplay.slice((process.env.HOME ?? "/home/user").length);
  }
  const pathLabel = branch ? `${dirDisplay}:${branch}` : dirDisplay;

  // LSP indicator
  const lspLabel = (lspCount ?? 0) > 0 ? `${lspCount} LSP` : null;
  const lspDot = lspConnected ? "●" : "○";

  // MCP indicator  
  const mcpLabel = (mcpCount ?? 0) > 0 ? `${mcpCount} MCP` : null;
  const mcpDot = mcpError ? "⊙" : mcpConnected ? "⊙" : "○";

  return (
    <Box flexDirection="row" justifyContent="space-between" flexShrink={0} paddingX={1}>
      {/* Left: directory */}
      <Text color={theme.muted}>{pathLabel}</Text>

      {/* Right: status indicators */}
      <Box gap={2}>
        {/* Permissions warning */}
        {(permissionsPending ?? 0) > 0 && (
          <Text color={theme.warning}>
            △ {permissionsPending} Permission{permissionsPending !== 1 ? "s" : ""}
          </Text>
        )}

        {/* LSP */}
        {lspLabel && (
          <Text>
            <Text color={lspConnected ? theme.success : theme.muted}>{lspDot}</Text>
            <Text color={theme.text}> {lspLabel}</Text>
          </Text>
        )}

        {/* MCP */}
        {mcpLabel && (
          <Text>
            <Text color={mcpConnected ? theme.success : mcpError ? theme.error : theme.muted}>{mcpDot}</Text>
            <Text color={theme.text}> {mcpLabel}</Text>
          </Text>
        )}

        {/* /status hint when connected */}
        {connected === "connected" && (
          <Text color={theme.muted}>/status</Text>
        )}
      </Box>
    </Box>
  );
}
