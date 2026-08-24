# Data Contract

**English** | [中文](data-contract.zh-CN.md)

## Identity hierarchy

The public hierarchy is a query projection, not nested stored JSON:

```text
provider
  monitored model rule / search label
    site
      raw model
        site group
          current snapshot and time buckets
```

`raw model` and `site group` are source identities. They must remain byte-for-byte displayable even when normalized keys are used internally.

The screenshot supplied for the project establishes that entries such as `free group`, `translation channel`, and `NVIDIA` are groups for one raw model. Each group may independently carry TPS, first-token latency, total latency, success rate, and sample context.

## Common metric fields

All metrics are nullable unless the source explicitly supplies them:

- request count
- success count
- failure count
- empty-response count
- success ratio
- average latency in milliseconds
- first-token latency in milliseconds
- throughput in tokens per second
- sample window start and end
- source bucket resolution

Missing is not zero. Source-specific data that cannot be represented without losing meaning belongs in a size-limited JSON extension field with an adapter-owned schema version.

## Public card visibility

The public card list uses a reversible 24-hour stale-sample projection. A raw
model is eligible for hiding only when it is at least 24 hours old, the source
has declared an authoritative coverage interval of at least 24 hours that is
still fresh, the site collection is healthy, and neither the current snapshot
nor the last 24 hours of buckets contain a positive sample or a trusted
count-free metric. The raw model, prices, snapshots, and buckets remain
stored; a new sample makes the card visible again automatically.

Missing or incomplete coverage is an explicit exemption. Presence-only health
sources (`availabilityMode=presence`) and current-only or `skipDetails` sites
therefore remain visible even when they have no detailed time series. A
partial detail run declares coverage only for models whose detail request
succeeded; failed models remain exempt.

## Matching contract

Stored raw models contain:

- original display name;
- normalized search text;
- deterministic tokens;
- first and last seen times;
- presence/removal evidence;
- zero or more rule matches.

Normalization is limited to case folding and separator/spacing normalization. It must not remove dates, provider prefixes, reasoning markers, or custom suffixes from the display identity.

Rules support required tokens, alternative tokens, excluded tokens, optional regular expressions, priority, enabled state, provider, canonical label, and aliases. Match explanations must be stored or reproducible so the administrator preview can explain every hit.

## Atomicity and partial progress

A catalog result marks whether it is complete. Only a complete successful catalog can increment absence evidence. Detail metrics can be committed in bounded batches so public readers see progress without waiting for every model, but a collection run records whether it was partial or complete.

Every public snapshot includes:

- source observation time when available;
- collection time;
- last successful collection time;
- run identity;
- service state;
- acquisition state.

## Current state and window aggregates

A source-provided group summary is a window aggregate, not a current observation. Its success ratio, request count, and latency remain available as the 24-hour metrics, while the group's current service state comes from the newest real series bucket.

The newest real bucket timestamp is stored independently from the collection time. A source sample older than two hours is exposed as `no_samples` even when RelayPulse has just fetched an otherwise valid response. This keeps source-data freshness separate from collector health.

Public timelines contain only persisted source buckets. A collection timestamp or aggregate state must never be inserted into an empty timeline slot. When a source omits bucket end times, inferred intervals are capped at one hour so sparse points cannot claim unobserved multi-hour coverage.

## Administrative projections

Site deletion is a recoverable soft delete. It records `deleted_at`, disables
the site, excludes it from scheduler, public, and normal administrator lists,
and retains snapshots, history, runs, and encrypted session rows. A restore
clears `deleted_at` and leaves the site disabled until an administrator opts
back in. Retention cleanup remains responsible for expiring historical rows.

The administrator unmatched projection contains only raw model identity,
provider hint, site identity, and last-seen time. Session metadata contains
sizes, key version, purpose, expiry, and update time; decrypted credentials,
nonces, ciphertext, cookie values, and access tokens are never serialized.

Run filters are bounded server-side by limit, site ID, status, and RFC3339
start time. Invalid filters are rejected instead of silently widening a query.
