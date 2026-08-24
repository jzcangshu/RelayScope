# Deployment sizing

**English** | [中文](deployment-sizing.zh-CN.md)

This document describes the measured resource envelope and the assumptions
behind the supported deployment sizes. It is a sizing guide, not a promise
that arbitrary adapters or challenge workloads fit the same envelope.

## Measured production baseline

The reference deployment was measured on 2026-08-24 with the release service
running on Debian 13:

| Item | Measured value |
| --- | ---: |
| Sites / enabled / session-required | 36 / 31 / 10 |
| Collector HTTP concurrency | 3 |
| Collection interval | 15 minutes |
| Collections in the last 24 hours | 2,586 |
| Metric buckets written in the last 24 hours | 79,392 |
| SQLite file | 90.5 MB |
| Raw models / site groups | 2,122 / 1,698 |
| RelayScope RSS during sampling | 42-53 MB |
| RelayScope observed cgroup peak | 58.6 MB |
| RelayScope CPU during a 60-second idle/normal sample | 1.7-1.9% of one host CPU |
| Public health endpoint latency | 0.3-0.7 ms locally |
| FlareSolverr RSS during sampling | 106 MB |
| FlareSolverr observed cgroup peak | 1.07 GB |

The production host had 8 vCPUs, 8 GB RAM, 2 GB swap, and a 40 GB disk. The
service itself is deliberately limited to three concurrent site HTTP
operations and four SQLite connections; the host had substantial spare
capacity during the measurement.

## Recommended sizes

### RelayScope only

For public collection without FlareSolverr or Chromium:

- **Minimum:** 1 vCPU, 512 MB RAM, 2 GB free disk, 1 GB swap.
- **Recommended:** 1-2 vCPU, 1 GB RAM, 10 GB free disk, 1-2 GB swap.
- **Comfortable:** 2 vCPU, 2 GB RAM, 20 GB free disk for larger catalogs,
  backups, and logs.

The 512 MB minimum assumes roughly 40 sites, default concurrency, normal
JSON payloads, and no other memory-heavy service on the host. It is an
operational floor, not a target for headroom.

### RelayScope with FlareSolverr

Treat FlareSolverr and its browser processes as a separate workload:

- **Minimum:** 2 vCPU, 2 GB RAM, 4 GB swap, 10 GB free disk.
- **Recommended:** 2-4 vCPU, 4 GB RAM, 20 GB free disk.

Do not run Chromium/FlareSolverr on a 768 MB host. The measured FlareSolverr
peak exceeded 1 GB even though its steady-state RSS was about 106 MB. Keep the
existing 1 GB `MemoryHigh` and 1.5 GB `MemoryMax` limits and monitor OOM kills.

## Capacity model

With interval `I` minutes and `N` enabled sites, the nominal collection rate is

`collections/day = N * 1440 / I`.

At 15 minutes, 40 sites produce at most 3,840 collections/day. The reference
deployment produced 2,586/day because disabled, failed, and delayed sites do
not all complete every nominal run.

The scheduler currently selects at most 100 due sites per dispatch. The
documented operating target is therefore **up to roughly 40-60 sites** at the
default interval. Before exceeding 100 enabled/due sites, measure collection
duration and backlog on the actual adapters; increasing HTTP concurrency raises
both network pressure and memory use.

SQLite storage is bounded by the 72-hour history cleanup, but the file does not
automatically shrink after deleting rows. Reserve space for the current file,
WAL/checkpoint overhead, and backups. A practical budget is:

`disk budget = 10 * current database size + 2 * largest backup + 2 GB logs/system`

For the measured 90.5 MB database, this is still well below 5 GB. Use 10 GB
free disk as the recommended floor so growth, upgrades, and recovery copies do
not become an outage risk.

## Scaling signals

Increase resources or reduce site load when any of these persist for 15 minutes:

- `running` collection runs remain near the scheduler's 3-minute timeout;
- due sites are repeatedly deferred across scheduler ticks;
- RSS exceeds 70% of the host memory limit;
- SQLite WAL or database writes remain blocked by `database is locked` errors;
- FlareSolverr reaches `MemoryHigh` or is killed by the OOM manager.

The first scaling action should be to reduce site frequency or isolate
FlareSolverr. Increase collector concurrency only after measuring remote-site
rate limits and CPU/memory headroom.
