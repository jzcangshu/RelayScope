# RelayPulse Product Specification

## Purpose

RelayPulse answers two practical questions for users of AI API relay sites:

1. If I want to use a model now, which site and group currently look usable?
2. If I want to use a site now, which monitored models and groups currently look usable?

It reports data published by each site or its probe. It does not call models and must not imply that it independently verified model service.

## Product priorities

In descending order:

1. Clear, filterable, current data.
2. Low resource usage on a 1-core, 768 MB server.
3. Stable unattended operation.
4. Low-effort site and matching maintenance.
5. Simple code and operations.

Cross-site statistical scoring, long-term analytics, and low-usage model coverage are not goals.

## Public experience

- Chinese-only responsive interface for desktop and mobile.
- Browse by monitored model, provider, site, raw model name, and site group.
- Preserve and display the exact source model and group names.
- Show source URL, source timestamp, collection timestamp, sample count when available, and collection freshness.
- Distinguish service health from collection health.
- Sort by service state, freshness, source success rate, sample size, then latency. Do not invent a composite score.
- Treat an empty sample window as `no samples`, never as healthy or failed.
- Refresh incrementally without a full page reload.

## Administration experience

- Password-protected administrator console with rate-limited login.
- Manage sites, adapter selection, schedules, adapter configuration, and enabled state.
- Manage popular-model matching rules and immediately preview their matches against discovered raw names.
- Inspect ambiguous matches, unmatched popular-looking names, collection runs, login state, challenge state, and stale data.
- Trigger a bounded collection or session recovery run.
- Never display complete cookies, OAuth state, encryption keys, or other credentials.

## Adding sites

RelayPulse starts with an empty database. Sites are added through the admin console. Display names may fall back to the site's own title or hostname and can be edited after creation. See `sites.example.json` in the repository root for the configuration format.

## Model matching rules

RelayPulse ships with a small set of example matching rules that demonstrate each matching capability (required terms, excluded terms, regex patterns, aliases, priority-based specificity). Administrators add their own rules through the console to match the model families they care about.

Matching is intentionally recall-oriented. Prefix and suffix variants coexist as distinct raw models. Rules are deterministic and editable. Exactly one matching rule assigns the public canonical model; multiple matching candidates are retained as an administrator-visible conflict with no public canonical assignment until the rule set is corrected. Exact raw names are never rewritten.

## Collection and display defaults

- Healthy sites are collected every 15 minutes with bounded jitter.
- Sites whose latest acquisition failed, needs login, or could not clear a challenge are retried every 30 minutes. A successful collection automatically restores the normal interval.
- Current snapshots power the compact list, but their service state comes from the newest real source bucket; source-provided 24-hour summaries remain aggregate metrics only.
- A newest source bucket older than two hours becomes `no_samples` even when collection itself is fresh. Public timelines never synthesize a slot from an aggregate value or collection time.
- When a source omits bucket end times, inferred coverage is capped at one hour.
- Historical buckets remain available for up to 72 hours before cleanup.
- A success ratio of 85% or above is healthy, 50% to below 85% is degraded, and below 50% is failed. Missing samples remain `no_samples`.
- A model card is hidden only after 24 hours without a sample when the source has supplied a fresh, authoritative 24-hour history window and the model is mature enough to judge. Sources that only report presence or current health remain visible; hidden cards are retained in storage and reappear as soon as a new sample arrives.

## State semantics

Service state and acquisition state are independent.

Service states:

- `healthy`
- `degraded`
- `failed`
- `no_samples`
- `removed`
- `unknown`

Acquisition states:

- `fresh`
- `stale`
- `collecting`
- `collection_failed`
- `login_expired`
- `challenge_pending`
- `challenge_failed`

A failed collection retains the last successful service snapshot. A raw model becomes removed only after three successful complete-catalog collections omit it. Removed entries are hidden from normal views after three days but retained as lightweight identity metadata.

## Authentication and challenge boundaries

- All registered initial sites are approved for LinuxDo-authenticated session import.
- The existing user script remains the behavioral reference for the user's local authorization flow, but RelayPulse does not automate the LinuxDo login challenge.
- LinuxDo session import and Cloudflare recovery are separate workflows sharing only the encrypted credential store.
- Occasional checkbox-style Cloudflare challenges must be attempted unattended.
- FlareSolverr may be pinned, wrapped, or patched as a replaceable challenge provider.
- If clearance cookies cannot be reused by the HTTP collector, the collector may fetch the structured endpoint inside the freshly cleared browser session.
- Recovery is bounded and enters cooldown rather than repeatedly hitting a community site.

## Local-first delivery

Development and validation happen on Windows first. Production installation on Debian 13 is a later delivery phase. Local toolchains and caches should live on drive D or F, not consume scarce drive C space.
