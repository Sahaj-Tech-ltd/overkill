import { useState, useCallback } from "react";
import type { BackendClient } from "../backend/client.ts";
import type {
  OnboardingConfig,
  OnboardingProviderConfig,
  OnboardingTTSConfig,
  OnboardingGatewayConfig,
} from "../backend/types.ts";

export type WizardStep =
  | "welcome"
  | "provider"
  | "model"
  | "tts"
  | "gateway"
  | "done";

interface UseOnboardingResult {
  step: WizardStep;
  totalSteps: number;
  stepIndex: number;
  providers: OnboardingProviderConfig[];
  defaultModel: string;
  tts: OnboardingTTSConfig | null;
  gateway: OnboardingGatewayConfig | null;
  saving: boolean;
  error: string | null;
  setProviders: (providers: OnboardingProviderConfig[]) => void;
  setDefaultModel: (model: string) => void;
  setTTS: (config: OnboardingTTSConfig | null) => void;
  setGateway: (config: OnboardingGatewayConfig | null) => void;
  nextStep: () => void;
  prevStep: () => void;
  goToStep: (step: WizardStep) => void;
  saveConfig: (backend: BackendClient) => Promise<boolean>;
}

const STEP_ORDER: WizardStep[] = [
  "welcome",
  "provider",
  "model",
  "tts",
  "gateway",
  "done",
];

export function useOnboarding(): UseOnboardingResult {
  const [step, setStep] = useState<WizardStep>("welcome");
  const [providers, setProviders] = useState<OnboardingProviderConfig[]>([]);
  const [defaultModel, setDefaultModel] = useState("");
  const [tts, setTTS] = useState<OnboardingTTSConfig | null>(null);
  const [gateway, setGateway] = useState<OnboardingGatewayConfig | null>(null);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const stepIndex = STEP_ORDER.indexOf(step);

  const nextStep = useCallback(() => {
    const idx = STEP_ORDER.indexOf(step);
    if (idx < STEP_ORDER.length - 1) {
      setStep(STEP_ORDER[idx + 1]);
    }
  }, [step]);

  const prevStep = useCallback(() => {
    const idx = STEP_ORDER.indexOf(step);
    if (idx > 0) {
      setStep(STEP_ORDER[idx - 1]);
    }
  }, [step]);

  const goToStep = useCallback((target: WizardStep) => {
    setStep(target);
  }, []);

  const saveConfig = useCallback(
    async (backend: BackendClient): Promise<boolean> => {
      setSaving(true);
      setError(null);

      const config: OnboardingConfig = {
        providers,
        defaultModel,
        tts: tts ?? undefined,
        gateway: gateway ?? undefined,
      };

      try {
        await backend.call("config.create", config);
        return true;
      } catch (err) {
        setError((err as Error).message);
        return false;
      } finally {
        setSaving(false);
      }
    },
    [providers, defaultModel, tts, gateway],
  );

  return {
    step,
    totalSteps: STEP_ORDER.length,
    stepIndex,
    providers,
    defaultModel,
    tts,
    gateway,
    saving,
    error,
    setProviders,
    setDefaultModel,
    setTTS,
    setGateway,
    nextStep,
    prevStep,
    goToStep,
    saveConfig,
  };
}
