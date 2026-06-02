import React, { useState, useEffect, useCallback } from "react";
import { Box, Text, useInput } from "ink";
import { DialogContainer } from "./dialog-container.tsx";
import type { BackendClient } from "../../backend/client.ts";
import type { ProviderInfo, ModelInfo } from "../../backend/types.ts";
import { useTheme } from "../../hooks/use-theme.ts";

interface ModelSwitcherProps {
  open: boolean;
  onClose: () => void;
  backend: BackendClient;
  currentModel?: string;
  currentProvider?: string;
  onSelect: (provider: string, model: ModelInfo) => void;
}

/** Input handler that only mounts when the dialog is open. */
function ModelSwitcherInput({
  providers,
  selectedProviderIdx,
  setSelectedProviderIdx,
  selectedModelIdx,
  setSelectedModelIdx,
  focusPanel,
  setFocusPanel,
  onSelect,
  onClose,
}: {
  providers: ProviderInfo[];
  selectedProviderIdx: number;
  setSelectedProviderIdx: (n: number) => void;
  selectedModelIdx: number;
  setSelectedModelIdx: (n: number) => void;
  focusPanel: "providers" | "models";
  setFocusPanel: (p: "providers" | "models") => void;
  onSelect: (provider: string, model: ModelInfo) => void;
  onClose: () => void;
}) {
  const currentProviderData = providers[selectedProviderIdx];
  const models = currentProviderData?.models ?? [];

  useInput((_input, key) => {
    if (key.tab) {
      setFocusPanel(focusPanel === "providers" ? "models" : "providers");
    } else if (key.leftArrow) {
      setFocusPanel("providers");
    } else if (key.rightArrow) {
      setFocusPanel("models");
    } else if (key.upArrow) {
      if (focusPanel === "providers") {
        const next = Math.max(0, selectedProviderIdx - 1);
        setSelectedProviderIdx(next);
        setSelectedModelIdx(0);
      } else {
        setSelectedModelIdx(Math.max(0, selectedModelIdx - 1));
      }
    } else if (key.downArrow) {
      if (focusPanel === "providers") {
        const next = Math.min(providers.length - 1, selectedProviderIdx + 1);
        setSelectedProviderIdx(next);
        setSelectedModelIdx(0);
      } else {
        setSelectedModelIdx(Math.min(models.length - 1, selectedModelIdx + 1));
      }
    } else if (key.return) {
      if (
        focusPanel === "models" &&
        currentProviderData &&
        models[selectedModelIdx]
      ) {
        onSelect(currentProviderData.name, models[selectedModelIdx]);
        onClose();
      } else {
        setFocusPanel("models");
      }
    }
  });
  return null;
}

