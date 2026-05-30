import React, { useState, useCallback } from "react";
import { Box, Text, useInput } from "ink";
import type { Key } from "ink";
import { TextInput } from "../text-input.tsx";
import { DialogContainer } from "./dialog-container.tsx";

interface SteerDialogProps {
  open: boolean;
  onClose: () => void;
  onSubmit: (message: string) => void;
}

/** Input handler that only mounts when the dialog is open. */
function SteerInputHandler({
  message,
  setMessage,
  onSubmit,
  onClose,
}: {
  message: string;
  setMessage: (s: string) => void;
  onSubmit: (message: string) => void;
  onClose: () => void;
}) {
  useInput((_input, key) => {
    if (key.escape) {
      onClose();
      return;
    }
    if (key.return) {
      const trimmed = message.trim();
      if (trimmed) {
        onSubmit(trimmed);
        setMessage("");
        onClose();
      }
    }
  });
  return null;
}

export function SteerDialog({
  open,
  onClose,
  onSubmit,
}: SteerDialogProps): React.JSX.Element | null {
  const [message, setMessage] = useState("");

  const handleChange = useCallback((val: string) => {
    setMessage(val);
  }, []);

  if (!open) return null;

  return (
    <DialogContainer open={open} onClose={onClose} title="Steer Agent">
      <SteerInputHandler
        message={message}
        setMessage={setMessage}
        onSubmit={onSubmit}
        onClose={onClose}
      />
      <Box marginBottom={1}>
        <Text color="gray">
          Send a guidance message to the running agent:
        </Text>
      </Box>
      <Box marginBottom={1}>
        <Text color="gray">{"> "}</Text>
        <TextInput
          value={message}
          onChange={handleChange}
          placeholder="Type steering message, press Enter to send..."
        />
      </Box>
      <Box>
        <Text dimColor>
          Press Enter to send · Esc to cancel
        </Text>
      </Box>
    </DialogContainer>
  );
}
