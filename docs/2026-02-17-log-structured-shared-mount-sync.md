---
date: 2026-02-17
author: Spritz Maintainers <user@example.com>
title: Log-Structured Shared Mount Sync (Dropbox-Style, No Separate DB)
tags: [spritz, shared-mounts, sync, object-storage, design]
---

## Overview

This document defines a robust sync architecture for Spritz shared mounts that:

- keeps object storage as the central source of truth,
- does not require a separate metadata database,
- uses ordered operations instead of full-folder snapshots,
- prevents deleted files from reappearing.

The model is Dropbox-like at the sync protocol level, but scoped to Spritz mounts.

## Problem Statement

Snapshot replacement can create sync loops and file resurrection:

- one pod applies a remote snapshot and emits new local filesystem events,
- those events produce a new snapshot publish,
- another pod applies that snapshot and repeats the cycle,
- deleted files can return if a stale snapshot wins a race.

To fix this, sync must move from "sync current folder image" to "sync ordered file operations with tombstones."

## Goals

- No silent data loss.
- No delete resurrection.
- Deterministic convergence across pods.
- Crash-safe replay and resume.
- No separate database service.

## Non-Goals

- Full POSIX shared filesystem semantics.
- Sub-second global consistency guarantees.
- Unlimited-history retention.

## High-Level Design

Use object storage as an append-only operation log plus blob store.

- File bytes: immutable blobs by content hash.
- Metadata/state: ordered commits per `owner + mount`.
- Current state pointer: single `head` object per `owner + mount`.

Each syncer:

1. detects local filesystem changes and turns them into operations,
2. publishes operations by appending commits with compare-and-swap head updates,
3. pulls and applies remote commits in strict order.

## Storage Layout

Example prefix:

```text
spritz-sync/v1/owners/{owner_id}/mounts/{mount_name}/
```

Objects:

- `heads/current.json`
- `commits/{seq:020d}-{commit_id}.json`
- `blobs/sha256/{hash}`

Example head payload:

```json
{
  "seq": 42,
  "commit_id": "c-01j0...",
  "updated_at": "2026-02-17T12:00:00Z"
}
```

Example commit payload:

```json
{
  "seq": 42,
  "commit_id": "c-01j0...",
  "parent_seq": 41,
  "parent_commit_id": "c-01jz...",
  "client_id": "pod-a",
  "op_id": "pod-a-000123",
  "created_at": "2026-02-17T12:00:00Z",
  "ops": [
    {
      "kind": "put",
      "path": ".config/tool/settings.json",
      "blob_hash": "sha256:...",
      "size": 512
    },
    {
      "kind": "delete",
      "path": ".config/tool/old.json",
      "tombstone": true
    }
  ]
}
```

## Core Rules

1. Never infer delete from absence.
   Delete is an explicit operation with tombstone semantics.
2. Apply commits strictly by sequence.
3. Ignore duplicate `(client_id, op_id)` operations (idempotency).
4. Commit ordering is authoritative for conflict resolution.
5. Remote apply must not trigger local republish (echo suppression).

## Write Protocol (No Separate DB)

For a syncer publishing local operations:

1. Read `heads/current.json` (get `seq`, `commit_id`, and object generation).
2. Prepare new commit with `seq = head.seq + 1` and parent fields.
3. Upload any missing blobs referenced by `put` operations.
4. Write commit object under `commits/...`.
5. Update `heads/current.json` with conditional write (generation match / CAS).
6. If CAS fails:
   - another writer advanced head first,
   - read new head, rebase pending ops, retry.

CAS on `head` provides single ordered history without a database.

## Read/Apply Protocol

Each syncer tracks local `last_applied_seq`.

1. Poll or watch head.
2. If `head.seq > last_applied_seq`, fetch missing commits in order.
3. Apply each operation atomically to local mount.
4. Persist `last_applied_seq` after successful commit apply.

If commit gaps are detected, syncer refetches from `last_applied_seq + 1`.

## Local Durable State

Each syncer keeps a local SQLite DB with at least:

- `last_applied_seq`
- known path index (path -> hash, tombstone flag)
- pending local operations queue
- dedupe keys `(client_id, op_id)`
- echo-suppression markers for recently applied remote changes

This makes restart/crash recovery deterministic.

## Echo Suppression

Remote apply changes local files. Watchers will see those writes.

Without suppression, remote apply can be re-published as a "new local" change.

Required behavior:

- tag applied operations in local DB,
- suppress watcher events that match recently applied remote operations,
- only publish true local-origin edits.

## Conflict Strategy

Preferred rule for v1:

- path-level last-writer-wins by commit sequence for canonical path state,
- when a local unacknowledged edit collides with newer remote state, create a conflict copy:
  - `<name>.conflict-<client_id>-<timestamp>`.

Never silently discard user-modified bytes.

## Delete and Tombstone Semantics

- `delete(path)` creates a tombstone in history.
- Tombstones prevent old content from being resurrected by stale clients.
- Tombstones may be garbage-collected after retention window, only if all active clients are past the tombstone sequence.

## Anti-Entropy Repair

Event streams can be missed. Add periodic reconciliation:

- syncer computes local manifest (path -> hash/tombstone),
- compares with state derived from commits up to `head.seq`,
- repairs drift by applying missing operations or full rehydrate.

This is a required safety net, not an optional feature.

## Security

- Use least-privilege credentials scoped to the mount prefix.
- Keep immutable blob objects write-once.
- Validate all paths to prevent traversal (no `..`, no absolute paths).
- Enforce size limits per operation and per commit.

## Migration from Snapshot Sync

1. Enter migration mode for target mount.
2. Materialize current mount state as initial commit (`seq=1` bootstrap).
3. Set head to bootstrap commit.
4. Enable log-based syncers with full protocol behavior enabled:
   - echo suppression,
   - deterministic hashing inputs,
   - commit/head/blob model,
   - CAS head updates,
   - local SQLite cursor/pending queue,
   - anti-entropy repair,
   - tombstone retention and GC policy.
5. Disable snapshot publish/apply loops.
6. Monitor convergence and rollback guardrails.

## Observability and SLO Signals

Track at minimum:

- publish success/failure rate,
- CAS retry count,
- apply lag (`head.seq - last_applied_seq`),
- conflict copy rate,
- resurrection detector count,
- anti-entropy repair count and duration.

Alert on sustained apply lag and repeated CAS storms.

## Test Matrix (Must Pass)

- create/modify/delete propagation across multiple pods,
- delete does not reappear after repeated sync cycles,
- duplicate publish retries are idempotent,
- crash/restart resumes from exact sequence,
- network partition and reconnect converges correctly,
- concurrent writers produce deterministic state plus conflict copies.

## Implementation Checklist

- implement head + commit + blob object model,
- enforce CAS head updates,
- add local SQLite cursor/pending queue,
- add watcher echo suppression,
- normalize deterministic hashing inputs,
- implement anti-entropy repair,
- define and enforce tombstone retention and GC rules,
- run full chaos and failure injection tests,
- cut over mount sync from snapshot mode to log mode.

## Summary

For Spritz shared mounts, object-storage-as-truth is viable without a separate database,
but only with log semantics:

- ordered commits,
- explicit tombstones,
- idempotent writes,
- strict apply ordering,
- echo suppression.

This is the minimum architecture needed to avoid sync loops and file resurrection.
