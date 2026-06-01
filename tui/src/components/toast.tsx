import React from "react";
import { Box, Text } from "ink";
import { type Toast, VARIANT_COLORS } from "../hooks/use-toast.ts";

interface ToastContainerProps {
  toasts: Toast[];
}

export function ToastContainer({
  toasts,
}: ToastContainerProps): React.JSX.Element | null {
  if (toasts.length === 0) return null;

  return (
    <Box
      flexDirection="column"
      position="absolute"
      marginTop={1}
      marginRight={2}
    >
      {toasts.map((toast) => (
        <Box key={toast.id} marginBottom={0}>
          <Text color={VARIANT_COLORS[toast.variant]}>▐</Text>
          <Text> {toast.message} </Text>
        </Box>
      ))}
    </Box>
  );
}
