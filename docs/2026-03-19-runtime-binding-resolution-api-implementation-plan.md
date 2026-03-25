---
date: 2026-03-19
author: Onur Solmaz <onur@textcortex.com>
title: Runtime Binding Resolution API Implementation Plan
tags: [spritz, runtime-binding, api, workload-identity, implementation-plan]
---

## Overview

This document defines a concrete internal API for reading the authoritative
runtime binding for a Spritz instance.

The goal is to let deployment-owned downstream services validate runtime
workload identity against Spritz control-plane state by `instance_id`, instead
of depending on extra pod-projected binding material.

This plan complements
[2026-03-19-runtime-service-delegation-architecture.md](2026-03-19-runtime-service-delegation-architecture.md)
by specifying the read path a downstream service can use before or during its
own token-exchange flow.

## Goals

- Make Spritz control-plane state the single source of truth for runtime
  binding.
- Expose a narrow internal API that returns canonical binding facts for one
  instance.
- Support downstream workload-identity validation using only:
  - the runtime workload token
  - the Spritz `instance_id`
- Avoid any requirement to inject a second runtime-binding token into the pod.
- Keep the API generic so multiple deployments can use it without adding
  deployment-specific billing or user-account semantics to Spritz.

## Non-goals

- Do not move deployment-specific billing, quotas, or entitlement checks into
  Spritz.
- Do not expose this API to browser clients or public internet callers.
- Do not require Spritz to mint deployment-specific user tokens.
- Do not add a second persistent runtime-binding store outside the canonical
  instance resource unless future scale or consistency needs justify it.
- Do not make images responsible for deciding which binding facts are
  authoritative.

## Problem statement

Spritz already resolves and stores the runtime binding facts needed for
workload-identity validation, but those facts are not currently available
through one explicit internal read contract.

Today:

- create-time resolver metadata mutations land on the instance resource
  metadata
- workload template propagation is driven by `spritz.spec.annotations`
- deployment-owned downstream services still need a canonical way to confirm:
  - which service account belongs to this instance
  - who owns the instance
  - which preset and instance class were resolved

This means downstream services need a direct control-plane lookup by
`instance_id`, instead of relying on duplicated binding material to be injected
into the pod.

## Source of truth

The canonical source of runtime-binding facts is the Spritz instance and its
typed control-plane state.

The initial implementation can derive the response from:

- resource namespace
- resource name
- `spritz.spec.owner`
- `spritz.spec.serviceAccountName`
- existing resolved annotations such as:
  - `spritz.sh/preset-id`
  - `spritz.sh/instance-class`

If Spritz later materializes typed runtime-principal or delegation state, this
API should read from that typed state without changing the external response
contract.

## API contract

Spritz should expose one internal endpoint:

```http
GET /internal/v1/runtime-bindings/{namespace}/{instanceId}
```

Request properties:

- internal service-to-service authentication only
- no browser session support
- no runtime workload token required on this read endpoint

Response shape:

```json
{
  "instanceId": "zeno-delta-breeze",
  "namespace": "spritz-production",
  "ownerPrincipal": {
    "id": "user-123",
    "type": "user"
  },
  "runtimePrincipal": {
    "authnMode": "workload_identity",
    "serviceAccountName": "zeno-agent-abcd1234"
  },
  "presetId": "zeno",
  "instanceClassId": "personal-agent"
}
```

Required fields:

- `instanceId`
- `namespace`
- `ownerPrincipal`
- `runtimePrincipal.authnMode`
- `runtimePrincipal.serviceAccountName`
- `presetId`
- `instanceClassId`

The response must contain only canonical binding facts. It must not include
deployment-specific pricing state, billing flags, or secret material.

## Downstream validation contract

This API does not replace workload-identity validation. It supports it.

A downstream service should:

1. validate the runtime workload token
2. extract runtime identity facts from that token, at minimum:
   - namespace
   - service account name
3. fetch the authoritative runtime binding from Spritz by `instance_id`
4. verify the workload token identity matches the binding returned by Spritz
5. continue with its own deployment-specific token exchange, entitlement
   checks, and usage accounting

This keeps Spritz responsible for canonical control-plane facts and leaves
deployment-specific authorization consequences to the downstream service.

## Auth and authorization

The endpoint should use the same internal authentication model as other Spritz
internal APIs.

The authorization rule should be narrow:

- only trusted internal callers may read runtime bindings
- callers may read bindings only for the specific namespace and instance they
  are validating

Spritz should not treat this endpoint as a general-purpose metadata export
surface.

## Errors

The endpoint should return clear, structured errors:

- `404` when the instance does not exist
- `409` when the instance exists but required runtime-binding facts are not yet
  materialized
- `403` when the caller is not allowed to read the binding
- `422` when the instance exists but the binding is invalid or incomplete

The response body should identify which required binding fact is missing or
invalid without leaking secret material.

## Implementation phases

### Phase 1

- add the internal resource and routing
- map current instance state into the runtime-binding response
- return `ownerPrincipal`, `runtimePrincipal`, `presetId`, and
  `instanceClassId`

### Phase 2

- add tighter authz scoping for trusted internal callers
- add structured logs and metrics for binding lookups
- add regression coverage for instances whose create-time metadata and
  workload-template metadata differ

### Phase 3

- wire runtime delegation exchange flows to use this endpoint as the canonical
  runtime-binding read path
- remove any remaining downstream dependence on pod-projected binding tokens

## Validation

Required validation before rollout:

- unit tests for mapping instance state into the API response
- resource tests for:
  - missing owner
  - missing service account
  - missing preset or instance class
  - not-found instances
- integration tests proving a downstream service can validate:
  - workload token namespace
  - workload token service account name
  against the binding returned by this endpoint
- regression test proving no pod-projected runtime-binding token is required
  for the control-plane lookup path

## References

- [2026-03-19-runtime-service-delegation-architecture.md](2026-03-19-runtime-service-delegation-architecture.md)
- [2026-03-19-unified-extension-framework-architecture.md](2026-03-19-unified-extension-framework-architecture.md)
