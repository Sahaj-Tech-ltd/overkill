import { useState, useCallback } from "react";

export interface UseDialogsResult {
  openDialog: string | null;
  open: (name: string) => void;
  close: () => void;
  toggle: (name: string) => void;
}

export function useDialogs(): UseDialogsResult {
  const [openDialog, setOpenDialog] = useState<string | null>(null);

  const open = useCallback((name: string) => {
    setOpenDialog(name);
  }, []);

  const close = useCallback(() => {
    setOpenDialog(null);
  }, []);

  const toggle = useCallback((name: string) => {
    setOpenDialog((prev) => (prev === name ? null : name));
  }, []);

  return { openDialog, open, close, toggle };
}
