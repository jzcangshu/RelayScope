# Local development

All large project tooling and caches are kept inside the repository on drive D and ignored by Git.

## Go toolchain

The validated local toolchain is Go 1.26.5 for Windows amd64. It is extracted to `.tools/go`. The downloaded archive is retained at `.tools/go1.26.5.windows-amd64.zip` because the workspace policy forbids deleting files.

Expected SHA-256:

```text
97e6b2a833b6d89f9ff17d25419ac0a7e3b482a044e9ab18cdef834bd834fd38
```

## Commands

```powershell
./scripts/test.ps1
./scripts/dev.ps1
```

The development server defaults to `http://127.0.0.1:8080`. Runtime state defaults to `data/relaypulse.db` and is excluded from Git.

Supported environment variables currently are:

| Variable | Default | Meaning |
| --- | --- | --- |
| `RELAYPULSE_LISTEN_ADDR` | `127.0.0.1:8080` | HTTP listen address |
| `RELAYPULSE_DATA_DIR` | `data` | Runtime data directory |
| `RELAYPULSE_LOG_LEVEL` | `info` | `debug`, `info`, `warn`, or `error` |
| `RELAYPULSE_SHUTDOWN_TIMEOUT` | `10s` | Graceful shutdown timeout |
| `RELAYPULSE_FLARESOLVERR_ENDPOINT` | empty | Optional loopback FlareSolverr endpoint, for example `http://127.0.0.1:8191`; only contacted after a non-2xx response whose body explicitly looks like a Cloudflare challenge |
| `RELAYPULSE_SESSION_ENCRYPTION_KEY` | empty | Optional base64url key (at least 32 bytes before encoding) used to encrypt site session bundles |
| `RELAYPULSE_PUBLIC_URL` | empty | Optional public HTTP/HTTPS origin, configured after the production domain is ready |
| `RELAYPULSE_OAUTH_CLIENT_ID` | empty | OAuth provider client ID; together with the secret enables public login (currently LinuxDo) |
| `RELAYPULSE_OAUTH_CLIENT_SECRET` | empty | OAuth provider client secret; keep only in protected deployment configuration |
| `RELAYPULSE_HTTP_CONCURRENCY` | `3` | Maximum concurrent site HTTP operations (1-32) |
| `RELAYPULSE_COLLECTION_TIMEOUT` | `3m` | Per-site scheduled collection timeout |
| `RELAYPULSE_HTTP_TIMEOUT` | `20s` | Outbound HTTP client timeout |
| `RELAYPULSE_MAINTENANCE_INTERVAL` | `30m` | History cleanup interval (minimum 1m) |

## Session import

The administrator console accepts a minimal JSON bundle for a registered site:

```json
{"userAgent":"Mozilla/5.0 ...","cookies":[{"name":"session","value":"..."}]}
```

The bundle is encrypted with AES-GCM before it is written to SQLite. Do not
paste it into source control, issue trackers, or logs. The browser profile is
never copied to the server.

## Adapter contracts confirmed in Chrome

The NewAPI pricing family currently exposes `/api/pricing` with
`model_name` and `enable_groups[]`. Its performance pages expose
`/api/perf-metrics/summary?hours=24` and `/api/perf-metrics?model=...&hours=24`;
the latter returns per-group aggregate metrics plus hourly `series[]`. The
adapter keeps both the exact source model name and every source group name.
Large catalogs are fetched once, then detail requests are made only for names
that match the configured popular-model rules.

## Pricing development

Current prices are normalized by `internal/pricing` and stored in the existing
model/group source-extension JSON, so adding a decoder does not require a
database migration. The public dashboard receives the normalized quote with
each current group row and selects the lowest priced group that has a parsed
quote and is currently `healthy` or `degraded`. Groups that are `failed`,
`no_samples`, or `unknown` do not participate in card price selection; when no
usable priced group remains, the card shows that pricing is unavailable. Fixed
billing is displayed per request; usage billing is displayed per million
tokens with input, output, cache-read, and cache-write prices. Missing cache
fields remain visibly unavailable rather than being inferred from another
price.

Run focused decoder tests while developing a rule:

```powershell
$env:GOCACHE = Join-Path $PWD '.cache\go-build'
$env:GOMODCACHE = Join-Path $PWD '.cache\go-mod'
& '.\.tools\go\bin\go.exe' test ./internal/pricing ./internal/adapter
```

For a NewAPI-compatible probe, set `pricingAdapter` to `newapi` and configure
`pricingPath` plus the optional `pricingStatusPath`. For any other payload,
implement and register a decoder rather than embedding parsing logic in the
probe adapter or dashboard.

## Local acceptance evidence

The first local run registered all 13 initial sites. Sites that require an
authenticated session are reported as `login_expired`; a site with a detected
Cloudflare page is reported as `challenge_pending`; failed requests retain the
last successful snapshot. A representative run produced 162 public model/group
rows across 6 sites with usable snapshots.

On the development machine, the measured SQLite database was about 1.1 MB
(about 1.3 MB including WAL), public row queries averaged about 4 ms, and the
Go process used about 64 MB private memory. These figures are not a production
capacity guarantee, but confirm that the chosen single-process design is well
within the stated low-resource target for the current dataset.

## Debian 13 service outline

Run the binary as a dedicated unprivileged user and keep the data directory
outside the release directory. A minimal systemd unit is:

```ini
[Unit]
Description=RelayPulse uptime monitor
After=network-online.target
Wants=network-online.target

[Service]
User=relaypulse
Group=relaypulse
WorkingDirectory=/opt/relaypulse
ExecStart=/opt/relaypulse/relaypulse
Environment=RELAYPULSE_LISTEN_ADDR=127.0.0.1:8080
Environment=RELAYPULSE_DATA_DIR=/var/lib/relaypulse
EnvironmentFile=-/etc/relaypulse/relaypulse.env
Restart=on-failure
RestartSec=10
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=/var/lib/relaypulse

[Install]
WantedBy=multi-user.target
```

FlareSolverr is optional and must be managed separately, bound to
`127.0.0.1` only. Keep it single-concurrency with a short timeout and a
failure cooldown; it is not a guarantee that every Cloudflare challenge can
be solved. Do not run Chromium and FlareSolverr on a 768 MB host without swap
and memory monitoring.

OAuth 登录的人机验证不由 RelayPulse 自动处理。管理员在本地 Chrome 完成授权后，将最小化的 User-Agent（浏览器标识）与 Cookie（会话 Cookie）包导入后台；主服务不会读取 Chrome 配置目录，也不会把浏览器凭据写入 Git，导入内容通过上述密钥加密后保存。

## Chrome session sync

Load `extension/session-sync` as an unpacked Manifest V3 extension. In the
administrator console choose **浏览器同步**, copy the ten-minute pairing code,
and enter it with the RelayPulse origin in the extension. The extension asks
for host access only to that origin and the pending sites returned by the
server. It never receives the administrator password or administrator cookie.

The sync token is bound to the extension origin, expires after thirty minutes,
and is consumed after one successful batch. The server stores imported bundles
through the existing encrypted session vault.
