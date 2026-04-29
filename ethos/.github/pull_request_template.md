## Summary
<!-- 2-5 bullet points -->

## Change Type
- [ ] Bug fix
- [ ] New feature
- [ ] Refactor
- [ ] Security fix
- [ ] Documentation
- [ ] Skill addition

## Scope
- [ ] agent-loop
- [ ] security
- [ ] compaction
- [ ] TUI
- [ ] routing
- [ ] memory
- [ ] tools
- [ ] CI

## Validation Evidence
<!-- Paste literal test output -->

```bash
go test ./...
golangci-lint run
ruff check bridge/
```

## Security & Privacy Impact
- [ ] No credentials or secrets introduced
- [ ] No user data exposed in logs
- [ ] No new outbound network calls
- [ ] Filesystem operations scoped to workspace
- [ ] No privilege escalation risk

## Compatibility & Migration
<!-- Breaking changes? Config migration needed? -->

## Rollback Plan
<!-- Required for medium/high risk changes -->

## AI Assistance
- [ ] This PR was generated with AI assistance
<!-- If checked, add Co-Authored-By trailer -->
