# ADR 0001: Modular monolith with compiled adapters

Status: accepted

## Decision

Use one Go binary, SQLite in WAL mode, embedded static frontend assets, and compiled adapter modules registered through a stable internal interface.

Do not use runtime Go plugins, microservices, Redis, a queue, or a dedicated time-series database.

## Rationale

The target machine is resource constrained, collection volume is modest, data is retained for only three days, and site adapters share authentication, challenge handling, matching, observability, and persistence. A modular monolith minimizes idle memory and operational failure modes while preserving clear integration boundaries.

Runtime Go plugins have platform and toolchain coupling and are unnecessary. A compiled registry still lets adapters be developed, tested, configured, enabled, and disabled independently.

## Revisit when

Measured database write contention, deployment isolation, or adapter trust boundaries cannot be resolved inside one process.

