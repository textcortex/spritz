---
date: 2026-03-31
author: Onur Solmaz <onur@textcortex.com>
title: Shared Channel Concierge Lifecycle Architecture
tags: [spritz, concierge, channel-gateway, slack, discord, teams, lifecycle, architecture]
---

## Overview

This document defines the lifecycle model for shared channel concierges behind
one shared provider gateway.

It covers the failure mode where a deployment still has a valid installation
record, but the live concierge runtime has already been deleted, expired, or
otherwise become unusable.

This architecture is provider-agnostic. Slack is the first implementation, but
the same model should apply to Discord, Teams, and future shared channel
providers.

This document focuses on shared channel concierges because that is where
deployment-owned installation state and provider routing meet. It is not
intended to invent concierge-only liveness semantics. The guarantees below
about live binding validation, stale binding invalidation, recovery
serialization, and `resolved` meaning live now should belong to the normal
interactive instance model as well. Shared concierges add external tenant
routing and provider installation metadata on top of that base lifecycle.

Related docs:

- `docs/2026-03-23-shared-app-tenant-routing-architecture.md`
- `docs/2026-03-24-slack-channel-gateway-implementation-plan.md`
- `docs/2026-03-30-channel-gateway-provider-status-updates.md`

## Provider Coverage

This lifecycle model is not Slack-specific.

It applies to any shared provider app where:

- one shared app installation surface receives events for many external tenants
- each external tenant should have its own durable logical concierge
- replies or actions go back out through the same shared provider gateway

Initial examples:

- Slack workspaces
- Discord guilds
- Teams tenants or teams

The same contract should remain valid for future shared provider apps without
introducing Slack-specific assumptions into the lifecycle rules.

## Problem

Shared channel concierge routing currently depends on state that can drift:

- a deployment-owned installation record
- a live Spritz runtime object
- create-time idempotency reservations
- provider gateway retry and recovery logic

When those pieces disagree, the gateway can receive a runtime identity that is
already dead. That creates the worst possible outcome:

- the route still looks resolved
- the next Spritz call fails with `spritz not found`
- recovery may replay stale create state instead of creating a fresh concierge
- the provider user sees a broken concierge even though installation still
  exists

The root issue is conceptual:

- the installation is a durable logical object
- the runtime is an expendable live realization of that object

The control plane must model those as different things.

## Goals

- Define one authoritative contract for resolving a shared concierge into a
  live runtime.
- Make runtime deletion, expiry, and recreation safe and predictable.
- Prevent completed idempotency replay from reviving dead runtime names.
- Keep the lifecycle model generic across Slack, Discord, Teams, and similar
  providers.
- Preserve the existing product concept of one concierge per external tenant.

## Non-goals

- Adding a separate top-level `Concierge` Kubernetes resource.
- Moving deployment-specific installation storage into Spritz.
- Defining provider UX copy in this document beyond lifecycle requirements.
- Replacing the existing shared gateway pattern with provider-specific runtimes.

## Generic Vs. Concierge-Specific Behavior

The following guarantees should be generic across interactive Spritz instances,
not special-case concierge behavior:

- a stored runtime binding is only a hint until live validation succeeds
- `resolved` means the runtime is routable now
- dead-runtime replay from stale idempotency state is invalid
- only one recovery may be in flight for one logical target at a time
- runtime deletion or terminal state invalidates the current binding

Shared channel concierges add these extra concerns on top:

- external tenant routing identity
- deployment-owned installation state
- provider installation metadata and outbound gateway send path

This document does not require identical idle-expiry policy for every instance
type. It does require the same reachability contract whenever an instance is
treated as routable for interactive traffic.

## Core Model

### Logical concierge installation

The durable object is the installation binding for one routing identity:

- `principalId`
- `provider`
- `externalScopeType`
- `externalTenantId`

That object owns:

- installation connectivity
- owner identity
- preset or class choice
- provider installation metadata
- optional last known runtime binding

### Live runtime binding

The live runtime binding is the current Spritz runtime that realizes the
installation right now.

That binding is not durable truth. It is a cacheable lease that must be
validated before use.

The stored runtime identifier may become stale because:

