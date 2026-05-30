import React, { useState, useCallback, useRef, useEffect } from "react";
import { Box, Text, useInput } from "ink";
import { DialogContainer } from "./dialog-container.tsx";

interface ClarifyChoice {
  text: string;
  index: number;
}

interface ClarifyRequest {
  question: string;
  choices: string[];
}

interface ClarifyDialogProps {
  open: boolean;
  request: ClarifyRequest | null;
  onAnswer: (answer: string, index: number) => void;
  onCancel: () => void;
}

/** Input handler that only mounts when the dialog is open. */
function ClarifyInputHandler({
  open,
  request,
  onAnswer,
  onCancel,
  selected,
  setSelected,
  customText,
  setCustomText,
  typingCustom,
  setTypingCustom,
  submitChoice,
  submitCustom,
}: {
  open: boolean;
  request: ClarifyRequest | null;
  onAnswer: (answer: string, index: number) => void;
  onCancel: () => void;
  selected: number;
  setSelected: (n: number) => void;
  customText: string;
  setCustomText: React.Dispatch<React.SetStateAction<string>>;
  typingCustom: boolean;
  setTypingCustom: (b: boolean) => void;
  submitChoice: (index: number) => void;
  submitCustom: () => void;
}) {
  const choices = request?.choices ?? [];
  useInput((input, key) => {
    if (key.escape) {
      if (typingCustom && choices.length > 0) {
        setTypingCustom(false);
        return;
      }
      onCancel();
      return;
    }

    if (key.ctrl && input === "c") {
      onCancel();
      return;
    }

    if (typingCustom || choices.length === 0) {
      if (key.return) {
        submitCustom();
        return;
      }
      if (key.backspace || key.delete) {
        setCustomText((prev) => prev.slice(0, -1));
        return;
      }
      if (input && input.length === 1 && !key.ctrl && !key.meta) {
        setCustomText((prev) => prev + input);
      }
      return;
    }

    if (key.upArrow) {
      setSelected(Math.max(0, selected - 1));
      return;
    }
    if (key.downArrow) {
      setSelected(Math.min(choices.length, selected + 1));
      return;
    }
    if (key.return) {
      if (selected === choices.length) {
        setTypingCustom(true);
      } else {
        submitChoice(selected);
      }
      return;
    }

    const n = parseInt(input, 10);
    if (n >= 1 && n <= choices.length) {
      submitChoice(n - 1);
      return;
    }
  });
  return null;
}

export function ClarifyDialog({
  open,
  request,
  onAnswer,
  onCancel,
}: ClarifyDialogProps): React.JSX.Element | null {
  const [selected, setSelected] = useState(0);
  const [customText, setCustomText] = useState("");
  const [typingCustom, setTypingCustom] = useState(false);
  const selectedRef = useRef(selected);
  selectedRef.current = selected;

  const choices = request?.choices ?? [];
  const hasChoices = choices.length > 0;

  // Reset state when dialog opens with a new request.
  useEffect(() => {
    if (open) {
      setSelected(0);
      setCustomText("");
      setTypingCustom(false);
    }
  }, [open, request]);

  const submitChoice = useCallback(
    (index: number) => {
      if (index >= 0 && index < choices.length) {
        onAnswer(choices[index], index);
      }
    },
    [choices, onAnswer],
  );

  const submitCustom = useCallback(() => {
    if (customText.trim()) {
      onAnswer(customText.trim(), -1);
    }
  }, [customText, onAnswer]);

  if (!open || !request) return null;

  const renderChoices = hasChoices && !typingCustom;
  const labelWidth = request.question ? Math.min(request.question.length + 12, 50) : 30;

  return (
    <DialogContainer
      open={open}
      onClose={onCancel}
      title={`Agent asks…`}
    >
      <ClarifyInputHandler
        open={open}
        request={request}
        onAnswer={onAnswer}
        onCancel={onCancel}
        selected={selected}
        setSelected={setSelected}
        customText={customText}
        setCustomText={setCustomText}
        typingCustom={typingCustom}
        setTypingCustom={setTypingCustom}
        submitChoice={submitChoice}
        submitCustom={submitCustom}
      />
      <Box flexDirection="column" paddingX={1} paddingY={1} width={labelWidth}>
        {/* Question */}
        <Box marginBottom={1}>
          <Text color="yellow" bold>
            🤔 {request.question}
          </Text>
        </Box>

        {/* Divider */}
        <Box marginBottom={1}>
          <Text dimColor>{"─".repeat(Math.min(labelWidth - 2, 60))}</Text>
        </Box>

        {/* Choices */}
        {renderChoices && (
          <Box flexDirection="column">
            {choices.map((choice, i) => {
              const isSel = selected === i;
              return (
                <Box key={i}>
                  <Text color={isSel ? "cyan" : undefined} bold={isSel}>
                    {isSel ? "▸ " : "  "}
                    {i + 1}. {choice}
                  </Text>
                </Box>
              );
            })}
            {/* "Other" option */}
            <Box marginTop={1}>
              <Text
                color={selected === choices.length ? "cyan" : "grey"}
                bold={selected === choices.length}
              >
                {selected === choices.length ? "▸ " : "  "}
                {choices.length + 1}. Other (type your answer)
              </Text>
            </Box>
          </Box>
        )}

        {/* Free-text input */}
        {(typingCustom || !hasChoices) && (
          <Box flexDirection="column">
            <Box>
              <Text color="cyan" bold>
                {"❯ "}
              </Text>
              <Text>{customText}</Text>
              <Text dimColor>│</Text>
            </Box>
            {hasChoices && (
              <Box marginTop={1}>
                <Text dimColor>Esc to go back · Enter to submit</Text>
              </Box>
            )}
          </Box>
        )}

        {/* Help text */}
        <Box marginTop={1}>
          <Text dimColor>
            {renderChoices
              ? `↑/↓ select · Enter confirm · 1-${choices.length} quick pick · Esc cancel`
              : "Type your answer · Enter submit · Esc cancel"}
          </Text>
        </Box>
      </Box>
    </DialogContainer>
  );
}
