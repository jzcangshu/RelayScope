# RelayPulse Architecture

## System shape

RelayPulse is a modular monolith. Its normal production runtime is one Go binary serving the public application, administrator console, JSON API, scheduler, collectors, and maintenance jobs. SQLite is the only database.

No Redis, message queue, separate time-series database, or frontend application server is permitted without a measured requirement and an architecture decision record.

```text
Browser / API client
        |
Go HTTP server
  |-- public queries and static assets
  |-- administrator API
  |-- scheduler and bounded worker pool
  |-- adapter registry
  |-- model matcher
  |-- session and challenge coordinators
        |
SQLite (WAL)
        |
Optional short-lived browser / FlareSolverr process
```

## Package boundaries

Planned internal packages:

- `internal/config`: validated application configuration.
- `internal/store`: SQLite schema, migrations, transactions, and query methods.
- `internal/domain`: stable domain types and state semantics.
- `internal/matcher`: deterministic model normalization, tokenization, matching, and previews.
- `internal/adapter`: adapter interfaces, registry, configuration schemas, and implementations.
- `internal/collector`: collection orchestration, retries, completeness checks, and transactional writes.
- `internal/scheduler`: jittered per-site scheduling with no overlapping site runs.
- `internal/session`: encrypted site sessions and OAuth login recovery coordination.
- `internal/challenge`: challenge detection and replaceable FlareSolverr/browser recovery.
- `internal/httpapi`: public and administrator handlers.
- `web`: embedded static frontend source and build output.

Administrative site deletion is a soft delete: historical data remains for
retention and recovery, while deleted sites are disabled and excluded from
active queries and scheduling. Recovery is explicit and restores a disabled
site, avoiding an unexpected collection on restore.

Adapters are compiled Go plugins in the architectural sense, not Go's runtime `plugin` package. Each adapter is a separately registered module with metadata, a configuration schema, discovery logic, and collection logic. This avoids unsafe dynamic loading while keeping integrations independently testable, enableable, and maintainable.

## Adapter contract

An adapter receives an immutable site definition and a scoped fetch client. It must not write the database directly.

It returns:

- source identity and observed source time;
- whether the model catalog is complete;
- discovered raw models;
- raw site groups for each model;
- current aggregate metrics when available;
- source-provided time buckets;
- typed collection diagnostics;
- limited adapter-specific extension data.

The fetch client provides HTTP, authenticated HTTP, cleared HTTP, and browser-context fetch modes. An adapter requests a capability but does not launch browsers itself.

## Collection flow

1. Scheduler creates a run only if the site has no active run.
2. Adapter fetches catalog or a batch endpoint outside a database transaction.
3. Matcher selects relevant raw models from the complete catalog.
4. Adapter obtains detail data only for selected models when a batch endpoint is unavailable.
5. Collector validates and normalizes the result in memory.
6. Store writes a bounded batch transaction and advances the public data revision.
7. Frontend conditional polling observes the new revision.

HTTP concurrency defaults to three sites globally. Work inside one site is serial unless an adapter declares a safe limit of two. Browser and FlareSolverr activity is globally serialized.

## Incremental history

Adapters refetch a bounded overlap window, normally the source's public 24-hour window. They return all real source buckets with stable boundaries and resolution. Time buckets have a uniqueness key composed from source group identity, metric window start, and source bucket resolution. Repeated observations overwrite the same row, so a later run repairs gaps left by failed or mistimed collection without fabricating samples. The database retains 72 hours to keep overlap repair separate from retention.

Embedded catalog or batch history is preferred because it adds no per-model network fan-out. Sources that expose only current values or catalog presence cannot participate in historical repair; their adapters must not synthesize buckets. Incomplete zero-sample current buckets are retained for provenance but do not replace the latest sampled current state.

For unavoidable per-model detail requests, successful models are committed even when some requests fail. Bounded typed diagnostics mark the run as partial. If every detail request fails, the collection fails before storage and the last successful snapshot remains active.

Current snapshots are stored separately from historical buckets so public list queries remain small. Network operations never hold a SQLite transaction.

Historical buckets and verbose run diagnostics expire after three days. Identity, configuration, current snapshots, model-removal evidence, and compact operational summaries remain.

## Browser processes

The main binary never keeps Chromium running merely for scheduling. Browser work is an exceptional external capability:

- OAuth workflow: import the minimal session bundle established in the user's local browser and store it with authenticated encryption.
- Cloudflare workflow: solve an occasional managed or checkbox challenge with a pinned FlareSolverr provider, then reuse clearance or fetch inside the cleared context.

Cloudflare browser work acquires one global lease with hard timeouts, memory-aware launch settings, cooldowns, and process cleanup. Production targets the upgraded server; swap is still recommended because browser memory use is bursty.

## Frontend delivery

The public interface and administrator console are static assets embedded in the Go binary. The implementation should use browser-native JavaScript or a build-time-only lightweight framework only when it materially reduces complexity. Runtime Node.js is forbidden.

Public updates use a revision-aware conditional poll every 30 to 60 seconds. WebSocket and server-sent events are unnecessary for a source that updates approximately every 20 minutes.

## Security

- Administrator authentication uses a strong generated password, an adaptive password hash, secure cookie sessions, CSRF protection, and login rate limiting. Browser session sync uses a short-lived, origin-bound pairing token; the extension never receives the administrator password.
- Site credentials and cookies use authenticated encryption with a key supplied outside the database.
- Logs redact cookies, authorization headers, API keys, and query values known to contain credentials.
- FlareSolverr binds only to loopback and is never exposed publicly.
- Browser automation is restricted to registered site and OAuth provider domain allowlists.
- `.env` files, database files, browser profiles, cookies, and keys never enter Git.

## Capacity target

SQLite is sufficient for tens of sites and a three-day horizon. Schema and indexes must be measured with representative generated data before production. A maintenance job deletes expired rows in small batches and performs incremental page reclamation; it does not frequently perform a full database vacuum.
