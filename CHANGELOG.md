# Changelog

All notable changes to RelayScope are documented here.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- Versioned deployment catalogs and admin-API import tooling for server
  migration: `sites.production.json` (36 monitored sites) and
  `rules.production.json` (57 model-matching rules, including gpt-oss-120b/20b,
  gpt-6-astra, claude-fable-5.1/5.2, glm-5.3-flash, and
  deepseek-v4-pro-0813), replayable with `scripts/import-sites.sh` and
  `scripts/import-rules.sh`. Catalogs hold public site URLs, model names, and
  matching patterns only — credentials never enter the repository.

## [v0.1.2] - 2026-09-05

### Fixed
- A transient session-refresh failure no longer permanently locks an
  authenticated site as `login_expired`. Session refreshes now classify their
  HTTP status the same way collection-time fetches do: a 401/403 rejection
  marks the site `login_expired`, while a 5xx or network error reports
  `adapter_collect_failed` so the next scheduled run can retry with the stored
  refresh cookie. Previously any refresh error was hardcoded to
  `login_expired`, which combined with the scheduler's 30-minute backoff could
  strand a site indefinitely after a single failed refresh.
- Raised the FlareSolverr challenge timeout to 180s and the scheduled and
  manual collection ceilings to 7 minutes, because heavier Turnstile
  challenges and the two-solve-per-collection pattern on NewAPI pricing
  sites were clipping healthy runs with `challenge_failed` or
  `context canceled`.
- Browser session sync no longer fails with 401 right after pairing: Chromium
  omits the `Origin` header on cross-origin GET fetches made by extensions, so
  the extension now reads pending sites via POST (which always carries Origin)
  and the server accepts both GET and POST on `/api/v1/session-sync/pending`.
- Extension error messages now surface the server's `error` field instead of a
  generic status code.
- A successful model-probe report that contains no models is now treated as an
  empty catalog: every previously known model of the site is marked
  unavailable instead of the collection failing with `adapter_collect_failed`.
  The collector accepts an empty catalog when the adapter explicitly declares
  how absent models should be marked; silent emptiness still fails the run.
- A missing-catalog pass re-admits soft-removed models before selecting
  groups, so the first empty catalog marks the full model list unavailable
  instead of resurrecting removed models with their pre-removal snapshots.

### Changed
- The project is renamed to `RelayScope` across the module path, docs, build
  scripts, and container image references.
- The administrator session-import prompt and the operations docs describe the
  session JSON contract generically (auth types, cookies, refresh rotation)
  instead of calling out one specific site.

## [v0.1.1] - 2026-08-24

### Added
- Bilingual documentation: Chinese translations of every guide with per-document
  language switching; the Chinese README is the default entry point and the
  English mirror lives in `README_EN.md`.
- Measured deployment sizing guide with the production baseline, recommended
  host profiles, a capacity model, and scaling signals.
- Dashboard screenshot in both READMEs.

### Changed
- README restructured: hero image, revised introduction, and the recommended
  server section placed before project status.
- CI badge links and the quick-start image path updated for the repository
  rename to `RelayScope` (image now published as `ghcr.io/jzcangshu/relayscope`).

### Removed
- Internal restructuring records (`docs/development-plan.md`, `docs/plans/`)
  and the personal agent working agreement (`AGENTS.md`) from the public
  repository.

## [v0.1.0] - 2026-08-24

### Added
- Management API lifecycle controls, filtered runs, unmatched-model inspection, redacted session metadata, and schema-driven adapter fields.
- Configurable HTTP concurrency, collection/HTTP timeouts, maintenance interval, and build metadata.
- Public open-source repository scaffold: CI workflow, Dockerfile, Makefile, CONTRIBUTING guide.
- Cross-platform build entry point (`make build/test/vet`) replacing PowerShell-only scripts.

### Changed
- Repository repositioned as a generic relay health aggregator (decoupled from LinuxDo community specifics).

### Removed
- Hardcoded community site seeds and per-site operational migrations.

[v0.1.2]: https://github.com/jzcangshu/RelayScope/compare/v0.1.1...v0.1.2
[v0.1.1]: https://github.com/jzcangshu/RelayScope/compare/v0.1.0...v0.1.1
[v0.1.0]: https://github.com/jzcangshu/RelayScope/releases/tag/v0.1.0
