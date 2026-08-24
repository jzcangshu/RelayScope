# Changelog

All notable changes to RelayPulse are documented here.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

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
  rename to `RelayPulse` (image now published as `ghcr.io/jzcangshu/relaypulse`).

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

[v0.1.1]: https://github.com/jzcangshu/RelayPulse/compare/v0.1.0...v0.1.1
[v0.1.0]: https://github.com/jzcangshu/RelayPulse/releases/tag/v0.1.0
