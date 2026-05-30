import React, { useState, useEffect, useCallback } from "react";
import { Box, Text, useInput } from "ink";
import type { BackendClient } from "../../backend/client.ts";
import type {
  WizardCatalogResult,
  WizardOption,
  QuickSetup,
} from "../../backend/types.ts";

interface WizardPanelProps {
  backend: BackendClient;
}

function StarRating({ rating }: { rating: number }): React.JSX.Element {
  // The API sends pre-rendered stars, fallback to manual rendering
  const stars = "⭐".repeat(Math.max(0, Math.min(5, rating)));
  return <Text color="yellow">{stars || "—"}</Text>;
}

function WizardSection({
  title,
  options,
  recommendedId,
  onSelect,
  selected,
  loading,
}: {
  title: string;
  options: WizardOption[];
  recommendedId?: string;
  onSelect: (id: string) => void;
  selected: string;
  loading: boolean;
}): React.JSX.Element {
  return (
    <Box flexDirection="column" marginBottom={1}>
      <Box paddingX={1}>
        <Text bold color="magenta">
          {title}
        </Text>
        <Text dimColor> ({options.length})</Text>
      </Box>
      {options.map((opt) => {
        const isRecommended = opt.id === recommendedId;
        const isSelected = opt.id === selected;

        return (
          <Box key={opt.id} flexDirection="column" paddingX={2}>
            <Box>
              <Text color={isSelected ? "cyan" : undefined}>
                {isSelected ? "▸ " : "  "}
              </Text>
              <StarRating rating={opt.rating} />
              <Text> </Text>
              <Text
                bold={isSelected}
                color={isRecommended ? "green" : undefined}
              >
                {opt.name}
              </Text>
              {isRecommended && (
                <Text color="green" bold>
                  {" "}
                  ★ Recommended
                </Text>
              )}
              {isSelected && !isRecommended && (
                <Text color="cyan"> (selected)</Text>
              )}
            </Box>
            <Box paddingLeft={4}>
              <Text dimColor>{opt.description.slice(0, 60)}
                {opt.description.length > 60 ? "..." : ""}
              </Text>
            </Box>
            {opt.tags && opt.tags.length > 0 && (
              <Box paddingLeft={4}>
                <Text color="grey">
                  Tags: {opt.tags.join(", ")}
                </Text>
              </Box>
            )}
          </Box>
        );
      })}
      {loading && options.length === 0 && (
        <Box paddingX={2}>
          <Text color="yellow">Loading...</Text>
        </Box>
      )}
    </Box>
  );
}

