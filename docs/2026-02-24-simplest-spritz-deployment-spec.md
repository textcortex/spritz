---
date: 2026-02-24
author: Spritz Contributors <user@example.com>
title: Simplest Spritz Deployment Specification
tags: [spritz, deployment, architecture]
---

## Overview

This document defines the default Spritz deployment model for the easiest
possible install by a new operator.

The default must avoid path-routing tricks, custom edge workers, and multi-origin
front-end hosting.

## Goals

- Make first deployment possible with one hostname and one Helm install.
- Keep UI and API in the same Kubernetes deployment surface.
- Minimize required configuration values.
- Keep advanced networking patterns optional.

## Non-goals

- Optimizing for existing multi-app domain/path routing.
- Requiring provider-specific edge features for default setup.
- Dropbox-grade conflict resolution in default storage mode.

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

- `host`: public Spritz host (example: `spritz.example.com`)
- `ingressClassName`: ingress class (or gateway class)
- `tls.issuerRef` (or pre-provisioned secret)
- `storageClass`: default PVC storage class

Everything else should have working defaults.

## Default Helm Values (Target)

```yaml
global:
  host: spritz.example.com

ingress:
  enabled: true
  className: nginx
  tls:
    enabled: true
    issuerRef:
      name: letsencrypt-prod
      kind: ClusterIssuer

ui:
  enabled: true
  basePath: /
  apiBaseUrl: /api

api:
  enabled: true
  basePath: /api

storage:
  homePvc:
    enabled: true
    storageClassName: standard
  sharedMounts:
    enabled: false
```

## Implementation Scope (Exact Changes)

### Helm Values and Compatibility

File: `helm/spritz/values.yaml`

- Add `global.host` with default `spritz.example.com`.
- Add `global.ingress.className` with default `nginx`.
- Add `global.ingress.tls.enabled` (default `true`).
- Add `global.ingress.tls.secretName` (default empty; operator-provided).
- Keep `ui.apiBaseUrl` default `/api`.
- Keep `ui.basePath` default `/`.
- Keep `ui.ingress.enabled` default `true` for single-host installs.
- Keep `operator.homePVC.enabled` default `true`.
- Keep `operator.sharedMounts.enabled` and `api.sharedMounts.enabled` default `false`.

Legacy keys that remain supported during transition:

- `ui.ingress.host`
- `ui.ingress.className`
- `ui.ingress.path`
- explicit `ui.apiBaseUrl`

### Helm Templates

Files:

- `helm/spritz/templates/ui-deployment.yaml`
- `helm/spritz/templates/api-deployment.yaml`
- `helm/spritz/templates/ui-api-ingress.yaml` (new)

Required behavior:

- Move ingress rendering out of `ui-deployment.yaml` into a dedicated template.
- Render one public ingress object with two ordered paths:
  - `/api` -> service `spritz-api` on `.Values.api.service.port`
  - `/` -> service `spritz-ui` on `.Values.ui.service.port`
- Source ingress class from `global.ingress.className`, fallback to `ui.ingress.className`.
- Source host from `global.host`, fallback to `ui.ingress.host`.
- Add TLS block when `global.ingress.tls.enabled` is true.
- Keep service names unchanged (`spritz-api`, `spritz-ui`) to avoid rollout risk.

### API Route Prefix Handling

File: `api/main.go`

- Register endpoints on both root and `/api` prefixes.
- Keep existing root routes for backward compatibility.
- Add `/api/healthz` alongside `/healthz` for path-based ingress health checks.

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

## Upgrade Behavior (Existing Installs)

- This is still prelaunch v1; defaults can be optimized for new installs.
- Existing installs can preserve legacy behavior by pinning old ingress keys.
- No CRD schema change is required for this deployment change.
- Existing Spritz custom resources are not mutated by chart upgrade.
- Home PVC default change applies to newly created Spritz resources after upgrade.

Compatibility precedence:

- API URL resolution:
  - explicit `ui.apiBaseUrl` wins
  - else use `<ui.basePath>/api` when `ui.basePath` is set
  - else use `/api`
- Host/class resolution:
  - use `global.host` and `global.ingress.className` when set
  - else fallback to `ui.ingress.host` and `ui.ingress.className`

## Operational Guardrails

Even in default mode, add these checks:

- Health endpoint checks for UI and API.
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
4. Create a devbox and open terminal.
5. Recreate the pod and verify home state persists.

Advanced mode validation should be a separate checklist.

## Test Matrix (Must Pass)

### Helm Render Checks

Run:

- `helm template spritz ./helm/spritz`
- `helm template spritz ./helm/spritz --set global.host= --set ui.ingress.host=legacy.example.com`

Pass criteria:

- Exactly one public ingress is rendered in default mode.
- Path `/api` routes to `spritz-api`.
- Path `/` routes to `spritz-ui`.
- Default host comes from `global.host`.
- Legacy host fallback works when `global.host` is empty.

### API Route Checks

Add tests in:

- `api/main_routes_test.go`

Assertions:

- `GET /healthz` returns 200.
- `GET /api/healthz` returns 200.
- Root and `/api` route variants hit identical auth and handler logic.

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

## Decision Summary

- Keep core Spritz architecture.
- Change deployment defaults toward single-host Kubernetes serving.
- Move edge/path-routing complexity behind an optional advanced setup.