export function ModelSwitcher({
  open,
  onClose,
  backend,
  currentModel,
  currentProvider,
  onSelect,
}: ModelSwitcherProps): React.JSX.Element | null {
  const { theme } = useTheme();
  const [providers, setProviders] = useState<ProviderInfo[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [selectedProviderIdx, setSelectedProviderIdx] = useState(0);
  const [selectedModelIdx, setSelectedModelIdx] = useState(0);
  const [focusPanel, setFocusPanel] = useState<"providers" | "models">(
    "providers",
  );

  useEffect(() => {
    if (!open) return;

    setLoading(true);
    setError(null);
    setSelectedProviderIdx(0);
    setSelectedModelIdx(0);
    setFocusPanel("providers");

    backend
      .call<{ providers: ProviderInfo[] }>("providers.list")
      .then((result) => {
        setProviders(result.providers ?? []);

        // If current provider exists, pre-select it
        if (currentProvider) {
          const pIdx = (result.providers ?? []).findIndex(
            (p) => p.name === currentProvider,
          );
          if (pIdx >= 0) {
            setSelectedProviderIdx(pIdx);
          }
        }
      })
      .catch((err: unknown) => {
        setError((err as Error).message);
      })
      .finally(() => {
        setLoading(false);
      });
  }, [open, backend, currentProvider]);

  const currentProviderData = providers[selectedProviderIdx];
  const [liveModels, setLiveModels] = useState<ModelInfo[] | null>(null);

  // L19: call models.list when provider changes to surface live/fresher models.
  useEffect(() => {
    const providerName = currentProviderData?.name;
    if (!providerName || !open) return;
    setLiveModels(null);
    backend
      .call<{ models: ModelInfo[] }>("models.list", { provider: providerName })
      .then((result) => {
        if (result?.models?.length) setLiveModels(result.models);
      })
      .catch(() => {
        // silently fall back to providers.list data
      });
  }, [selectedProviderIdx, open, backend, currentProviderData?.name]);

  const models = liveModels ?? currentProviderData?.models ?? [];

  if (!open) return null;

  const isActive = (provider: string, modelId: string) =>
    provider === currentProvider && modelId === currentModel;

  return (
    <DialogContainer open={open} onClose={onClose} title="Switch Model">
      <ModelSwitcherInput
        providers={providers}
        selectedProviderIdx={selectedProviderIdx}
        setSelectedProviderIdx={setSelectedProviderIdx}
        selectedModelIdx={selectedModelIdx}
        setSelectedModelIdx={setSelectedModelIdx}
        focusPanel={focusPanel}
        setFocusPanel={setFocusPanel}
        onSelect={onSelect}
        onClose={onClose}
      />
      {loading && (
        <Box paddingX={1} paddingY={1}>
          <Text color={theme.warning}>Loading providers...</Text>
        </Box>
      )}

      {error && (
        <Box paddingX={1} paddingY={1}>
          <Text color={theme.error}>Error: {error}</Text>
        </Box>
      )}

      {!loading && !error && (
        <Box flexDirection="row" height={10}>
          {/* Left panel: providers */}
          <Box
            flexDirection="column"
            width={20}
            borderStyle={focusPanel === "providers" ? "round" : undefined}
            borderColor={focusPanel === "providers" ? "cyan" : undefined}
            paddingX={1}
          >
            <Box marginBottom={1}>
              <Text
                bold
                color={focusPanel === "providers" ? "cyan" : undefined}
              >
                Providers
              </Text>
            </Box>
            {providers.length === 0 && (
              <Text dimColor> No providers found</Text>
            )}
            {providers.map((p, i) => (
              <Box key={p.name}>
                <Text color={i === selectedProviderIdx ? "cyan" : undefined}>
                  {i === selectedProviderIdx ? "▸ " : "  "}
                  {p.name}
                </Text>
              </Box>
            ))}
          </Box>

          {/* Right panel: models */}
          <Box
            flexDirection="column"
            flexGrow={1}
            borderStyle={focusPanel === "models" ? "round" : undefined}
            borderColor={focusPanel === "models" ? "cyan" : undefined}
            paddingX={1}
          >
            <Box marginBottom={1}>
              <Text bold color={focusPanel === "models" ? "cyan" : undefined}>
                Models
              </Text>
            </Box>
            {models.length === 0 && <Text dimColor> Select a provider</Text>}
            {models.map((m, i) => (
              <Box key={m.id}>
                <Text color={i === selectedModelIdx ? "cyan" : undefined}>
                  {i === selectedModelIdx ? "▸ " : "  "}
                  {isActive(currentProviderData?.name ?? "", m.id)
                    ? "● "
                    : "  "}
                  {m.name}
                  {m.context_window ? (
                    <Text dimColor>
                      {" "}
                      ({Math.round(m.context_window / 1000)}k)
                    </Text>
                  ) : m.maxTokens ? (
                    <Text dimColor> ({Math.round(m.maxTokens / 1000)}k)</Text>
                  ) : null}
                </Text>
              </Box>
            ))}
          </Box>
        </Box>
      )}

      <Box marginTop={1} paddingX={1}>
        <Text dimColor>
          Tab/arrows to navigate · Enter to select · Esc to close
        </Text>
      </Box>
    </DialogContainer>
  );
}
