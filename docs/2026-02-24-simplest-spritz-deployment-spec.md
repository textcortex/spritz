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

## Decision Summary

- Keep core Spritz architecture.
- Change deployment defaults toward single-host Kubernetes serving.
- Move edge/path-routing complexity behind an optional advanced setup.
