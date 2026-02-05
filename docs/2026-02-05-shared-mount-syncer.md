---
date: 2026-02-05
author: Onur <onur@textcortex.com>
title: Shared Mount Syncer (Object Storage)
tags: [spritz, storage, shared-mounts, syncer]
---

## Overview

This document specifies a provider-agnostic way to share multiple directories across
Spritz pods without RWX storage. Object storage (GCS, S3, MinIO) is the source of truth,
and each pod syncs one or more shared mounts into local paths.

## Context

- RWX volumes are not guaranteed in many clusters.
- Spritz needs shared directories when multiple pods run for the same owner or org.
- Object storage is universally available but not POSIX, so we treat it as a versioned
  config store and sync locally.

## Design Summary

- Source of truth: object storage bucket.
- Writer: Spritz API (single writer).
- Readers: per-pod syncer (init + optional sidecar).
- Local target: `emptyDir` mounted at `/shared`.
- Each mount is materialized under `/shared/<mount-name>`.
- Workloads read from `/shared/<mount-name>/live`.

## Shared Mount Model

Shared mounts are configured as a list so the platform can define multiple shared
directories, each with its own scope and sync policy.

Config is just a mount named `config`; it has no special handling.

Example config (conceptual):

```yaml
sharedMounts:
  - name: config
    mountPath: /shared/config
    scope: owner
    mode: read-only
    syncMode: poll
    pollSeconds: 30
  - name: workspace
    mountPath: /shared/workspace
    scope: owner
    mode: snapshot
    syncMode: manual
```

Field expectations:

- `name`: stable identifier used in object storage paths.
- `mountPath`: absolute path inside the pod.
- `scope`: one of `owner`, `org`, `project`, `spritz`.
- `mode`: `read-only` or `snapshot` (no RWX semantics).
- `syncMode`: `poll` or `manual`.

## Storage Layout

Each mount has its own prefix and versioned bundle:

- `spritz-shared/<scope>/<scope-id>/<mount>/latest.json`
- `spritz-shared/<scope>/<scope-id>/<mount>/revisions/<revision>.tar.zst`

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
3. Extract into a temp dir (for example `/shared/<mount>/.incoming`).
4. Atomically swap:
   - `mv /shared/<mount>/.incoming /shared/<mount>/current`
   - `ln -sfn /shared/<mount>/current /shared/<mount>/live`

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
- `initContainer` runs the first sync for all mounts.
- `sidecar` keeps mounts current (optional, per mount).

## API Surface (Spec)

Internal endpoints for the syncer and writer:

- `GET /internal/v1/shared-mounts/{scope}/{scopeId}/{mount}/latest`
- `GET /internal/v1/shared-mounts/{scope}/{scopeId}/{mount}/revisions/{revision}`
- `PUT /internal/v1/shared-mounts/{scope}/{scopeId}/{mount}/revisions/{revision}`
- `PUT /internal/v1/shared-mounts/{scope}/{scopeId}/{mount}/latest`

Expected behavior:

- `latest` returns the JSON metadata plus checksum.
- `revisions` returns the tarball stream (or a signed URL).
- `latest` write must include `ifMatchRevision` and returns 409 on mismatch.

## Scopes and Authorization

Scope controls who can read or publish a mount:

- `owner`: access limited to a single user.
- `org`: access limited to org members.
- `project`: access limited to a project or team.
- `spritz`: access limited to a single Spritz resource.

The API enforces scope checks. Pods only receive credentials for their allowed scopes.

## Snapshot Writes (No RWX)

`snapshot` mode never exposes live RWX semantics. Writers publish bundles explicitly:

1. Client uploads a bundle to the API.
2. API writes the revision tarball.
3. API updates `latest.json` if the expected revision matches.

## Deprecated: Shared Config PVC

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
