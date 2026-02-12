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

This is snapshot replication, not a POSIX shared filesystem. Writes are published as
bundles and applied by replacing the mount contents.

## Context

- RWX volumes are not guaranteed in many clusters.
- Spritz needs shared directories when multiple pods run for the same owner or org.
- Object storage is universally available but not POSIX, so we treat it as a versioned
  config store and sync locally.

## Design Summary

- Source of truth: object storage bucket.
- Writer: Spritz API (single writer).
- Readers: per-pod syncer (init + optional sidecar).
- Local target: `emptyDir` mounted per mount path (configurable per mount).
- Each mount is materialized directly under `<mountPath>`.
- Workloads read/write from `<mountPath>`.
- Snapshot mounts publish bundles on filesystem changes (watcher + debounce).
- Sync mode `poll` uses long-polling so updates apply quickly without aggressive polling.

## Shared Mount Model

Shared mounts are configured as a list so the platform can define multiple shared
directories, each with its own scope and sync policy.

Config is just a mount named `config`; it has no special handling.

Per-spritz configuration is supported. Each Spritz can set its own mount list
and mount paths. If no mounts are provided in the Spritz spec, the operator can
fall back to a platform default list.

Mount definitions are supplied via the Spritz spec (name + mountPath + scope).
Bucket names and credentials are platform-managed; users do not bring their own
buckets.

Example config (conceptual):

