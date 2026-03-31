---
date: 2026-03-31
author: Onur Solmaz <onur@textcortex.com>
title: Instance Replacement Rollout Architecture
tags: [spritz, instance, replacement, rollout, revision, architecture]
---

## Overview

This document defines a generic Spritz primitive for replacing one live
instance with another revision of the same logical workload.

The main use case is not limited to one product. Any deployment may need to:

- refresh a long-lived instance after a new image or preset revision
- replace a broken instance while preserving external ownership
- run controlled rollout from one revision to another

Spritz should provide the generic replacement primitive. Deployment-specific
systems should decide which instances to roll and when.

## Goals

- Support `create new -> verify ready -> cut over -> delete old`.
- Give external controllers a deterministic, idempotent replacement contract.
- Make the target revision explicit and inspectable.
- Keep deployment-specific rollout policy outside Spritz.
- Avoid forcing callers to script raw create, probe, and delete sequences.

## Non-goals

- Do not add deployment-specific concepts such as customers, billing, or
  concierge registries.
- Do not define "replace all instances of type X" in Spritz.
- Do not move external installation ownership into Spritz.
- Do not delete the old instance before the replacement is ready.

## Core Model

Spritz should distinguish between:

- the current live instance
- the target revision requested by the caller
- the replacement candidate created for that revision
- the replacement progress as observed through the replacement instance state

The replacement primitive does not decide who should use it. It only guarantees
safe replacement semantics for one instance at a time.

## Revision Contract

Every replace request should carry one opaque `targetRevision` string.

Spritz should treat that value as caller-owned metadata:

- compare it for equality
- store it on the replacement instance annotations
- expose it on replacement status

Spritz should not interpret product meaning from the revision string.

Spritz should persist `targetRevision` on the replacement instance as an
annotation so later reads can report it without extra state:

- `spritz.sh/target-revision`

## Replacement Primitive

Spritz should expose one internal API for replacing an instance, for example:

```text
POST /api/internal/v1/spritzes/{namespace}/{instanceId}:replace
```

Suggested request fields:

- `targetRevision`
- `idempotencyKey`
- `replacement`

`replacement` should be the normal internal create payload for the new
instance, using the same schema as `POST /api/internal/v1/spritzes`, except the
caller should omit `idempotencyKey`. Spritz should derive the child create key
as:

```text
replace:<idempotencyKey>
```

This makes the replace API implementable without requiring Spritz to infer a
new spec from the source instance or from the opaque revision string.

Suggested behavior:

1. load the source instance
2. search for an existing replacement instance with matching source lineage and
   replacement idempotency key
3. if found, return that replacement instance
4. otherwise create a replacement candidate using the existing create path and
   the supplied `replacement` payload
5. attach `targetRevision` and source lineage metadata to the replacement
6. return both source and replacement bindings to the caller

This should be a `create before destroy` primitive by default.

Source-lineage annotations on the replacement instance:

- `spritz.sh/replacement-source-namespace`
- `spritz.sh/replacement-source-name`
- `spritz.sh/replacement-idempotency-key`
- `spritz.sh/target-revision`

## What Spritz Should Return

The response should include enough information for an external controller to
perform cutover safely:

- source namespace and instance id
- replacement namespace and instance id
- replacement target revision
- replacement phase
- `replayed`

If the same `idempotencyKey` is replayed, the caller should get the same
replacement instance back.

### Exact response shape

```json
{
  "status": "success",
  "data": {
    "source": {
      "namespace": "example-ns",
      "instanceId": "zeno-old"
    },
    "replacement": {
      "namespace": "example-ns",
      "instanceId": "zeno-new",
      "targetRevision": "sha256:...",
      "phase": "Provisioning",
      "ready": false
    },
    "replayed": false
  }
}
```

HTTP contract:

- `202 Accepted` when replacement exists but is not yet ready
- `200 OK` when the replayed or newly created replacement is already `Ready`
- `404` when the source instance does not exist
- `409` when the replacement request is incompatible with a previous use of the
  same `idempotencyKey`

## Cutover Ownership

Spritz should not assume it owns external routing tables.

That means:

- Spritz creates the replacement candidate
- Spritz reports when the replacement is ready
- the external controller decides when to cut traffic over
- the external controller decides when to delete the old instance

This keeps Spritz generic for deployments where routing authority lives
elsewhere.

Polling contract:

- the caller should poll the normal instance read endpoint for the replacement
  instance
- no separate replacement-operation resource is required in the first
  implementation
- replacement is ready when the replacement instance phase is `Ready`

## Invariants

- the old instance remains intact until the replacement is ready
- failed replacement must not delete a healthy source instance
- replacement is idempotent per request key
- replacement records lineage to the source instance
- readiness must be explicit, not inferred from create success alone

## Optional Helper APIs

The replacement primitive is enough on its own, but these helpers would make
external controllers simpler:

- a delete-and-wait helper for the old instance
- revision metadata on normal instance read responses

These helpers should remain generic and instance-scoped.

## Why This Belongs In Spritz

Replace-one-instance is a lifecycle primitive, not a deployment policy.

It belongs in Spritz because it is useful for:

- shared channel concierges
- personal agent runtimes
- hosted tools or workers that need revision refresh

What does not belong in Spritz is the higher-level question of which instances
should be replaced during a deployment.

## Validation

The primitive is good enough when an external controller can:

1. request a replacement for one instance
2. observe the replacement become ready
3. switch its own external binding to the replacement
4. delete the old instance
5. retry the same request idempotently after a crash

## Related Docs

- `docs/2026-03-31-shared-channel-concierge-lifecycle-architecture.md`
- deployment-specific rollout controllers may layer on top of this primitive
  without adding product-specific policy into Spritz
