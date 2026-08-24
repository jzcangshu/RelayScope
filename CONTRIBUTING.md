# Contributing to RelayScope

Thanks for your interest in RelayScope. This guide explains how to contribute effectively within the project's constraints.

## Project constraints (read first)

RelayScope is a **modular monolith**: one Go binary, one SQLite database, one process. The following are hard boundaries — contributions that cross them will be redirected:

- **No Redis, message queue, separate time-series database, or frontend application server.** New dependencies require an Architecture Decision Record (ADR).
- **No CGO.** The single direct runtime dependency is `modernc.org/sqlite` (pure-Go SQLite). Keep it that way.
- **No Node.js at runtime.** The frontend is static assets embedded in the binary. Build-time tooling is allowed only when it materially reduces complexity.
- **Target deployment: 1-core, 768 MB server.** Memory and allocation pressure matter. Don't add background goroutines, caches, or polling loops without a measured need.

When in doubt, prefer fewer abstractions and fewer dependencies. See `docs/architecture.md` and `docs/decisions/0001-modular-monolith.md` for the full rationale.

## Development setup

```bash
# Prerequisites: Go 1.26+, Node.js LTS (frontend tests only)
make tidy
make test      # Go tests + frontend tests
make run       # starts on http://127.0.0.1:8080
```

On first run the server generates a strong admin password and writes it to `<data-dir>/admin-password.txt` (mode 0600).

## Adding a new site adapter

Adapters are the primary extension point. See `docs/adapter-authoring.md` for the full guide. Summary:

1. Implement the `adapter.Adapter` interface (4 methods: `Key`, `DisplayName`, `ConfigSchema`, `Collect`).
2. Register it in `cmd/relayscope/main.go`.
3. Add tests following the table-driven style in `internal/adapter/adapter_test.go`.

**Do not hardcode site-specific results.** Adapters implement the source's data protocol; they must not encode the current return values of a particular site.

## Code style

- `gofmt -s` is the baseline; CI enforces it.
- Wrap errors with `%w`. Distinguish "invalid input" from "operation failed".
- Keep functions short. If a function exceeds ~60 lines, look for an extractable unit.
- No `TODO`/`FIXME` without a linked issue.
- Match the surrounding code's comment density — comments state constraints the code can't show, nothing more.

## Commit messages

Use [Conventional Commits](https://www.conventionalcommits.org/):

```
feat: add sub2api adapter
fix: persist scheduler next-run timestamps
docs: clarify acquisition state semantics
refactor: split store.go by domain
```

## Pull requests

- One logical change per PR. If you need "and also", it's two PRs.
- Include or update tests for behavior changes.
- Don't commit credentials, `.db` files, or runtime state. The `.gitignore` covers the known paths — verify before pushing.
- Don't commit build artifacts (`bin/`, `dist/`).

## Security

If you discover a security issue, **do not open a public issue**. Email the maintainer directly or open a private security advisory on GitHub.
