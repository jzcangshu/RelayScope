# RelayPulse Product Specification

## Purpose

RelayPulse answers two practical questions for users of LinuxDo community relay sites:

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

## Initial sites

Display names may initially fall back to the site's own title or hostname and can be edited in the console.

| Source URL | Initial adapter family | Known note |
| --- | --- | --- |
| https://elysiver.h-e.top/pricing | NewAPI pricing | Reference implementation |
| https://new-api.abrdns.com/status | NewAPI embedded probe | Batch status endpoint |
| https://status.chybenzun.top/ | Custom probe | Cloudflare-protected HTTP |
| https://metapi.lilililwan.xyz/pricing | Unknown pending probe | Connection currently closes |
| https://api.42w.shop/pricing | NewAPI pricing | Full catalog is available behind page pagination |
| https://api.fengwind.com/model-market | Model market | Login required |
| http://v4.whyyin.cn:28327/pricing | Pending discovery | User has opened page in Chrome |
| https://52ccl.net/pricing | Pending discovery | User has opened page in Chrome |
| https://ai.121628.xyz/pricing | Pending discovery | User has opened page in Chrome |
| https://aitoken.forum/pricing | Pending discovery | User has opened page in Chrome |
| https://api.llm.pm/pricing | Pending discovery | User has opened page in Chrome |
| https://demo.dev2.mulink.top/pricing | Pending discovery | User has opened page in Chrome |
| https://v-api.de5.net/pricing | Pending discovery | User has opened page in Chrome |
| https://jianzhile.vip/console/model-status | Authenticated NewAPI model probe + pricing | Login required; prices from https://jianzhile.vip/pricing |
| https://sub2.pigeonw.com/monitor | Authenticated Sub2API channel monitor | Login required |

## Initial monitored model families

- DeepSeek: `deepseek-v4-flash`, `deepseek-v4-pro`, `deepseek-v4-flash-0731`, `deepseek-v4-pro-0731`
- GLM: `glm-5`, `glm-5.1`, `glm-5.2`
- MiniMax: `minimax-m2.5`, `minimax-m2.7`, `minimax-m3`
- Kimi: `kimi-k2.5`, `kimi-k2.6`, `kimi-k2.7`, `kimi-k3`
- MiMo: `mimo-v2.5`, `mimo-v2.5-pro`
- OpenAI: `gpt-5.5`, `gpt-5.6-luna`, `gpt-5.6-terra`, `gpt-5.6-sol`
- Google: `gemini-3.7-flash`, `gemini-3.6-flash`, `gemini-3.5-flash`, `gemini-3.5-flash-lite`, `gemini-3.1-flash-lite`, `gemini-3.1-pro-preview`, `gemini-3.1-pro-preview-customtools`, `gemini-3-flash-preview`, `gemini-2.5-pro`, `gemini-2.5-flash`, `gemini-2.5-flash-lite`
- xAI: `grok-4.3`, `grok-4.5`
- Anthropic: every combination of `claude-{opus,sonnet,haiku}-{4-6,4-7,4-8,5}`, plus `claude-fable-5`

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
