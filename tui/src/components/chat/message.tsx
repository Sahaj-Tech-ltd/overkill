import React from "react";
import { Text, Box } from "ink";
import { ThinkingBlock } from "./thinking-block.tsx";
import { useTheme } from "../../lib/theme.ts";
import { highlight } from "../../lib/highlight.ts";

interface MessageProps {
  role: "user" | "assistant" | "system";
  content: string;
  streaming?: boolean;
  terminalWidth?: number;
  reasoning?: string;
  /** Duration of the reasoning phase in ms */
  reasoningDuration?: number;
  /** Duration of the entire turn in ms */
  turnDuration?: number;
}

interface TextSegment {
  type: "text" | "bold" | "code" | "codeblock";
  content: string;
}

function parseMarkdown(text: string): TextSegment[] {
  const segments: TextSegment[] = [];
  let remaining = text;

  while (remaining.length > 0) {
    const codeBlockStart = remaining.indexOf("```");
    const boldStart = remaining.indexOf("**");
    const inlineCodeStart = remaining.indexOf("`");

    const positions: { pos: number; type: string }[] = [];
    if (codeBlockStart !== -1)
      positions.push({ pos: codeBlockStart, type: "codeblock" });
    if (boldStart !== -1) positions.push({ pos: boldStart, type: "bold" });
    if (inlineCodeStart !== -1)
      positions.push({ pos: inlineCodeStart, type: "code" });

    if (positions.length === 0) {
      segments.push({ type: "text", content: remaining });
      break;
    }

    positions.sort((a, b) => a.pos - b.pos);
    const first = positions[0];

    if (first.pos > 0) {
      segments.push({ type: "text", content: remaining.slice(0, first.pos) });
      remaining = remaining.slice(first.pos);
    }

    if (first.type === "codeblock") {
      const endIdx = remaining.indexOf("```", 3);
      if (endIdx === -1) {
        segments.push({ type: "codeblock", content: remaining.slice(3) });
        break;
      }
      segments.push({ type: "codeblock", content: remaining.slice(3, endIdx) });
      remaining = remaining.slice(endIdx + 3);
    } else if (first.type === "bold") {
      const endIdx = remaining.indexOf("**", 2);
      if (endIdx === -1) {
        segments.push({ type: "text", content: remaining });
        break;
      }
      segments.push({ type: "bold", content: remaining.slice(2, endIdx) });
      remaining = remaining.slice(endIdx + 2);
    } else {
      const endIdx = remaining.indexOf("`", 1);
      if (endIdx === -1) {
        segments.push({ type: "text", content: remaining });
        break;
      }
      segments.push({ type: "code", content: remaining.slice(1, endIdx) });
      remaining = remaining.slice(endIdx + 1);
    }
  }

  return segments;
}

function renderSegment(
  segment: TextSegment,
  key: number,
  theme: ReturnType<typeof useTheme>,
  syntax?: import("../../themes/definitions.ts").SyntaxColors,
): React.JSX.Element {
  switch (segment.type) {
    case "bold":
      return (
        <Text key={key} bold color={theme.text}>
          {segment.content}
        </Text>
      );
    case "code":
      return (
        <Text key={key} backgroundColor={theme.inputBg} color={theme.accent}>
          {" "}
          {segment.content}{" "}
        </Text>
      );
    case "codeblock": {
      // Detect language from first line if it looks like a lang tag.
      const firstLine = segment.content.split("\n")[0] ?? "";
      const langMatch = firstLine.match(/^[a-zA-Z0-9_+#-]+$/);
      const lang = langMatch ? firstLine : undefined;
      const code = lang
        ? segment.content.slice(firstLine.length + 1)
        : segment.content;
      const highlightedLines = highlight(code, lang ?? "text", syntax);
      return (
        <Box
          key={key}
          flexDirection="column"
          borderStyle="round"
          borderColor={theme.border}
          paddingX={1}
        >
          {highlightedLines.length > 0 ? (
            highlightedLines.map((line, i) => (
              <Text key={i}>
                {line.length > 0 ? (
                  line.map((token, j) => (
                    <Text key={j} color={token.color}>
                      {token.text}
                    </Text>
                  ))
                ) : (
                  <Text> </Text>
                )}
              </Text>
            ))
          ) : (
            code.split("\n").map((line, i) => (
              <Text key={i} color={theme.accent}>
                {line}
              </Text>
            ))
          )}
        </Box>
      );
    }
    default:
      return <Text key={key} color={theme.text}>{segment.content}</Text>;
  }
}

function renderContent(
  text: string,
  theme: ReturnType<typeof useTheme>,
  syntax?: import("../../themes/definitions.ts").SyntaxColors,
): React.JSX.Element[] {
  const segments = parseMarkdown(text);
  return segments.map((seg, i) => renderSegment(seg, i, theme, syntax));
}

function wrapText(text: string, width: number): string[] {
  const lines: string[] = [];
  for (const line of text.split("\n")) {
    const runes = [...line];
    if (runes.length <= width) {
      lines.push(line);
    } else {
      for (let i = 0; i < runes.length; i += width) {
        lines.push(runes.slice(i, i + width).join(""));
      }
    }
  }
  return lines;
}

function formatDuration(ms: number): string {
  if (ms < 1000) return `${ms}ms`;
  if (ms < 60000) return `${(ms / 1000).toFixed(1)}s`;
  const mins = Math.floor(ms / 60000);
  const secs = ((ms % 60000) / 1000).toFixed(1);
  return `${mins}m ${secs}s`;
}

export function MessageBubble({
  role,
  content,
  streaming,
  terminalWidth,
  reasoning,
  reasoningDuration,
  turnDuration,
}: MessageProps): React.JSX.Element {
  const maxWidth = terminalWidth ? terminalWidth - 4 : 80;
  const theme = useTheme();

  if (role === "system") {
    return (
      <Box>
        <Text dimColor color="yellow">
          {content}
        </Text>
      </Box>
    );
  }

  if (role === "user") {
    const wrapped = wrapText(content, maxWidth);
    return (
      <Box flexDirection="column" alignItems="flex-end">
        {wrapped.map((line, i) => (
          <Text key={i} color="white">
            {line}
          </Text>
        ))}
      </Box>
    );
  }

  // Assistant
  const prefix = "Overkill > ";
  const contentWidth = maxWidth - prefix.length;
  const wrapped = wrapText(
    content,
    contentWidth > 20 ? contentWidth : maxWidth,
  );

  return (
    <Box flexDirection="column">
      {reasoning && (
        <ThinkingBlock
          reasoning={reasoning}
          duration={reasoningDuration}
          theme={theme}
        />
      )}
      {wrapped.map((line, i) => (
        <Box key={i}>
          {i === 0 ? (
            <>
              <Text color="cyan" bold>
                {prefix}
              </Text>
              {renderContent(line, theme, theme.syntax)}
            </>
          ) : (
            <>
              <Text>{" ".repeat(prefix.length)}</Text>
              {renderContent(line, theme, theme.syntax)}
            </>
          )}
        </Box>
      ))}
      {/* Turn completion time — shown when turn is done (not streaming) */}
      {!streaming && turnDuration != null && turnDuration > 0 && (
        <Box marginTop={1}>
          <Text color={theme.muted}>⏱ Turn completed in {formatDuration(turnDuration)}</Text>
        </Box>
      )}
      {streaming && (
        <Box>
          <Text>{" ".repeat(prefix.length)}</Text>
          <Text color="cyan">▊</Text>
        </Box>
      )}
    </Box>
  );
}
