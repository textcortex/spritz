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

### Session exchange is the live resolver

The channel session exchange contract is the only operation allowed to turn a
logical installation into a bearer for provider message handling.

That contract must return one of two results:

- `resolved`: a bearer for a runtime that exists and is usable now
- `unavailable`: no live concierge is ready yet

It must not return `resolved` for a runtime that has not been verified.

## Invariants

### 1. Installation is durable; runtime is disposable

The installation record is the long-lived object. The runtime may be replaced
many times without changing the installation identity.

### 2. Resolved means live now

If session exchange returns `resolved`, all of the following must already be
true:

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

On every session exchange:

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

This does not replace live validation in session exchange. It reduces drift, but
session exchange must still verify runtime existence before returning success.

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

## Provider Rollout

Slack should be the first implementation of this lifecycle model.

Phase 1 for Slack should add:

- live validation in session exchange before returning `resolved`
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

## Follow-ups

- Decide whether shared concierges should default to durable or disposable
  runtime policy.
- Define the exact API shape for lifecycle invalidation callbacks where the
  deployment controller is external to Spritz.
- Decide whether stale idempotency invalidation should become a generic Spritz
  create rule, or remain scoped to shared concierge recovery.
