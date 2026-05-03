---
name: frontend-design
version: 1.0.0
description: Build distinctive, production-grade frontend interfaces with high design quality. Use when the user asks for web components, pages, or applications and the visual direction matters as much as the code. Avoids generic-template output.
author: ethos-team
category: design
tags: [frontend, design, ui, ux, css]
triggers: [design, "build a page", "build a landing", "build a component", ui, frontend]
enabled: true
---

# Frontend Design

For frontend work where the look matters. The default reflex of every LLM is to produce a bland centered-card layout with one accent color. Resist it.

## Anti-template policy

Banned by default — only ship if the user explicitly asks for them:

- Default card grid with uniform spacing
- Centered hero with gradient blob
- Unmodified shadcn/Tailwind defaults
- Flat layout with no depth
- Uniform radius/spacing/shadow across every component
- Safe gray-on-white with one accent color
- Sidebar-cards-charts dashboard

## Required qualities

Every meaningful surface should demonstrate at least four of:

1. Clear hierarchy through scale contrast
2. Intentional rhythm in spacing — not uniform padding
3. Depth via overlap, shadows, surfaces, or motion
4. Typography with character (real pairing strategy)
5. Color used semantically, not decoratively
6. Hover / focus / active states that feel designed
7. Grid-breaking editorial or bento composition
8. Texture, grain, or atmosphere when fitting
9. Motion that clarifies flow, not distracting
10. Data viz treated as part of the design system

## Before writing any code

1. **Pick a specific style direction.** Avoid "clean minimal".
   - Editorial / magazine
   - Neo-brutalism
   - Glassmorphism with real depth
   - Dark luxury / light luxury with disciplined contrast
   - Bento layouts
   - Scrollytelling
   - 3D integration
   - Swiss / International
   - Retro-futurism
2. **Define a palette intentionally.** 1–2 base, 1 accent, 1 destructive, plus a semantic warning/success.
3. **Choose typography deliberately.** Two families max; specify weights and exact use.
4. **Gather references.** At least three real examples of the direction you're going.

## Design tokens

Define as CSS custom properties up front. Never hardcode palette/spacing/radius repeatedly.

```css
:root {
  --color-surface: oklch(98% 0 0);
  --color-text: oklch(18% 0 0);
  --color-accent: oklch(68% 0.21 250);

  --text-base: clamp(1rem, 0.92rem + 0.4vw, 1.125rem);
  --text-hero: clamp(3rem, 1rem + 7vw, 8rem);

  --space-section: clamp(4rem, 3rem + 5vw, 10rem);

  --duration-fast: 150ms;
  --duration-normal: 300ms;
  --ease-out-expo: cubic-bezier(0.16, 1, 0.3, 1);
}
```

## Performance budget

| Page Type | JS (gzipped) | CSS |
|-----------|--------------|-----|
| Landing | < 150 kb | < 30 kb |
| App | < 300 kb | < 50 kb |
| Microsite | < 80 kb | < 15 kb |

CWV targets: LCP < 2.5s, INP < 200ms, CLS < 0.1.

## Animation discipline

Animate compositor-friendly properties only:
- `transform`, `opacity`, `clip-path`, `filter` (sparingly)

Avoid layout-bound:
- `width`, `height`, `top`, `left`, `margin`, `padding`, `border`, `font-size`

Use `will-change` narrowly; remove when done. Prefer CSS for simple transitions; `requestAnimationFrame` or established libs for JS motion. Never wire scroll handlers directly — IntersectionObserver instead.

## Semantic HTML

Default to `<header> <nav> <main> <section> <article> <aside> <footer>` over generic `<div>` stacks. Screen readers and crawlers reward you. CSS targeting is also cleaner.

## Component checklist

Before declaring a component done:

- [ ] Avoids looking like a default Tailwind / shadcn template
- [ ] Has intentional hover / focus / active states
- [ ] Uses hierarchy rather than uniform emphasis
- [ ] Would look believable in a real product screenshot
- [ ] Both light and dark themes intentional (if both exist)
- [ ] Keyboard accessible
- [ ] Reduced-motion respected

## Output format

Deliver:

1. The component / page code
2. The token / palette decisions inline as comments or a tokens file
3. A one-paragraph rationale for the style direction picked
4. A list of what was deliberately rejected (so the user can push back)
