---
date: 2026-02-09
author: Onur <onur@textcortex.com>
title: Spritz User Config Subset
tags: [spritz, ui, api, spec]
---

## Overview

End users need a safe way to customize spritzes without editing the full CR spec.
This document defines a **user-editable config subset** (`userConfig`) that the
API validates and merges into a server-owned template.

## Goals

- Let users adjust a small, safe set of settings.
- Keep security-sensitive fields server-owned.
- Make validation deterministic and server-side.
- Support UI YAML editing without exposing full spec controls.

## Non-goals

- Full CR spec editing in the UI.
- Allowing users to pick storage buckets or credentials.
- Exposing cluster-level settings like namespaces or service accounts.

## User Config Schema

`userConfig` is an object with these allowed fields.

| Field | Type | Notes |
| --- | --- | --- |
| `image` | string | Allowed only when policy permits custom images. |
| `repo` | object | `url`, `branch`, `dir`, `revision`, `depth`, `submodules`. |
| `ttl` | string | Duration like `8h` or `30m`. |
| `env` | list | Key/value list, subject to allowlist. |
| `resources` | object | CPU/memory (allowed only when enabled; no caps enforced by default). |
| `sharedMounts` | list | Same shape as `spec.sharedMounts`. |

## Policy and Validation

The API enforces a **UserConfigPolicy** that defines what is allowed.
The policy is server-owned and can vary by environment.

Policy controls (env-configurable):

- `SPRITZ_USER_CONFIG_ALLOW_IMAGE` + `SPRITZ_USER_CONFIG_ALLOWED_IMAGE_PREFIXES`
- `SPRITZ_USER_CONFIG_ALLOW_REPO`
- `SPRITZ_USER_CONFIG_ALLOW_TTL` + `SPRITZ_USER_CONFIG_MAX_TTL`
- `SPRITZ_USER_CONFIG_ALLOW_ENV` + `SPRITZ_USER_CONFIG_ALLOWED_ENV_KEYS` + `SPRITZ_USER_CONFIG_ALLOWED_ENV_PREFIXES`
- `SPRITZ_USER_CONFIG_ALLOW_RESOURCES`
- `SPRITZ_USER_CONFIG_ALLOW_SHARED_MOUNTS` + `SPRITZ_USER_CONFIG_ALLOWED_MOUNT_ROOTS` (default `/home/dev,/workspace`)

Notes:

- Shared mounts are restricted to `scope=owner`.

Validation is always server-side. UI validation is helpful but not trusted.

## Merge Rules

1. Start from a server-managed template.
2. Apply `userConfig` fields in a fixed order.
3. Validate the resulting spec.
4. Write the final spec into the Spritz CR.

If validation fails, the API returns a clear error and the spec is unchanged.

## Storage and Audit

The API stores `userConfig` on the Spritz resource for auditability in
`metadata.annotations["spritz.sh/user-config"]` as JSON.

The server remains the only writer of sensitive spec fields.

## API Surface

Recommended endpoints:

- `POST /spritzes` accepts `userConfig` on create.

## UI Behavior

The UI should expose a YAML/JSON editor for `userConfig` only. JSON is
recommended for nested objects like `resources`.
This keeps power-user flexibility while retaining guardrails.

## Validation Notes for Shared Mounts

- `mountPath` must be absolute.
- `mountPath` must be under allowed roots.
- `scope` must be `owner` until new scopes are implemented.

## References

- `docs/2026-02-05-shared-mount-syncer.md`
