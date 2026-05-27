import { useState, useCallback, useRef } from "react";

export type ToastVariant = "info" | "success" | "warning" | "error";

export interface Toast {
  id: string;
  message: string;
  variant: ToastVariant;
  createdAt: number;
}

export interface UseToastResult {
  toasts: Toast[];
  show: (message: string, variant?: ToastVariant, duration?: number) => void;
  dismiss: (id: string) => void;
}

const VARIANT_COLORS: Record<ToastVariant, string> = {
  info: "cyan",
  success: "green",
  warning: "yellow",
  error: "red",
};

export { VARIANT_COLORS };

let nextId = 0;

export function useToast(): UseToastResult {
  const [toasts, setToasts] = useState<Toast[]>([]);
  const timersRef = useRef<Map<string, ReturnType<typeof setTimeout>>>(
    new Map(),
  );

  const dismiss = useCallback((id: string) => {
    const timer = timersRef.current.get(id);
    if (timer) {
      clearTimeout(timer);
      timersRef.current.delete(id);
    }
    setToasts((prev) => prev.filter((t) => t.id !== id));
  }, []);

  const show = useCallback(
    (
      message: string,
      variant: ToastVariant = "info",
      duration: number = 3000,
    ) => {
      const id = `toast-${nextId++}`;
      const toast: Toast = { id, message, variant, createdAt: Date.now() };

      setToasts((prev) => [...prev, toast]);

      const timer = setTimeout(() => {
        dismiss(id);
      }, duration);
      timersRef.current.set(id, timer);
    },
    [dismiss],
  );

  return { toasts, show, dismiss };
}
