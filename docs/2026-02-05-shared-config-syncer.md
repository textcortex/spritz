---
date: 2026-02-05
author: Onur <onur@textcortex.com>
title: Shared Config Syncer (Object Storage)
tags: [spritz, storage, config, syncer]
---

## Overview

This document describes a provider-agnostic approach for sharing per-user config across
multiple Spritz pods without RWX storage. Object storage (GCS, S3, MinIO) is the source
of truth, and each pod syncs a local copy into a shared mount.

## Context

- RWX volumes are not guaranteed in many clusters.
- Spritz needs a shared config directory when multiple pods run for the same owner.
- Object storage is universally available but not POSIX, so we treat it as a versioned
  config store and sync locally.

## Design Summary

- Source of truth: object storage bucket.
- Writer: Spritz API (single writer).
- Readers: per-pod syncer (init + optional sidecar).
- Local target: `emptyDir` mounted at `/shared`.
- Workloads read from `/shared/live`.

## Storage Layout

Each owner has a prefix and a versioned bundle:

- `spritz-config/<owner-id>/latest.json`
- `spritz-config/<owner-id>/revisions/<revision>.tar.zst`

Example `latest.json`:

```json
{
  "revision": "2026-02-05T10-30-00Z",
  "checksum": "sha256:...",
  "updated_at": "2026-02-05T10:30:00Z"
}
```

## Syncer Behavior

Init (startup):

1. Fetch `latest.json` via the API.
2. Download the tarball from object storage.
3. Extract into a temp dir (for example `/shared/.incoming`).
4. Atomically swap:
   - `mv /shared/.incoming /shared/current`
   - `ln -sfn /shared/current /shared/live`

Sidecar (optional polling):

- Poll `latest.json` every N seconds.
- If `revision` changes, repeat the init flow and swap.

## Write Path (Conflict Control)

Only the API writes to object storage. Clients must include the expected revision:

- If expected revision != current revision, reject with 409.
- This avoids concurrent write conflicts and makes updates explicit.

## Provider-Agnostic Options

Two portable options keep the implementation cloud-agnostic:

- S3-compatible API (preferred): use one client across GCS, S3, MinIO by swapping
  endpoint and credentials.
- `rclone`: single binary that supports GCS, S3, and many other providers.

## Kubernetes Wiring (High Level)

- `emptyDir` volume mounted at `/shared`.
- `initContainer` runs the first sync.
- `sidecar` keeps it current (optional, based on how dynamic the config is).

## Deprecated: Shared Config PVC

The shared config PVC approach is sunsetted and is **not** planned for implementation
in the near future. Any existing PVC code should be treated as legacy and not enabled
for new deployments. Use the object-storage syncer above.

## Validation

- Start two pods for the same owner and confirm `/shared/live` matches.
- Update config via API and confirm both pods switch revisions.
- Kill and restart a pod; it should restore the latest revision on boot.

## References

- Platform doc: `docs/spritz/2026-01-24-shared-config-syncer.md`
