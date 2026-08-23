# Adapter authoring

An adapter is a compiled module registered in `cmd/relaypulse/main.go`. It
must implement the small `internal/adapter.Adapter` contract and return a
validated `domain.Collection`; it never writes SQLite directly.

Use this workflow for a new site family:

1. Record the page URL and the structured requests observed in Chrome.
2. Save a sanitized response fixture under `internal/adapter/testdata/`.
3. Add a contract test for catalog shape, exact raw names, all source groups,
   empty samples, and the detail series.
4. Add a config schema and register the adapter key.
5. Configure the site in the administrator console; do not add site-specific
   credentials or code paths to the collector.

Adapters should fetch a complete catalog when the source provides one. The
collector applies popular-model matching before detail requests, so a large
catalog does not create proportional historical data. A zero-model catalog is
an error unless the catalog was valid and none of its names matched a popular
rule; that distinction is recorded in the collection run.

When a source exposes historical buckets, every collection should refetch a
bounded overlap window (normally 24 hours) and return every real bucket in that
window. Keep the source's stable bucket boundaries and resolution: storage
upserts repeated buckets by group, start time, and resolution, which repairs
missed runs without duplicating history. Never synthesize history from a
current value, catalog presence, or an aggregate. An incomplete current bucket
with zero samples may be stored as history, but it must not replace the latest
sampled current state.

Adapters that want the public stale-sample card policy to evaluate a model must
also return an authoritative `HistoryCoverageStart` and `HistoryCoverageEnd`
covering at least 24 continuous hours. Set neither field when history is not
available, when a detail request failed, or when the response only proves
catalog presence/current health. `availabilityMode=presence` and
`skipDetails=true` are intentionally history-exempt. Coverage is model-level;
for partial detail success, declare it only on successful models. The policy
never deletes the model or its stored history, and a later real sample restores
the card automatically.

Prefer history embedded in an already-required catalog or batch response. Do
not add one request per model when the same window is available in that
response. If detail requests are unavoidable, continue after individual model
failures and attach bounded typed diagnostics to the successful collection.
Return an error when every attempted detail request fails so the collector
preserves the previous snapshot. A mixed result is committed and recorded as a
partial run.

When catalog membership is itself the availability signal, set
`availabilityMode` to `presence`. Listed models are healthy, while previously
known models omitted from a complete catalog are retained and marked failed.
Do not use this mode for paginated or filtered responses unless the adapter has
verified that it assembled the complete catalog.

Raw source names and group names are identity data. Never normalize them for
display or use a normalized name as a database identity key.

## Pricing decoders

Pricing is separate from health collection. A source-specific price format
belongs in `internal/pricing` and implements the `pricing.Decoder` contract.
Register it in `pricing.DefaultRegistry`; do not add source-specific branches
to the store, collector, or public dashboard.

A decoder receives the pricing response and an optional status/configuration
response. It returns normalized model metadata and one display quote per source
group. Normalized quotes may use either input/output prices per million tokens
or a fixed price per request. They also retain the source currency code,
currency symbol, group multiplier, and any exchange-rate conversion applied by
the source.

Decoders may also return direct model/group quotes when the source publishes a
fully calculated price per channel. The built-in `model-market` decoder reads
`data.items[].pricing`, normalizes per-token values to per-million-token values,
and preserves channel names as source groups. Prefer direct quotes over
reconstructing a billing formula that the source has already evaluated.

The built-in `newapi` decoder reads:

- `model_ratio`, `completion_ratio`, and `group_ratio` for ratio billing;
- `cache_ratio`, `create_cache_ratio`, and compatible cache-creation aliases;
- standard-tier `cr` and `cc` coefficients from `billing_expr` when explicit
  cache ratios are absent;
- `model_price` for fixed billing;
- `quota_per_unit`, `quota_display_type`, `custom_currency_symbol`, and
  `custom_currency_exchange_rate` from the status response.

Add decoder tests for every billing mode, missing-price state, group
multiplier, and currency conversion before registering a new format.

Probe adapters expose three optional configuration fields:

```json
{
  "pricingAdapter": "newapi",
  "pricingPath": "/api/pricing",
  "pricingStatusPath": "/api/status"
}
```

When pricing is present in the probe catalog response, omit `pricingPath`; the
configured decoder receives the already-fetched catalog. This avoids a second
request and is appropriate for `model-market` payloads.

Uptime Kuma adapters and probe adapters can attach an independent price source:

```json
{
  "statusBaseUrl": "https://status.example.com",
  "pricingBaseUrl": "https://api.example.com",
  "pricingAdapter": "newapi",
  "pricingPath": "/api/pricing",
  "pricingStatusPath": "/api/status",
  "pricingOptional": true,
  "pricingRequiresSession": true
}
```

`statusBaseUrl` lets health collection remain on a dedicated status origin.
`pricingOptional` isolates price-source failures so health data still updates.
`pricingRequiresSession` exposes a missing pricing login to session sync. Stored
cookies and authorization headers are sent only to the exact origin of the
site's `baseUrl`; cross-origin status requests always use the public fetcher.

For probe adapters, catalog, batch-status, and detail paths all resolve against
`statusBaseUrl`, while an omitted `pricingBaseUrl` keeps pricing paths on the
site's `baseUrl`. When an ungrouped probe reports model-wide health and the
pricing catalog supplies named groups, the adapter projects the same health
observation onto each price group instead of creating an unrelated `default`
group.

Use these fields when a probe page and its pricing endpoint use an already
registered format. If the probe pricing payload is unique, add a decoder under
`internal/pricing`, register its key, and reference that key in
`pricingAdapter`. Keep credentials in the encrypted session store; never place
tokens in adapter configuration or fixtures.
