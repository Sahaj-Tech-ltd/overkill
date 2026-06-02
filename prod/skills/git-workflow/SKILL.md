---
name: git-workflow
description: Use when working with git — branching, committing, pulling, pushing, handling merge conflicts, or creating PRs. Also use when the user says "commit this", "push", "create a PR", or "what branch am I on".
---

# Git Workflow

## Overview

Standard git operations for the Overkill repo. Never push without explicit user approval.

## Repo Location

```bash
cd ~/docker/overkill
```

## Quick Reference

```bash
# Status
git status
git log --oneline -10
git diff
git diff --stat

# Branch
git branch                    # list local branches
git checkout -b feature/name  # create + switch

# Stage & Commit (Conventional Commits)
git add -A
git commit -m "feat: add X"
git commit -m "fix: resolve Y"
git commit -m "refactor: clean up Z"

# Push (USER MUST APPROVE FIRST)
git push origin main
git push origin feature/name

# Pull
git pull origin main
git pull --rebase origin main   # clean history
```

## Conventional Commits

| Prefix | When |
|--------|------|
| `feat:` | New feature |
| `fix:` | Bug fix |
| `refactor:` | Code restructuring, no behavior change |
| `test:` | Adding/updating tests |
| `docs:` | Documentation |
| `chore:` | Build, deps, config |
| `perf:` | Performance improvement |

## Branch Strategy

- `main` — stable, deployable
- `feature/<name>` — new features
- `fix/<name>` — bug fixes
- `refactor/<name>` — refactoring

## Before Committing

1. `go build ./...` must pass
2. `cd tui && npx tsc --noEmit` must pass
3. `go test ./...` should pass
4. No `.env`, secrets, or API keys in diff
5. Internal docs (bugs.md, plan.md) should NOT be staged

## Before Pushing

**🚫 NEVER push without explicit user approval.**
The user must explicitly say "push" or "push it."

When approved:
1. `git rev-parse --show-toplevel` — verify right repo
2. `git remote -v` — verify remote is Sahaj-Tech-ltd/overkill
3. `git diff origin/main --stat` — review what you're pushing
4. `git push origin <branch>`

## Merge Conflicts

1. `git status` — see conflicted files
2. Edit files — resolve `<<<<<<<` markers
3. `git add <resolved-file>`
4. `git commit` (no -m, use merge message)
5. Or abort: `git merge --abort`

## Undoing

```bash
git reset HEAD~1              # undo last commit, keep changes
git reset --hard HEAD~1       # undo last commit, discard changes
git checkout -- <file>        # discard uncommitted changes to file
git stash                     # temporarily hide changes
git stash pop                 # restore stashed changes
```
