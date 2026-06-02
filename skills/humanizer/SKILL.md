---
name: humanizer
version: 1.0.0
description: Remove signs of AI-generated writing from text. Use when editing or reviewing prose to make it sound natural and human-written. Covers inflated symbolism, promotional language, AI vocabulary, em-dash overuse, rule-of-three padding, and vague attributions.
author: overkill-team
category: writing
tags: [writing, editing, prose, style]
triggers: [humanize, "make this less ai", "sound more human", "remove ai voice", "edit this prose"]
enabled: true
---

# Humanizer

Edit text to remove the tells that mark it as machine-generated. Based on Wikipedia's "Signs of AI writing" guide and direct observation.

## When to use

- Editing prose written by an LLM (your own output included)
- Reviewing copy before publication
- When the user says "this sounds AI-generated" or "tone is off"

## The tells

### 1. Inflated symbolism

> "This represents a paradigm shift in how we conceptualize..."

Strip. Concrete claims only. If the thing matters, say what it does, not what it represents.

### 2. Promotional language

Words to flag and usually delete:

- *seamless, seamlessly, seamlessness*
- *robust, comprehensive, holistic*
- *cutting-edge, state-of-the-art, next-generation*
- *transformative, revolutionary, game-changing*
- *unparalleled, unmatched, world-class*
- *empower, unlock, supercharge*

If the actual claim survives without the adjective, keep just the claim.

### 3. -ing analyses (superficial)

> "Highlighting the importance of..., showcasing the potential to..., underscoring the need for..."

These are filler. They look analytical but assert nothing. Cut or replace with a concrete observation.

### 4. Vague attribution

> "Many experts believe..." / "It is widely accepted that..." / "Studies show..."

Either name the source or drop the appeal. "Many experts" without names is laundered confidence.

### 5. Em-dash overuse

LLMs love em-dashes — for emphasis, for asides, for lists. Real writers use them sparingly. If a paragraph has three em-dashes, two are wrong; pick one and reach for a comma, period, or parenthesis on the others.

### 6. Rule of three (padded)

> "It's faster, cheaper, and more reliable."
> "Innovative, scalable, and future-proof."

When the third item adds nothing, two items are stronger. When all three are vague adjectives, the sentence is decoration.

### 7. AI vocabulary

Common LLM signature words to interrogate:

*delve, navigate (the landscape of), tapestry, intricate, nuanced, multifaceted, crucial, pivotal, leverage, utilize, foster, harness, embark on, journey, realm, plethora, myriad*

None are wrong in isolation. All are overrepresented in machine prose. Use sparingly and only when precise.

### 8. Passive evasion

> "Mistakes were made." / "It is suggested that..."

Active voice with a subject is harder to hide behind. Prefer it.

### 9. Negative parallelisms

> "It's not just X, it's Y."
> "This isn't merely a tool, it's a..."

Once per essay is fine. Three times is a tic.

### 10. Filler phrases

Cut on sight:

- "It's worth noting that..."
- "It's important to remember..."
- "In today's fast-paced world..."
- "At its core, ..."
- "When it comes to ..."
- "In the realm of ..."

If what follows is worth saying, just say it.

## Process

1. Read the text once through.
2. Mark every instance of patterns 1–10 above.
3. For each: delete, replace with a concrete claim, or rewrite the sentence.
4. Read aloud. Anywhere you stumble on a word, that word is wrong.
5. If a paragraph still feels generic after edits, ask: what is the specific claim? If you can't name one, cut the paragraph.

## Output format

Return the edited text. If the user wants the diff, show:

```
BEFORE: <original passage>
AFTER:  <edited passage>
WHY:    <which tell, briefly>
```

Don't perform the editing on prose that is intentionally formal (legal, academic, regulatory) without checking with the user first.
