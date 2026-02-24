---
date: 2026-02-24
author: Spritz Contributors <user@example.com>
title: Simplest Spritz Deployment Specification
tags: [spritz, deployment, architecture]
---

## Overview

This document defines the default Spritz deployment model for the easiest
possible install by a new operator.

The default must avoid path-routing tricks, custom edge workers, multi-origin
front-end hosting, and backward-compatibility branches.

## Target End State

- One hostname, one ingress, one Helm install.
- One routing model:
  - `/` -> `spritz-ui`
  - `/api` -> `spritz-api`
- API served only under `/api/*` (no root API routes).
- UI uses `/api` as its API base in default deployment mode.
- One canonical ingress config surface under `global.ingress`.
- No legacy fallback keys in the default chart path.

## Goals

- Make first deployment possible with one hostname and one Helm install.
- Keep UI and API in the same Kubernetes deployment surface.
- Minimize required configuration values.
- Keep advanced networking patterns outside the default path.
- Keep defaults stable and production-oriented for standalone installs.

## Non-goals

- Optimizing for existing multi-app domain/path routing in default mode.
- Requiring provider-specific edge features for default setup.
- Dropbox-grade conflict resolution in default storage mode.
- Preserving old/alternate ingress key paths.

## Default Deployment Model

### Topology

- `spritz-ui` and `spritz-api` run in Kubernetes.
- Single public host, for example `spritz.example.com`.
- Ingress/Gateway routes:
  - `/` -> `spritz-ui`
  - `/api` -> `spritz-api`

### Why this is the default

- No external frontend hosting dependency.
- No cross-origin CORS/env drift for standard installs.
- No edge-worker route forwarding required.
- Easier debugging: one host, one ingress path map.

## Required Operator Inputs

The default installation should require only:

- `global.host`: public Spritz host (example: `spritz.example.com`)
- `global.ingress.className`: ingress class
- `global.ingress.tls.enabled`: whether TLS is enabled
- `global.ingress.tls.secretName` (optional): pre-provisioned TLS secret name
- `operator.homePVC.storageClass` (optional): home PVC storage class override

Everything else should have working defaults.

## Default Helm Values (Target)

```yaml
global:
  host: spritz.example.com
  ingress:
    className: nginx
    tls:
      enabled: true
      secretName: ""

ui:
  ingress:
    enabled: true
  apiBaseUrl: /api

operator:
  homePVC:
    enabled: true
    storageClassName: standard

  sharedMounts:
    enabled: false

api:
  sharedMounts:
    enabled: false
```

## Implementation Scope (Exact Changes)

### Helm Values (Strict v1)

File: `helm/spritz/values.yaml`

- Add `global.host` with default `spritz.example.com`.
- Add `global.ingress.className` with default `nginx`.
- Add `global.ingress.tls.enabled` (default `true`).
- Add `global.ingress.tls.secretName` (default empty; operator-provided).
- Keep `ui.ingress.enabled` default `true` for single-host installs.
- Keep `ui.apiBaseUrl` default `/api`.
- Keep `operator.homePVC.enabled` default `true`.
- Keep `operator.sharedMounts.enabled` and `api.sharedMounts.enabled` default `false`.
- Remove compatibility-only keys from the default path:
  - `ui.ingress.host`
  - `ui.ingress.className`
  - `ui.ingress.path`
  - `ui.basePath`

### Helm Templates

Files:

- `helm/spritz/templates/ui-deployment.yaml`
- `helm/spritz/templates/ui-api-ingress.yaml` (new)

Required behavior:

- Move ingress rendering out of `ui-deployment.yaml` into a dedicated template.
- Render one public ingress object with two ordered paths:
  - `/api` -> service `spritz-api` on `.Values.api.service.port`
  - `/` -> service `spritz-ui` on `.Values.ui.service.port`
- Source ingress class only from `global.ingress.className`.
- Source host only from `global.host`.
- Add TLS block when `global.ingress.tls.enabled` is true.
- Keep service names unchanged (`spritz-api`, `spritz-ui`) to avoid rollout risk.