- the runtime expired
- the runtime was deleted
- the runtime was replaced
- the runtime never finished provisioning

### One live resolver, many interfaces

There should be one authoritative live resolver that turns a logical target
into the current live runtime binding.

Channel session exchange is one interface built on top of that resolver because
provider message handling needs a bearer and provider auth. Other interactive
paths should call the same live resolver rather than reimplementing equivalent
readiness, recovery, and stale-binding logic.

Any interface layered on top of the live resolver must preserve the same
outcomes:

- `resolved`: a bearer for a runtime that exists and is usable now
- `unavailable`: no live concierge is ready yet

It must not return `resolved` for a runtime that has not been verified.

## Invariants

### 1. Installation is durable; runtime is disposable

The installation record is the long-lived object. The runtime may be replaced
many times without changing the installation identity.

### 2. Resolved means live now

If the live resolver, or any interface layered on top of it, returns
`resolved`, all of the following must already be true:

- the runtime object exists
- the runtime is in a usable phase
- the returned bearer authorizes access to that exact runtime binding

### 3. Stored runtime IDs are hints, not authority

A persisted `instanceId` or runtime name may be reused as a fast path, but only
after live validation succeeds.

### 4. Completed idempotency replay to a missing runtime is stale

If create replay points at a runtime name that no longer exists, that replay is
not a success case. It is stale state and must be treated as invalid.

### 5. Runtime deletion must invalidate the binding

When Spritz learns that the runtime has been deleted, expired, or entered a
terminal lifecycle, the live binding for that installation must stop being
considered healthy.

## Resolution Contract

### Inputs

The resolver takes a routing identity:

- `principalId`
- `provider`
- `externalScopeType`
- `externalTenantId`

### Output states

`resolved`

- installation exists
- a live runtime exists
- the runtime is usable now
- bearer and provider auth are returned

`unavailable`

- installation exists
- the system may still be creating or recovering the concierge
- no live runtime is ready yet

`unresolved`

- installation does not exist
- or it is disconnected
- or required provider auth is missing

### Required algorithm

On every live-resolution request:

1. Load the durable installation.
2. If disconnected or missing provider auth, return `unresolved`.
3. If a last-known runtime binding exists, validate that it still exists and is
   usable.
4. If validation succeeds, return `resolved`.
5. If validation fails because the runtime is missing or terminal:
   - mark the binding stale
   - clear or invalidate stale replay state if it still points at the dead
     runtime
   - create or resume recovery for a fresh runtime
6. Return `resolved` only after the new runtime is actually ready.
7. Otherwise return `unavailable`.

The critical point is step 5:

- missing runtime must not fall back to returning the old runtime name
- missing runtime must not rely on completed idempotency replay that still
  points at that dead runtime

## Idempotency Rules

Create idempotency remains required, but its semantics must be tightened for
shared concierges.

### Completed reservation with missing runtime

If a completed reservation points at runtime `X` and runtime `X` is gone:

- the reservation is stale
- replay must not return `X`
- a fresh runtime must be created or the reservation must be rewritten to the
  new live runtime

### Pending reservation with missing runtime

If a pending reservation names runtime `X` and runtime `X` is gone:

- recovery may continue only if the create transaction can still produce a live
  runtime
- otherwise the reservation must be moved forward or invalidated

### Consequence

Idempotency storage cannot be the sole source of truth for whether a concierge
still exists.

## Lifecycle Notifications

Reverse invalidation is required.

When a shared concierge runtime enters a terminal lifecycle:

- expired
- deleted
- terminating without replacement
- failed beyond recovery policy

Spritz should notify the deployment-owned installation controller so the cached
runtime binding is marked stale immediately.

This does not replace live validation in the resolver. It reduces drift, but
every interface layered on top of the live resolver must still verify runtime
existence before returning success.

## Shared Concierge Policy

Shared channel concierges should be treated as durable logical service entry
points, even if their backing runtimes are replaceable.

That means deployments must choose one of two supported policies explicitly:

### Durable runtime policy

- shared concierges use long or no idle expiry
- runtime deletion should be rare
- recovery is still supported for exceptional cases

### Disposable runtime policy

- shared concierges may expire or be recreated routinely
- recovery must be fast enough that provider users do not observe broken
  routing
