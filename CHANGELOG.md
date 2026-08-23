# Changelog

All notable changes to RelayPulse are documented here.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- Public open-source repository scaffold: CI workflow, Dockerfile, Makefile, CONTRIBUTING guide.
- Cross-platform build entry point (`make build/test/vet`) replacing PowerShell-only scripts.

### Changed
- Repository repositioned as a generic relay health aggregator (decoupled from LinuxDo community specifics).

### Removed
- Hardcoded community site seeds and per-site operational migrations.
