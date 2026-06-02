/**
 * Memo the Elephant — context-aware status phrases.
 *
 * Memo is Overkill's mascot: two Postgres elephants that never forget.
 * Strong, mighty, even feared by tigers (the frontier models).
 *
 * Phrases are regex-matched against the user's input and the current
 * agent action to make the thinking indicator feel alive and contextual.
 *
 * The agent's self-improvement loop can add new phrases via the
 * `memo.learn` RPC, with persistence to the learning store.
 */

import type { BackendClient } from "../backend/client.ts";
import type { MemoPhraseResult as BackendMemoResult } from "../backend/types.ts";

export interface PhraseRule {
  /** Regex patterns to match against user input */
  patterns: RegExp[];
  /** Status phrases for this context */
  phrases: string[];
}

export interface MemoPhraseResult {
  phrase: string;
  category: string;
}

/**
 * Base phrase rules — ordered by priority. First matching rule wins.
 * Add new rules via self-improvement loop (appended to end).
 */
export const BASE_PHRASE_RULES: PhraseRule[] = [
  // ── research / papers ──
  {
    patterns: [
      /research/i,
      /paper/i,
      /arxiv/i,
      /study/i,
      /synthesize/i,
      /literature/i,
      /academic/i,
    ],
    phrases: [
      "Synthesizing papers in the trunk...",
      "Cross-referencing research...",
      "Never forgetting a citation...",
      "Two elephants, one literature review...",
      "Trunk-deep in the archives...",
    ],
  },

  // ── debugging / fixes ──
  {
    patterns: [
      /bug/i,
      /fix/i,
      /error/i,
      /broke/i,
      /debug/i,
      /crash/i,
      /fail/i,
      /trace/i,
    ],
    phrases: [
      "Sniffing out the bug like a truffle pig...",
      "Tusks deployed for debugging...",
      "Charging at the error with full force...",
      "Two elephants can squash any bug...",
      "Tracking the stack trace with elephant precision...",
    ],
  },

  // ── coding / building ──
  {
    patterns: [
      /code/i,
      /implement/i,
      /build/i,
      /create/i,
      /write/i,
      /refactor/i,
      /migrate/i,
      /add/i,
      /feature/i,
    ],
    phrases: [
      "Trunk-deep in the codebase...",
      "Stampeding through files...",
      "Writing code that elephants would approve...",
      "Building something mighty...",
      "Crafting postgres-worthy code...",
    ],
  },

  // ── deploy / ship ──
  {
    patterns: [/deploy/i, /push/i, /ship/i, /release/i, /publish/i, /launch/i],
    phrases: [
      "Charging toward deployment...",
      "Summoning the build gods...",
      "Shipping with elephant-sized confidence...",
      "Two elephants pushing to prod...",
      "The frontier is scared of this deploy...",
    ],
  },

  // ── memory / context ──
  {
    patterns: [
      /memory/i,
      /remember/i,
      /recall/i,
      /context/i,
      /history/i,
      /session/i,
      /past/i,
    ],
    phrases: [
      "Never forgetting anything...",
      "Consolidating memories...",
      "Two elephants, zero forgotten context...",
      "Recalling everything you've ever said...",
      "The memory trunk never empties...",
    ],
  },

  // ── review / audit ──
  {
    patterns: [/review/i, /audit/i, /check/i, /inspect/i, /analyze/i, /scan/i],
    phrases: [
      "Inspecting with elephant eyes...",
      "Auditing the savanna...",
      "Two elephants reviewing your work...",
      "Nothing escapes an elephant audit...",
      "Scanning every file like a trunk sweeps the ground...",
    ],
  },

  // ── planning / thinking ──
  {
    patterns: [
      /plan/i,
      /think/i,
      /design/i,
      /architect/i,
      /spec/i,
      /proposal/i,
    ],
    phrases: [
      "Planning with elephant foresight...",
      "Designing something elephants will remember...",
      "Architecting a savanna of ideas...",
      "Two elephants strategizing...",
    ],
  },

  // ── test / verify ──
  {
    patterns: [/test/i, /verify/i, /validate/i, /assert/i, /prove/i],
    phrases: [
      "Testing with elephant thoroughness...",
      "Two elephants verifying every edge case...",
      "Validating like an elephant never forgets a bug...",
      "Proving correctness, trunk-first...",
    ],
  },
];

