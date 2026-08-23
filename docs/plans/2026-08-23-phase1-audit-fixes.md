# Phase 1 Audit Fixes Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Close the three Phase 1 audit gaps: schema validation, durable scheduler updates, and stale match cleanup.

**Architecture:** Keep validation lightweight and dependency-free in `internal/adapter`. Persist the next schedule using a short independent context after collection. Delete matches for models marked removed inside the collection transaction.

**Tech Stack:** Go, SQLite, standard library JSON/schema handling, Go tests.

---

### Task 1: Add config schema validation

**Files:**
- Modify: `internal/adapter/config.go`
- Test: `internal/adapter/config_test.go`

Implement validation for known properties: JSON type, enum, minimum, and maximum. Preserve unknown keys and explicit values, but return a descriptive error for invalid known values.

Run: `go test ./internal/adapter -count=1`

### Task 2: Make scheduler state write-back durable

**Files:**
- Modify: `internal/scheduler/scheduler.go`
- Test: `internal/scheduler/scheduler_test.go`

Use a short independent context for `GetSite` and `SetSiteNextRun` so parent collection cancellation does not prevent schedule persistence. Add a focused regression test for canceled collection context.

Run: `go test ./internal/scheduler -count=1`

### Task 3: Remove matches for models removed by complete catalogs

**Files:**
- Modify: `internal/store/store.go`
- Test: `internal/store/store_test.go`

When complete-catalog absence evidence marks models removed, delete their `model_matches` rows in the same transaction. Add a regression test verifying removal after the third omission leaves no matches.

Run: `go test ./internal/store -count=1`

### Task 4: Full verification and commit

Run `go test ./...`, `go vet ./...`, and `gofmt -l .`; inspect the diff and commit the fixes with a focused message.
