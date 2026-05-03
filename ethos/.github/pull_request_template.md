## Summary
<!-- 2-5 bullets covering the why and the what. -->

## Test Plan
<!-- Concrete commands run + their results. Paste literal output. -->
```
go test -race ./...
golangci-lint run
```

## Risk
<!-- Blast radius. What breaks if this is wrong? Rollback plan? -->

## Linked Issues
<!-- Closes #123, refs #456. -->

---

### Security & Privacy Impact
- [ ] No credentials or secrets introduced
- [ ] No user data exposed in logs
- [ ] No new outbound network calls
- [ ] Filesystem operations scoped to workspace
- [ ] No privilege escalation risk

### AI Assistance
- [ ] This PR was generated with AI assistance (if checked, include `Co-Authored-By` trailer)
