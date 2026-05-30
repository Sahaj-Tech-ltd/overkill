import { useState, useCallback } from "react";

export interface ClarifyRequest {
  question: string;
  choices: string[];
}

export type ClarifyCallback = ((answer: string, index: number) => void) | null;

export interface UseDialogsResult {
  openDialog: string | null;
  open: (name: string) => void;
  close: () => void;
  toggle: (name: string) => void;
  // Clarify dialog state — set by the gateway when agent asks a question.
  clarifyRequest: ClarifyRequest | null;
  clarifyOpen: boolean;
  clarifyCallback: ClarifyCallback;
  showClarify: (req: ClarifyRequest, cb: (answer: string, index: number) => void) => void;
  dismissClarify: () => void;
}

export function useDialogs(): UseDialogsResult {
  const [openDialog, setOpenDialog] = useState<string | null>(null);
  const [clarifyRequest, setClarifyRequest] = useState<ClarifyRequest | null>(null);
  const [clarifyOpen, setClarifyOpen] = useState(false);
  const [clarifyCallback, setClarifyCallback] = useState<ClarifyCallback>(null);

  const open = useCallback((name: string) => {
    setOpenDialog(name);
  }, []);

  const close = useCallback(() => {
    setOpenDialog(null);
  }, []);

  const toggle = useCallback((name: string) => {
    setOpenDialog((prev) => (prev === name ? null : name));
  }, []);

  const showClarify = useCallback(
    (req: ClarifyRequest, cb: (answer: string, index: number) => void) => {
      setClarifyRequest(req);
      setClarifyCallback(() => cb);
      setClarifyOpen(true);
    },
    [],
  );

  const dismissClarify = useCallback(() => {
    setClarifyOpen(false);
    setClarifyRequest(null);
  }, []);

  return {
    openDialog,
    open,
    close,
    toggle,
    clarifyRequest,
    clarifyOpen,
    clarifyCallback,
    showClarify,
    dismissClarify,
  };
}
