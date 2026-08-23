## Summary
<!-- One or two sentences: what does this change do and why? -->

## Related issue
<!-- Closes #N, or "N/A" -->

## Type of change
- [ ] Bug fix (non-breaking)
- [ ] New feature / adapter
- [ ] Refactor (no behavior change)
- [ ] Documentation
- [ ] Infrastructure / CI

## Constraints checklist
Contributions must respect the project's hard boundaries. Confirm:
- [ ] No new external dependency without an ADR
- [ ] No CGO / no Redis / no message queue / no separate TSDB
- [ ] No runtime Node.js
- [ ] No hardcoded site-specific results (adapters implement the protocol, not a site's current output)
- [ ] No credentials, `.db` files, or build artifacts committed

## Verification
<!-- What did you run? e.g. `make check`, specific test names, manual steps -->

## Notes for review
<!-- Anything reviewers should pay attention to -->