/** Default phrases when no pattern matches */
const DEFAULT_PHRASES = [
  "Remembering everything...",
  "Processing with postgres-grade memory...",
  "Two elephants, zero forgetfulness...",
  "The trunk is thinking...",
  "Mighty thoughts in progress...",
];

/**
 * Match user input against phrase rules.
 * Returns a contextual phrase if matched, or a random default.
 */
export function matchMemoPhrase(userInput: string): MemoPhraseResult {
  for (const rule of BASE_PHRASE_RULES) {
    for (const pattern of rule.patterns) {
      if (pattern.test(userInput)) {
        return {
          phrase: pickRandom(rule.phrases),
          category: "contextual",
        };
      }
    }
  }
  return {
    phrase: pickRandom(DEFAULT_PHRASES),
    category: "default",
  };
}

function pickRandom(arr: string[]): string {
  return arr[Math.floor(Math.random() * arr.length)];
}

/**
 * Get a phrase for a specific agent action (tool call status).
 * These fire mid-turn when the agent is using tools.
 */
export function getActionPhrase(action: string): string {
  const actionPhrases: Record<string, string[]> = {
    web_search: [
      "Trunk-extending into the web...",
      "Searching the savanna of the internet...",
      "Two elephants Googling...",
    ],
    read_file: [
      "Reading files with elephant attention...",
      "Scanning the code like an elephant scans the horizon...",
      "Never skimming — reading every line...",
    ],
    write_file: [
      "Writing with the precision of an elephant's trunk...",
      "Creating files that will be remembered forever...",
    ],
    terminal: [
      "Running commands with elephant power...",
      "Shell access granted. Tusks activated...",
    ],
    patch: [
      "Patching with surgical trunk precision...",
      "Editing like only an elephant can...",
    ],
    delegate_task: [
      "Sending out the elephant herd...",
      "Delegating across the savanna...",
      "Multiple elephants working in parallel...",
    ],
    memory: [
      "Committing to the eternal memory trunk...",
      "Two elephants storing this forever...",
    ],
  };

  const phrases = actionPhrases[action];
  if (phrases) return pickRandom(phrases);
  return pickRandom([
    "Working with elephant diligence...",
    "The trunk never stops...",
    "Mighty work in progress...",
  ]);
}

// Re-export for direct use

let _memoClient: BackendClient | null = null;

/** Set the backend client for server-side memo phrase resolution. */
export function setMemoClient(client: BackendClient | null): void {
  _memoClient = client;
}

/**
 * Match user input against phrase rules, trying the backend RPC first
 * and falling back to local hardcoded phrases.
 */
export async function matchMemoPhraseAsync(
  userInput: string,
): Promise<MemoPhraseResult> {
  if (_memoClient) {
    try {
      const result = await _memoClient.memoPhrase(userInput);
      if (result?.phrase)
        return { phrase: result.phrase, category: result.category || "server" };
    } catch {
      // Fall through to local phrases on RPC failure.
    }
  }
  return matchMemoPhrase(userInput);
}

/**
 * Learn new patterns/phrases via the backend RPC, falling back to a no-op
 * when the client is unavailable.
 */
export async function memoLearnAsync(
  patterns: string[],
  phrases: string[],
  category: string,
): Promise<void> {
  if (_memoClient) {
    try {
      await _memoClient.memoLearn(patterns, phrases, category);
    } catch {
      // Best-effort — local phrases still work.
    }
  }
}

// Re-export for direct use
export { DEFAULT_PHRASES };
