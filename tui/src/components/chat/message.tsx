import React from "react";
import { Text, Box } from "ink";
import { ThinkingBlock } from "./thinking-block.tsx";

interface MessageProps {
  role: "user" | "assistant" | "system";
  content: string;
  streaming?: boolean;
  terminalWidth?: number;
  reasoning?: string;
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

function renderSegment(segment: TextSegment, key: number): React.JSX.Element {
  switch (segment.type) {
    case "bold":
      return (
        <Text key={key} bold>
          {segment.content}
        </Text>
      );
    case "code":
      return (
        <Text key={key} backgroundColor="gray" color="white">
          {" "}
          {segment.content}{" "}
        </Text>
      );
    case "codeblock":
      return (
        <Box
          key={key}
          flexDirection="column"
          borderStyle="round"
          borderColor="gray"
          paddingX={1}
        >
          {segment.content.split("\n").map((line, i) => (
            <Text key={i} color="green">
              {line}
            </Text>
          ))}
        </Box>
      );
    default:
      return <Text key={key}>{segment.content}</Text>;
  }
}

function renderContent(text: string): React.JSX.Element[] {
  const segments = parseMarkdown(text);
  return segments.map((seg, i) => renderSegment(seg, i));
}

function wrapText(text: string, width: number): string[] {
  const lines: string[] = [];
  for (const line of text.split("\n")) {
    if (line.length <= width) {
      lines.push(line);
    } else {
      for (let i = 0; i < line.length; i += width) {
        lines.push(line.slice(i, i + width));
      }
    }
  }
  return lines;
}

export function MessageBubble({
  role,
  content,
  streaming,
  terminalWidth,
  reasoning,
}: MessageProps): React.JSX.Element {
  const maxWidth = terminalWidth ? terminalWidth - 4 : 80;

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
      {reasoning && <ThinkingBlock reasoning={reasoning} />}
      {wrapped.map((line, i) => (
        <Box key={i}>
          {i === 0 ? (
            <>
              <Text color="cyan" bold>
                {prefix}
              </Text>
              {renderContent(line)}
            </>
          ) : (
            <>
              <Text>{" ".repeat(prefix.length)}</Text>
              {renderContent(line)}
            </>
          )}
        </Box>
      ))}
      {streaming && (
        <Box>
          <Text>{" ".repeat(prefix.length)}</Text>
          <Text color="cyan">▊</Text>
        </Box>
      )}
    </Box>
  );
}