export function WizardPanel({
  backend,
}: WizardPanelProps): React.JSX.Element {
  const [catalog, setCatalog] = useState<WizardCatalogResult | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [selectedProvider, setSelectedProvider] = useState("");
  const [selectedGateway, setSelectedGateway] = useState("");
  const [selectedTTS, setSelectedTTS] = useState("");
  const [selectedDatabase, setSelectedDatabase] = useState("");
  const [applying, setApplying] = useState(false);
  const [applyResult, setApplyResult] = useState<string | null>(null);

  const fetchCatalog = useCallback(() => {
    setLoading(true);
    setError(null);
    backend
      .call<WizardCatalogResult>("wizard.catalog")
      .then((result) => {
        setCatalog(result);
        // Pre-select recommended
        if (result.recommended) {
          setSelectedProvider(result.recommended.provider);
          setSelectedGateway(result.recommended.gateway);
          setSelectedTTS(result.recommended.tts);
          setSelectedDatabase(result.recommended.database);
        }
        setError(null);
      })
      .catch((err: unknown) => {
        setError((err as Error).message);
      })
      .finally(() => {
        setLoading(false);
      });
  }, [backend]);

  useEffect(() => {
    fetchCatalog();
  }, [fetchCatalog]);

  const handleUseRecommended = useCallback(() => {
    if (!catalog?.recommended) return;

    setApplying(true);
    setApplyResult(null);

    const rec = catalog.recommended;
    backend
      .call<{ status: string; message: string }>("wizard.quick-setup", {
        provider: selectedProvider || rec.provider,
        model: rec.model,
        gateway: selectedGateway || rec.gateway,
        tts: selectedTTS || rec.tts,
        database: selectedDatabase || rec.database,
      })
      .then((result) => {
        setApplyResult(`✓ ${result.message || result.status}`);
        setError(null);
      })
      .catch((err: unknown) => {
        setApplyResult(`✕ ${(err as Error).message}`);
      })
      .finally(() => {
        setApplying(false);
      });
  }, [backend, catalog, selectedProvider, selectedGateway, selectedTTS, selectedDatabase]);

  const hasSelection =
    selectedProvider || selectedGateway || selectedTTS || selectedDatabase;

  // Wire Enter key to trigger "Use Recommended" when the catalog is loaded.
  useInput((input, key) => {
    if (key.return && catalog && !applying) {
      handleUseRecommended();
    }
  });

  return (
    <Box flexDirection="column" overflow="hidden">
      {/* Header */}
      <Box paddingX={1}>
        <Text color="cyan" bold>
          ⚡ Setup Wizard
        </Text>
      </Box>

      {/* Loading */}
      {loading && (
        <Box paddingX={1}>
          <Text color="yellow">Loading catalog...</Text>
        </Box>
      )}

      {/* Error */}
      {error && !catalog && (
        <Box paddingX={1}>
          <Text color="red">Error: {error}</Text>
        </Box>
      )}

      {/* Catalog sections */}
      {catalog && (
        <Box flexDirection="column" marginTop={1}>
          <Box paddingX={1}>
            <Text dimColor>{"─".repeat(28)}</Text>
          </Box>

          <WizardSection
            title="Providers"
            options={catalog.providers}
            recommendedId={catalog.recommended?.provider}
            selected={selectedProvider}
            loading={loading}
            onSelect={setSelectedProvider}
          />

          <Box paddingX={1}>
            <Text dimColor>{"─".repeat(28)}</Text>
          </Box>

          <WizardSection
            title="Gateways"
            options={catalog.gateways}
            recommendedId={catalog.recommended?.gateway}
            selected={selectedGateway}
            loading={loading}
            onSelect={setSelectedGateway}
          />

          <Box paddingX={1}>
            <Text dimColor>{"─".repeat(28)}</Text>
          </Box>

          <WizardSection
            title="TTS"
            options={catalog.tts}
            recommendedId={catalog.recommended?.tts}
            selected={selectedTTS}
            loading={loading}
            onSelect={setSelectedTTS}
          />

          <Box paddingX={1}>
            <Text dimColor>{"─".repeat(28)}</Text>
          </Box>

          <WizardSection
            title="Databases"
            options={catalog.databases}
            recommendedId={catalog.recommended?.database}
            selected={selectedDatabase}
            loading={loading}
            onSelect={setSelectedDatabase}
          />
        </Box>
      )}

      {/* Action area */}
      {catalog && (
        <Box flexDirection="column" paddingX={1} marginTop={1}>
          <Box paddingX={1}>
            <Text dimColor>{"─".repeat(28)}</Text>
          </Box>

          {/* Recommended summary */}
          {catalog.recommended && (
          <Box paddingX={1} marginTop={1} flexDirection="column">
            <Text bold>Recommended Setup:</Text>
            <Box paddingLeft={2} flexDirection="column">
              <Text dimColor>
                Provider: {catalog.recommended.provider || "—"}
              </Text>
              <Text dimColor>
                Gateway: {catalog.recommended.gateway || "—"}
              </Text>
              <Text dimColor>
                TTS: {catalog.recommended.tts || "—"}
              </Text>
              <Text dimColor>
                Database: {catalog.recommended.database || "—"}
              </Text>
            </Box>
          </Box>
          )}

          {/* Use Recommended button */}
          <Box marginTop={1} paddingX={1}>
            <Box>
              <Text
                color={applying ? "yellow" : "green"}
                bold
                inverse
              >
                {applying ? " Applying... " : " Use Recommended "}
              </Text>
            </Box>
          </Box>

          {/* Apply result */}
          {applyResult && (
            <Box paddingX={1} marginTop={1}>
              <Text
                color={applyResult.startsWith("✓") ? "green" : "red"}
              >
                {applyResult}
              </Text>
            </Box>
          )}

          {/* Footer hint */}
          <Box paddingX={1} marginTop={1}>
            <Text dimColor>
              Press Enter on "Use Recommended" to apply
            </Text>
          </Box>
        </Box>
      )}
    </Box>
  );
}
