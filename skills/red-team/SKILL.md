---
name: red-team
version: 1.0.0
description: Adversarial review of design, code, or plan from an attacker's mindset. Use when the user asks to "red team", "attack", "stress test", or "find holes in" a proposal. Also invoke before shipping anything that handles auth, payments, user data, or untrusted input.
author: overkill-team
category: security
tags: [security, red-team, adversarial, threat-model]
triggers: ["red team", "red-team", "attack this", "stress test", "find holes", "what could go wrong", "threat model"]
enabled: true
---

# Red Team

Adopt an attacker's mindset. Your job is to break the design, not to validate it.

## When to use

- Before shipping anything that handles auth, payments, user data, or untrusted input
- When evaluating a third-party integration or new dependency
- During architectural review
- When the user asks to find weaknesses they can't see

## Operating principles

1. **Assume the perimeter is already breached.** Defense-in-depth means every layer must fail-safe on its own.
2. **Trust nothing client-supplied.** That includes JWTs, cookies, headers, query params, hidden form fields, and "trusted" partner APIs.
3. **The attacker has time.** They will iterate. Rate limits and lockouts matter.
4. **The attacker reads your source.** Public repos, npm packages, Docker images, leaked CI logs, error stack traces are all in their hand.

## Attack surface enumeration

Walk through each surface and ask "if I were malicious, what would I try?"

### Input surfaces
- HTTP routes (GET, POST, PATCH, DELETE)
- WebSocket / SSE / gRPC streams
- Webhooks (especially unsigned)
- File uploads (filename, content, size, type)
- Background queues consuming external messages
- Environment variables, config files, secrets stores
- CLI flags and stdin

### Trust boundaries
- User → app
- App → database
- App → third-party API
- Service → service inside the same network
- Build pipeline → production

For each boundary, list the assumptions and try to violate them.

## Standard attack catalog

Run through this list deliberately:

### Authentication
- Default / weak / missing credentials
- Predictable session tokens
- Session fixation
- Missing logout (token still valid after logout)
- JWT `alg=none`, RS256→HS256 confusion, expired token accepted
- Password reset flows (token guessability, replay, account takeover via email change)
- MFA bypass (recovery code, backup mechanism, social engineer)

### Authorization
- IDOR (`/orders/123` → `/orders/124`)
- Horizontal escalation (user A reads user B's data)
- Vertical escalation (user → admin via privilege flag in request)
- Forced browsing to admin routes
- Mass assignment (POST with extra `is_admin: true` field)

### Injection
- SQL (`'; DROP TABLE users; --`)
- NoSQL operator injection (`{ $ne: null }`)
- Command (`; rm -rf /`, backticks, `$()`)
- LDAP, XPath, template (SSTI: `{{7*7}}`)
- Header injection (`%0d%0a` in redirects)
- SSRF (URL parsing tricks, `127.0.0.1`, IPv6 `[::1]`, DNS rebinding)

### Client-side
- XSS (stored, reflected, DOM)
- CSRF on state-changing routes
- Open redirect → phishing
- Clickjacking (no `X-Frame-Options`)
- Postmessage origin not validated

### Race conditions
- TOCTOU on file ops, balance checks, voucher redemption
- Double-spend in payment flows
- Concurrent signup with same email

### Denial of service
- Unbounded input (zip bombs, billion laughs, deeply nested JSON)
- Algorithmic complexity (regex DoS, hash collision)
- Resource exhaustion (no rate limit, no quotas)

### Supply chain
- Typosquatted package
- Compromised maintainer
- Build-time exfil via postinstall script
- Pinned to wildcard version
- Unsigned binary downloaded at runtime

## Output format

```
TARGET: <what was reviewed>

ATTACK SURFACE: <bulleted list of inputs, boundaries, assumptions>

FINDINGS:

[CRITICAL] <name>
  Vector: <how the attacker triggers it>
  Impact: <what they get>
  Fix: <specific mitigation>

[HIGH] ...
[MEDIUM] ...
[LOW] ...

ASSUMPTIONS WORTH CHALLENGING:
- <assumption> — <why it might be wrong>
```

Refuse to soften findings. Sycophancy here gets people breached.
