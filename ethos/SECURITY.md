# Security Policy

## Reporting Vulnerabilities

**Please do not report security vulnerabilities through public GitHub issues.**

Instead, use [GitHub Security Advisories](https://github.com/Sahaj-Tech-ltd/ethos/security/advisories/new).

### Response Timeline

| Stage | Timeline |
|-------|----------|
| Acknowledgment | 48 hours |
| Initial assessment | 1 week |
| Critical fix | 2 weeks |
| Coordinated disclosure | 90 days |

## Trust Model

Ethos is a **single-user personal coding agent**. It is NOT designed for:
- Multi-tenant environments
- Shared workspaces with untrusted users
- Production server deployment
- Handling untrusted input at scale

If you're using Ethos in any of these contexts, you're outside the threat model.

## Agent Autonomy Levels

| Level | Description | Default |
|-------|-------------|---------|
| ReadOnly | Agent can read files but cannot execute commands or write | No |
| Supervised | Agent proposes actions, user approves before execution | **Yes** |
| Full | Agent executes without approval | No |

We strongly recommend staying in **Supervised** mode.

## Sandboxing Layers

1. **Workspace isolation** — agent operates within a project directory
2. **Path traversal blocking** — `..` sequences and symlink attacks blocked
3. **Command allowlisting** — configurable allow/deny patterns for shell commands
4. **Forbidden paths** — `/etc`, `~/.ssh`, `~/.gnupg`, cloud credentials
5. **Rate limiting** — prevents runaway tool execution loops
6. **Pre-exec scanning** — destructive commands flagged before execution

## What We Consider Security Issues

- Prompt injection that bypasses the security plane
- Credential exfiltration through tool output or logs
- Arbitrary command execution outside workspace scope
- File access outside project directory
- Privilege escalation via tool chaining

## What We Do NOT Consider Security Issues

- Agent producing incorrect code (quality issue, not security)
- Agent being unable to complete a task (functionality issue)
- Config file containing user-provided API keys (user responsibility)
- Agent following user instructions that are destructive (user authorized)

## Supply Chain Security

- GitHub Actions use pinned SHAs (not tags) for critical workflows
- `govulncheck` runs on every push
- `pip audit` runs on every push
- Supply chain audit workflow checks for `.pth` files, `base64+exec` patterns
- Dependabot monitors Go, Python, and GitHub Actions dependencies

## No Bug Bounty

Ethos does not offer a monetary bug bounty program. We offer:
- Credit in our security acknowledgments
- CVE assignment for qualifying vulnerabilities
- Coordinated disclosure timeline

## Coordinated Disclosure

If a vulnerability is confirmed:
1. We assign a CVE through GitHub Security Advisories
2. We develop and test a fix
3. We release a patch version
4. After 90 days (or after patch is available), details are published

## Out of Scope

- Social engineering attacks against maintainers
- Attacks requiring physical access to the user's machine
- Denial of service against GitHub infrastructure
- Vulnerabilities in dependencies not affecting Ethos directly