### API Route Prefix Handling

File: `api/main.go`

- Register API and internal endpoints only under `/api`.
- Expose health check at `/api/healthz`.
- Remove root-prefixed API routes from the public server surface.

### UI Runtime Behavior

Files:

- `helm/spritz/templates/ui-deployment.yaml`
- `ui/entrypoint.sh`

Required behavior:

- Default runtime API base is `/api`.
- Do not require base-path routing logic for default standalone mode.

## Storage and Sync Defaults

- Default mode is per-devbox persistent home PVC.
- Shared cross-devbox live sync is disabled by default.
- Shared mounts remain available as an opt-in advanced feature.

Rationale:

- PVC-only mode has fewer failure modes.
- This is enough for most single-devbox usage.
- Operators can enable shared sync only when they need it.

## Optional Advanced Mode

Advanced mode can support:

- Path mounting under another app host (example: `/spritz`).
- Edge worker route forwarding.
- SNI override and custom origin hostnames.
- Shared live sync across multiple devboxes.

These are explicitly optional and should be documented separately from the
default install flow.

## Backward Compatibility Policy

- No backward compatibility contract is required for this prelaunch baseline.
- Remove compatibility paths instead of carrying long-term dual behavior.
- If values are renamed/removed, operators must adopt the new canonical keys.
- No CRD schema change is required for this deployment-focused work.

## Operational Guardrails

Even in default mode, add these checks:

- Health endpoint checks for UI and `/api/healthz`.
- TLS handshake check on the configured public host.
- Alert on repeated `5xx` from ingress.

If advanced mode is enabled, add:

- DNS drift detection for origin hostnames.
- Edge-to-origin TLS checks.
- Alerting for edge handshake failures.

## Validation Checklist

After install:

1. Open `https://spritz.example.com`.
2. Confirm UI loads from `/`.
3. Confirm API health at `/api/healthz`.
4. Confirm root API endpoint path is not served (for example `/healthz` is not used as the API health path).
5. Create a devbox via `/api/spritzes` and open terminal.
6. Recreate the pod and verify home state persists.

Advanced mode validation should be a separate checklist.

## Test Matrix (Must Pass)

### Helm Render Checks

Run:

- `helm template spritz ./helm/spritz`

Pass criteria:

- Exactly one public ingress is rendered in default mode.
- Path `/api` routes to `spritz-api`.
- Path `/` routes to `spritz-ui`.
- Default host comes from `global.host`.
- Ingress class comes from `global.ingress.className`.

### API Route Checks

Add tests in:

- `api/main_routes_test.go`

Assertions:

- `GET /api/healthz` returns 200.
- `GET /healthz` is not the canonical health path for API routing.
- Secured API handlers are served under `/api`.
- Root-prefixed API paths are not part of default route surface.

Run:

- `(cd api && go test ./...)`

### Smoke and Guardrail Checks

Run:

- `./e2e/local-smoke.sh`
- `./scripts/verify-agnostic.sh`
- `npx -y @simpledoc/simpledoc check`

Pass criteria:

- Spritz reaches `Ready` in local smoke.
- No provider-specific values are introduced.
- Documentation conventions pass.

## Remaining Work (Now)

No additional functional cleanup is required for the strict standalone target.

Current code paths are aligned to:

- UI at `/`
- API at `/api/*`
- Canonical ingress config under `global.ingress`
- No runtime `basePath` compatibility plumbing in UI assets/entrypoint/chart

Known pre-existing non-blocker:

- `./scripts/verify-agnostic.sh` currently fails on
  `operator/controllers/home_pvc_test.go` due an existing fixture value
  (`"spritz/app"`), unrelated to this deployment model implementation.

## Decision Summary

- Keep core Spritz architecture.
- Use a strict single-host Kubernetes deployment default.
- Keep API under `/api` and UI under `/`.
- Keep edge/path-routing complexity outside the default deployment model.
