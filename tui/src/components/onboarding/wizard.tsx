import React, { useCallback } from "react";
import { Box, Text } from "ink";
import type { BackendClient } from "../../backend/client.ts";
import { useOnboarding } from "../../hooks/use-onboarding.ts";
import { StepWelcome } from "./step-welcome.tsx";
import { StepProvider } from "./step-provider.tsx";
import { StepModel } from "./step-model.tsx";
import { StepTTS } from "./step-tts.tsx";
import { StepGateway } from "./step-gateway.tsx";

interface WizardProps {
  backend: BackendClient;
  onComplete: () => void;
}

const STEP_TITLES: Record<string, string> = {
  welcome: "Welcome",
  provider: "Choose Providers",
  model: "Choose Models",
  tts: "Configure TTS",
  gateway: "Connect Gateways",
  done: "Done",
};

function ProgressBar({
  current,
  total,
  label,
}: {
  current: number;
  total: number;
  label: string;
}): React.JSX.Element {
  const width = 30;
  const filled = Math.floor(((current + 1) / total) * width);
  const empty = width - filled;

  return (
    <Box marginBottom={1} flexDirection="column">
      <Box>
        <Text color="cyan">Progress: </Text>
        <Text color="green">{"█".repeat(filled)}</Text>
        <Text dimColor>{"░".repeat(empty)}</Text>
        <Text dimColor>
          {" "}
          {current + 1}/{total}
        </Text>
      </Box>
      <Text dimColor>
        Step {current + 1}/{total} — {label}
      </Text>
    </Box>
  );
}

export function Wizard({
  backend,
  onComplete,
}: WizardProps): React.JSX.Element {
  const onboarding = useOnboarding();

  const handleSaved = useCallback(
    async () => {
      const success = await onboarding.saveConfig(backend);
      if (success) {
        onComplete();
      }
    },
    [onboarding, backend, onComplete],
  );

  const renderStep = (): React.JSX.Element => {
    switch (onboarding.step) {
      case "welcome":
        return <StepWelcome onNext={onboarding.nextStep} />;
      case "provider":
        return (
          <StepProvider
            providers={onboarding.providers}
            setProviders={onboarding.setProviders}
            onNext={onboarding.nextStep}
            onBack={onboarding.prevStep}
          />
        );
      case "model":
        return (
          <StepModel
            providers={onboarding.providers}
            defaultModel={onboarding.defaultModel}
            setDefaultModel={onboarding.setDefaultModel}
            onNext={onboarding.nextStep}
            onBack={onboarding.prevStep}
          />
        );
      case "tts":
        return (
          <StepTTS
            tts={onboarding.tts}
            setTTS={onboarding.setTTS}
            onNext={onboarding.nextStep}
            onBack={onboarding.prevStep}
          />
        );
      case "gateway":
        return (
          <StepGateway
            backend={backend}
            gateway={onboarding.gateway}
            setGateway={onboarding.setGateway}
            onNext={handleSaved}
            onBack={onboarding.prevStep}
            saving={onboarding.saving}
            error={onboarding.error}
          />
        );
      case "done":
        return (
          <Box flexDirection="column" alignItems="center" justifyContent="center" height="100%">
            <Text color="green" bold>✓ Configuration saved!</Text>
            <Box marginTop={1}>
              <Text dimColor>Launching Overkill...</Text>
            </Box>
          </Box>
        );
    }
  };

  return (
    <Box
      flexDirection="column"
      padding={2}
      width="100%"
      height="100%"
    >
      {/* Header */}
      <Box flexDirection="column" marginBottom={1}>
        <Box>
          <Text bold color="cyan">
            ⚡ Overkill Setup
          </Text>
          <Text dimColor> — {STEP_TITLES[onboarding.step] ?? "..."}</Text>
        </Box>
        <ProgressBar
          current={onboarding.stepIndex}
          total={onboarding.totalSteps}
          label={STEP_TITLES[onboarding.step] ?? "..."}
        />
      </Box>

      {/* Step content */}
      <Box flexDirection="column" flexGrow={1}>
        {renderStep()}
      </Box>
    </Box>
  );
}
