# Operations checklist

## Before first deployment

- Create `/var/lib/relaypulse` with mode `0700` and ownership `relaypulse`.
- Generate a strong `RELAYPULSE_SESSION_ENCRYPTION_KEY` outside Git.
- Copy only the release binary and embedded assets.
- Put the generated administrator password file in the protected data
  directory; do not put it in a shell history or repository.
- Configure the reverse proxy to reach `127.0.0.1:8080` and terminate HTTPS.
- Configure `RELAYPULSE_PUBLIC_URL=https://watchbot.cfd` after the domain resolves to the server and the reverse proxy certificate is active.

## Release files

- Build Linux amd64 with `scripts/build-linux.ps1`.
- Install the binary as `/opt/relaypulse/relaypulse` and the unit from `deploy/relaypulse.service`.
- Copy `deploy/relaypulse.env.example` to `/etc/relaypulse/relaypulse.env`, then fill secrets only on the server with mode `0600`.
- Use `deploy/Caddyfile.example` only after replacing the placeholder domain.

## FlareSolverr

- Pin the standalone Linux x64 release instead of adding a container runtime.
- Install it under `/opt/flaresolverr`, run it as the unprivileged
  `flaresolverr` user, and use `deploy/flaresolverr.service`.
- Keep `HOST=127.0.0.1`; port 8191 must never be exposed publicly.
- Keep the RelayPulse endpoint empty until the helper passes its startup and
  request checks, then set it to `http://127.0.0.1:8191`.
- Browser work is serialized by RelayPulse. The systemd unit also applies a
  1 GB soft memory limit and a 1.5 GB hard limit.
- FlareSolverr can clear some browser checks and non-interactive challenges,
  but its upstream CAPTCHA solvers are currently documented as unavailable.
  Treat `challenge_failed` as an expected external limitation and retain the
  last successful snapshot.

## Routine checks

- Open `/health/live` and `/health/ready` after upgrades.
- Inspect the administrator collection-run view for `login_expired`,
  `challenge_pending`, `challenge_failed`, and stale data.
- Refresh private-site credentials through the Chrome access-token side panel.
- Keep at least one recent SQLite backup; copy the database while the service
  is stopped or use SQLite's online backup tooling.
- Watch disk usage. The service deletes history older than 72 hours in small
  batches and does not run a full vacuum on every cycle.
- The access-token extension must point only at the configured RelayPulse
  origin. It uploads exact-origin matches from the pending list and never the
  rest of an account backup.
- The port 80 virtual host in `deploy/nginx-monitor.conf` forwards the full
  application. Prefer HTTPS before using administrator or session-sync paths
  on an untrusted network.

## Recovery semantics

A collection failure never overwrites the last successful service snapshot.
The public page marks acquisition freshness separately, so users can tell
whether a model is unhealthy or whether the monitor simply cannot currently
read the source site.
