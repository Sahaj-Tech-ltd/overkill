// Syntax highlighting for code blocks in the TUI.
// Lightweight tokenizer — no external dependencies needed.
// Outputs arrays of { text, color } tokens compatible with Ink <Text>.
// Theme-aware: colors come from the active theme's syntax palette.

import type { SyntaxColors } from "../themes/definitions.ts";

export interface HighlightToken {
  text: string;
  color: string;
}

type TokenKind =
  | "keyword"
  | "string"
  | "comment"
  | "number"
  | "type"
  | "function"
  | "operator"
  | "text";

// Default colors used when no theme syntax is available.
const DEFAULT_SYNTAX: SyntaxColors = {
  keyword: "#cba6f7",
  string: "#a6e3a1",
  comment: "#6c7086",
  number: "#fab387",
  type: "#89b4fa",
  function: "#89dceb",
  operator: "#f38ba8",
  text: "#cdd6f4",
};

// Shared keyword sets — extend as needed.
const KEYWORDS = new Set([
  // General
  "if", "else", "for", "while", "do", "switch", "case", "break",
  "continue", "return", "throw", "try", "catch", "finally", "new",
  "delete", "typeof", "instanceof", "in", "of", "void", "yield",
  "async", "await", "class", "extends", "super", "import", "export",
  "default", "from", "as", "static", "get", "set",
  // Go-specific
  "func", "var", "const", "type", "package", "go", "defer", "select",
  "chan", "map", "struct", "interface", "range", "fallthrough",
  "nil", "true", "false",
  // Rust-specific
  "fn", "let", "mut", "pub", "impl", "trait", "enum", "match",
  "where", "use", "mod", "crate", "self", "Self", "unsafe", "dyn",
  "ref", "move", "macro_rules!",
  // Python-specific
  "def", "elif", "pass", "lambda", "with", "raise", "except",
  "finally", "is", "not", "and", "or", "None", "True", "False",
  "print", "global", "nonlocal", "assert",
  // TypeScript-specific
  "interface", "readonly", "abstract", "implements",
]);

const BUILTIN_TYPES = new Set([
  "string", "number", "boolean", "void", "any", "never", "unknown",
  "int", "int8", "int16", "int32", "int64",
  "uint", "uint8", "uint16", "uint32", "uint64",
  "float32", "float64", "byte", "rune", "error",
  "bool", "String", "Number", "Boolean", "Array", "Object",
  "Map", "Set", "Promise", "Date", "RegExp",
  "usize", "isize", "u8", "u16", "u32", "u64",
  "i8", "i16", "i32", "i64", "f32", "f64",
  "str", "char",
]);

const OPERATORS = new Set([
  "=", "+", "-", "*", "/", "%", "**", "//",
  "+=", "-=", "*=", "/=", "%=", "**=", "//=",
  "&=", "|=", "^=", "<<=", ">>=",
  "==", "===", "!=", "!==", "<", ">", "<=", ">=",
  "&&", "||", "!", "&", "|", "^", "~",
  "<<", ">>", "->", "=>", "::", ".",
  "?", ":", "??", "?.", "??=",
]);

const STRING_DELIMITERS = new Set(['"', "'", "`"]);

// ─── Theme-aware color mapping ─────────────────────────────────────────────

let currentSyntax: SyntaxColors = DEFAULT_SYNTAX;

export function setSyntaxColors(syntax: SyntaxColors): void {
  currentSyntax = syntax;
}

function colorFor(kind: TokenKind): string {
  return currentSyntax[kind] ?? DEFAULT_SYNTAX[kind] ?? "white";
}

// ─── Tokenizer ─────────────────────────────────────────────────────────────

function tokenizeLine(line: string, lang: string): HighlightToken[] {
  const tokens: HighlightToken[] = [];
  let i = 0;

  while (i < line.length) {
    const rest = line.slice(i);

    // Whitespace
    if (/^\s/.exec(rest)) {
      const m = /^\s+/.exec(rest)!;
      tokens.push({ text: m[0], color: colorFor("text") });
      i += m[0].length;
      continue;
    }

    // Single-line comment (// or # or --)
    if ((lang !== "python" && /^\/\//.exec(rest)) ||
        (lang === "python" && /^#/.exec(rest)) ||
        (["sql", "lua"].includes(lang) && /^--/.exec(rest))) {
      tokens.push({ text: line.slice(i), color: colorFor("comment") });
      break;
    }

    // Strings
    if (STRING_DELIMITERS.has(rest[0]!)) {
      const delim = rest[0]!;
      let j = 1;
      while (j < rest.length) {
        if (rest[j] === "\\") { j += 2; continue; }
        if (rest[j] === delim) { j++; break; }
        j++;
      }
      tokens.push({ text: rest.slice(0, j), color: colorFor("string") });
      i += j;
      continue;
    }

    // Numbers
    const numMatch = /^0x[0-9a-fA-F]+|^0b[01]+|^0o[0-7]+|^\d+\.?\d*(?:[eE][+-]?\d+)?/.exec(rest);
    if (numMatch) {
      tokens.push({ text: numMatch[0], color: colorFor("number") });
      i += numMatch[0].length;
      continue;
    }

    // Multi-char operators
    let opFound = false;
    for (const op of [...OPERATORS].sort((a, b) => b.length - a.length)) {
      if (rest.startsWith(op)) {
        tokens.push({ text: op, color: colorFor("operator") });
        i += op.length;
        opFound = true;
        break;
      }
    }
    if (opFound) continue;

    // Identifiers and keywords
    const idMatch = /^[a-zA-Z_$][a-zA-Z0-9_$]*/.exec(rest);
    if (idMatch) {
      const word = idMatch[0];
      if (KEYWORDS.has(word)) {
        tokens.push({ text: word, color: colorFor("keyword") });
      } else if (BUILTIN_TYPES.has(word)) {
        tokens.push({ text: word, color: colorFor("type") });
      } else if (/^[A-Z]/.test(word)) {
        tokens.push({ text: word, color: colorFor("type") });
      } else if (i + word.length < line.length && line[i + word.length] === "(") {
        tokens.push({ text: word, color: colorFor("function") });
      } else {
        tokens.push({ text: word, color: colorFor("text") });
      }
      i += word.length;
      continue;
    }

    // Fallthrough — single char
    tokens.push({ text: rest[0]!, color: colorFor("text") });
    i++;
  }

  return tokens;
}

// ─── Public API ────────────────────────────────────────────────────────────

export function highlight(
  code: string,
  lang: string,
  syntax?: SyntaxColors,
): HighlightToken[][] {
  if (syntax) {
    setSyntaxColors(syntax);
  }

  const lines = code.split("\n");
  return lines.map((line) => tokenizeLine(line, lang));
}