- provider gateways may show recovery status, but only after the lifecycle
  contract above is followed

Either policy is acceptable. What is not acceptable is mixing disposable
runtime behavior with durable routing assumptions.

## Implementation Decisions

The following decisions are pinned down by this document and should be treated
as the default implementation contract across Slack, Discord, Teams, and future
shared provider apps.

### 1. Default policy

Shared channel concierges should default to the durable runtime policy.

This is a policy decision for shared provider entry points, not a separate
reachability contract that regular instances do not get.

That means:

- shared concierges should use long or no idle expiry by default
- routine expiry of a healthy shared concierge is not the normal operating mode
- deployments may opt into disposable runtime behavior only explicitly

Regular interactive instances may still choose different expiry policy, but
they should share the same live validation, stale-binding invalidation,
recovery serialization, and readiness gate before they are treated as routable.

### 2. Recovery concurrency

Recovery must be serialized per logical installation.

That means:

- only one recovery operation may be active for a given routing identity at a
  time
- concurrent calls through any interface for the same installation must observe
  the same in-flight recovery and return `unavailable` until it completes
- recovery must replace the live binding atomically when the new runtime is
  ready

### 3. Stale idempotency handling

Stale completed idempotency state should remain historical state, not be
rewritten in place to look like the new recovery result.

That means:

- a completed reservation that points at a missing runtime must be marked stale
- replay must not return that dead runtime name
- fresh recovery must use a new recovery attempt and a new idempotency key
- the old stale reservation may remain for auditability, but it is no longer a
  valid replay source

### 4. Runtime invalidation callback contract

Lifecycle invalidation callbacks should carry enough information to invalidate a
binding safely without clearing a newer replacement binding by mistake.

Minimum required fields:

- routing identity: `principalId`, `provider`, `externalScopeType`,
  `externalTenantId`
- observed runtime identity: the runtime name or equivalent binding identity
- reason: `expired`, `deleted`, `terminating`, or `failed`
- `observedAt`

Required rule:

- invalidate the cached binding only if the observed runtime identity still
  matches the current live binding for that installation

### 5. Readiness gate for `resolved`

`resolved` means the runtime is actually routable now.

Minimum requirements:

- the runtime object exists
- the runtime is in `Ready` phase, or the implementation's exact equivalent
  serving phase
- the runtime is not `Pending`, `Creating`, `Terminating`, `Expired`, or
  `Failed`
- any required serving condition for interactive traffic is true before the
  bearer is returned
- the returned bearer is bound to that exact validated runtime

Interactive readiness may still have more than one layer.

For example:

- a runtime may be live enough to satisfy `resolved`
- the first ACP prompt path may still need a short retry window before it can
  accept work

That distinction must stay inside the normal availability or recovery logic.
It must not turn ordinary slow first-prompt behavior into a fake
runtime-recovery signal.

## Provider Rollout

Slack should be the first implementation of this lifecycle model.

Phase 1 for Slack should add:

- live validation in the shared resolver before returning `resolved`
- stale-binding invalidation when the runtime is missing
- stale completed idempotency detection for missing concierge names
- lifecycle notification wiring from Spritz back to the deployment-owned
  installation controller

The same contract should then be reused for:

- Discord guild concierges
- Teams tenant or team concierges
- future provider-specific shared app gateways

## Validation

Before this architecture is considered implemented, verify:

1. A healthy installation resolves to a live concierge without recreation.
2. A missing concierge never returns `resolved` with the dead runtime name.
3. A missing concierge causes recovery to create a fresh runtime.
4. A completed idempotency reservation pointing to a missing runtime is treated
   as stale.
5. A terminal runtime lifecycle event invalidates the cached binding.
6. Gateway status messages only appear during actual recovery, not on slow
   healthy prompts.
7. The same contract works for provider-specific routing identities beyond
   Slack.
8. The same live-binding validation and readiness rules are reused by ordinary
   interactive instances, with concierge-specific logic limited to tenant
   routing and provider installation state.

## Follow-ups

- Decide whether stale idempotency invalidation should become a generic Spritz
  create rule, or remain scoped to shared concierge recovery.
