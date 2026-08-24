# Security Policy

## Reporting a vulnerability

If you discover a security vulnerability in RelayScope, **do not open a public GitHub issue**.

Instead, use GitHub's private vulnerability reporting:

1. Go to the repository's **Security** tab.
2. Click **Report a vulnerability**.
3. Describe the issue with reproduction steps and impact.

You should receive an initial response within 72 hours.

## Scope

RelayScope is a self-hosted monitoring aggregator. Security-relevant areas include:

- Administrator authentication and session management.
- Encrypted credential storage (site tokens and cookies).
- CSRF protection on management endpoints.
- Cloudflare challenge / browser session handling boundaries.
- Input validation on adapter-collected data before it reaches SQLite.

## Supported versions

Only the latest release line receives security fixes. The `main` branch is development and may contain unreleased changes.