```yaml
sharedMounts:
  - name: config
    mountPath: /home/dev/.config
    scope: owner
    mode: snapshot
    syncMode: manual
  - name: workspace
    mountPath: /home/dev/workspace
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
  - `poll` keeps the mount current by applying the latest revision.
  - `manual` only applies the latest revision at startup.
  - Snapshot mounts may still publish local changes when `mode: snapshot`.
- `pollSeconds`: max wait time for long-poll requests when `syncMode: poll`.
- `publishSeconds`: safety interval for checking/publishing changes when `mode: snapshot`.

## Storage Layout

Each mount has its own prefix and versioned bundle:

- `spritz-shared/<scope>/<scope-id>/<mount>/latest.json`
- `spritz-shared/<scope>/<scope-id>/<mount>/revisions/<revision>.tar.gz`

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
3. Extract into a temp dir (for example `<mountPath>/.incoming-<id>`).
4. Atomically replace the mount contents by swapping extracted entries into `<mountPath>`.

Sidecar (sync mode `poll`):

- The syncer long-polls `latest.json` (blocking up to `pollSeconds`).
- If `revision` changes, repeat the init flow and replace the mount contents.
- If nothing changes before the long-poll timeout, the syncer immediately reconnects.

This yields near-instant updates without RWX storage.

Sidecar (publish for snapshot mounts):

- A filesystem watcher watches the mount root.
- Changes are debounced (coalesced) to avoid publishing on every write.
- When a bundle checksum differs from the current checksum, the syncer uploads a new
  revision and advances `latest.json`.
- A periodic publish tick (`publishSeconds`) is retained as a safety net in case the
  watcher misses events.

## Write Path (Conflict Control)

Only the API writes to object storage. Clients must include the expected revision:

- If expected revision != current revision, reject with 409.
- This avoids concurrent write conflicts and makes updates explicit.

## Provider-Agnostic Options

We keep the storage layer provider-agnostic, but standardize on rclone in the
initial rollout.

- `rclone` (current): one binary that supports GCS, S3, and many providers.
- S3-compatible API (future): use one client across GCS, S3, MinIO by swapping
  endpoint and credentials.

## Initial Implementation Decisions

- Driver: `rclone`.
- Scope: `owner` only.
- Write mode: shared mounts are writable inside the pod, and the syncer publishes
  snapshots on change. No RWX semantics are exposed.

## Kubernetes Wiring (High Level)

- One `emptyDir` volume per mount, mounted at `mountPath`.
- `initContainer` runs the first sync for all mounts.
- `sidecar` keeps mounts current (optional, per mount).

When using per-spritz mounts:

- The Spritz spec supplies `sharedMounts`.
- The operator uses those mounts for that pod.
- The syncer receives the mount list via env JSON (`SPRITZ_SHARED_MOUNTS`).

## API Surface (Spec)

Internal endpoints for the syncer and writer:

- `GET /internal/v1/shared-mounts/owner/{ownerId}/{mount}/latest`
- `GET /internal/v1/shared-mounts/owner/{ownerId}/{mount}/revisions/{revision}`
- `PUT /internal/v1/shared-mounts/owner/{ownerId}/{mount}/revisions/{revision}`
- `PUT /internal/v1/shared-mounts/owner/{ownerId}/{mount}/latest`

Expected behavior:

- `latest` returns the JSON metadata plus checksum.
- `latest` supports long-poll via query params:
  - `waitSeconds=<int>`: block up to N seconds if not modified.
  - `ifNoneMatchRevision=<revision>` (or `If-None-Match` header): current revision.
  - Response is `304 Not Modified` if unchanged before timeout.
- `revisions` returns the tarball stream (or a signed URL).
- `latest` write must include `ifMatchRevision` and returns 409 on mismatch.

## Scopes and Authorization

Scope controls who can read or publish a mount:

- `owner`: access limited to a single user.
- `org`: access limited to org members.
- `project`: access limited to a project or team.
- `spritz`: access limited to a single Spritz resource.

The API enforces scope checks. Pods only receive credentials for their allowed scopes.

Initial implementation scope:

- `owner` only.
  - Other scopes are defined here but not implemented yet.

## Internal Auth (Current)

The internal endpoints are protected by a shared bearer token:

- Syncer sends `Authorization: Bearer <SPRITZ_INTERNAL_TOKEN>`.
- API checks the token on all `/internal/v1/...` endpoints.

This is intentionally simple and works for initial rollout, but it is coarse:

- Any process with the token can read or write any owner scope within the same
  environment.
- Staging/production remain isolated because each environment has separate tokens
  and buckets.

## Recommended Long-Term Auth (Best Practice)

Replace the shared token with a short-lived, per-workspace token that is scoped
to one owner (and optionally one mount).

Preferred model:

1. Operator mints a JWT per workspace pod with claims:
   - `owner_id`
   - `mount` (optional)
   - `exp` (short TTL, 30-60 minutes)
   - `pod_uid` (optional)
2. Token is stored only in that pod (projected secret).
3. API verifies JWT with a public key.
4. API enforces `owner_id` to match the request path.
5. Operator rotates tokens periodically.

Alternative (Kubernetes-native):

- Use Bound ServiceAccount tokens and `TokenReview` in the API.
- Map pod identity to `owner_id` using labels or annotations.

## Snapshot Writes (No RWX)

`snapshot` mode never exposes live RWX semantics. Writers publish bundles explicitly:

1. Client uploads a bundle to the API.
2. API writes the revision tarball.
3. API updates `latest.json` if the expected revision matches.

For Spritz pods, the shared mount is writable. The syncer sidecar uses a filesystem
watcher to publish quickly when content changes, with a periodic safety tick.

## Deprecated: Shared Config PVC

The shared config PVC approach is sunsetted and is **not** planned for implementation
in the near future. Any existing PVC code should be treated as legacy and not enabled
for new deployments. Use the object-storage syncer above.

## GCS Uniform Bucket-Level Access (Important)

When using GCS buckets with Uniform Bucket-Level Access (UBLA) enabled, rclone must
be configured for bucket-policy mode. Otherwise uploads fail with:

- `googleapi: Error 400: Cannot insert legacy ACL for an object when uniform bucket-level access is enabled`

Required rclone config:

```ini
[gcs]
type = google cloud storage
provider = GCS
env_auth = true
bucket_policy_only = true
```

Equivalent environment override (if you do not want to embed it in `rclone.conf`):

- `RCLONE_GCS_BUCKET_POLICY_ONLY=true`

If this is missing, shared mount revision writes return 500 and cross-pod sync does not
advance, even though the syncer loop is running.

## Validation

- Start two pods for the same owner and confirm `mountPath` matches.
- Update config via API and confirm both pods switch revisions.
- Kill and restart a pod; it should restore the latest revision on boot.

## References

- Platform doc: `docs/spritz/2026-01-24-shared-config-syncer.md`
