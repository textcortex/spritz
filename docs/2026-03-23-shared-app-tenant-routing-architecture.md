---
date: 2026-03-23
author: Onur Solmaz <onur@textcortex.com>
title: Shared App Tenant Routing Architecture
tags: [spritz, integrations, routing, slack, discord, teams, concierge, architecture]
---

## Overview

This document defines a Spritz-native architecture for serving many
tenant-dedicated agent instances behind one shared external app integration.

Examples:

- one shared Slack app installed into many workspaces
- one shared Discord app installed into many guilds
- one shared Teams app installed into many tenants or teams

In this model, each external tenant gets its own Spritz concierge, but all
tenants still talk through the same provider app registration. Spritz routes
each inbound event to the correct concierge by looking up the external tenant
binding instead of assuming one shared runtime per app.

The design must reuse the existing Spritz control-plane concepts:

- service principals
- owner references
- presets
- instance classes
- admission resolvers
- normal instance lifecycle

Spritz may expose `Concierge` as a first-class native concept, but it should be
implemented using the same underlying primitives instead of creating a second
runtime model.

## Goals

- Support one shared Slack, Discord, or Teams app serving many tenants.
- Route each tenant to its own dedicated Spritz concierge.
- Make `Concierge` a first-class Spritz concept.
- Reuse normal Spritz instance creation instead of inventing a separate
  concierge runtime system.
- Let deployments keep using existing owner-resolution and preset-resolution
  flows during install or bootstrap.
- Keep the inbound routing contract portable enough to work across Slack,
  Discord, Teams, and future shared-app providers.

## Non-goals

- Making Spritz responsible for account linking outside the existing
  resolver-based model.
- Introducing a second runtime or materialization stack separate from
  instances.
- Encoding billing, agent selection policy, or deployment-specific ownership
  rules in Spritz core.
- Replacing normal instance creation with a separate bot-only provisioning
  path.

## Problem Statement

Today, a shared external app identity naturally maps to one integration entry
point, but not to one tenant.

That breaks down when:

- the same Slack app is installed into many workspaces
- the same Discord app is present in many guilds
- the same Teams app serves many organizations

In those cases, the provider app identity is not enough to decide which
concierge should handle an event. The missing key is the external tenant scope.

Spritz therefore needs a first-class way to bind:

- a provider integration
- plus an external tenant identifier
- to one target concierge

## Design Principles

### Concierge is first-class, but not special under the hood

Spritz may expose `Concierge` as a named product concept because many
deployments will use it.

But `Concierge` should still be implemented as a thin control-plane wrapper
around the existing instance model.

Creating a concierge should internally:

- resolve owner and preset inputs through the normal admission framework
- create a normal instance from a preset and instance class
- store a concierge record that points to that instance
- store the tenant routing identity for inbound events

### Reuse instance creation as-is

Tenant-specific app entry points should still be materialized as normal
instances created from presets and instance classes.

The install flow may resolve owners, selectors, or preset inputs through the
existing admission framework, but the runtime that actually executes must still
be an ordinary Spritz instance.

### Owner resolution and tenant routing are different

The owner tells Spritz who should own or administer the concierge and its
backing instance.

The external tenant identifier tells Spritz which concierge should receive an
inbound provider event.

These concerns must remain separate. Routing must not depend on recomputing
owner identity on every inbound event.

### One active concierge exists per external tenant

The routing contract should be deterministic.

For a given combination of:

- integration principal
- provider
- external tenant identifier

Spritz should have exactly one active concierge at a time.

### Concierge should survive runtime replacement

The tenant-facing identity should not be the same thing as the current runtime
instance.

That lets Spritz:

- replace or recreate the runtime
- keep the same external tenant binding
- preserve stable operational references for the tenant entry point

## Canonical Terms

### Shared app principal

The authenticated Spritz principal representing the shared provider app
integration.

### External tenant ID

The provider-scoped identifier that determines where an inbound event belongs.

Examples:

- Slack workspace ID
- Discord guild ID
- Teams tenant or tenant-plus-team key

Spritz should treat this as an opaque provider-scoped identifier.

For providers that need more than one dimension of scope, Spritz should store
an explicit `externalScopeType` next to the ID instead of overloading the ID
field itself.

### Concierge

A first-class Spritz control-plane object representing one tenant-facing entry
agent for an external tenant.

A concierge is stable across runtime replacements. It owns the tenant binding
and points at the current backing instance.

### Backing instance

The normal Spritz instance that implements the concierge runtime.

This may host OpenClaw or any other preset.

### Routing index

An index over concierge state used to resolve inbound events quickly.

This is derived state. `Concierge` remains the source of truth.

## Proposed Architecture

The native Spritz model should add two things:

- a first-class `Concierge` resource
- inbound routing as a new integration layer

The control-plane pieces become:

- existing service principal authentication for the shared app
- a concierge store
- existing create flow for instance creation
- a routing index over concierge state
- a new inbound route-resolution step
- a provider webhook or event ingress surface that resolves the concierge
  before forwarding to the concierge's backing instance

### Concierge model

Spritz should persist a concierge record with at least:

- `id`
- `principalId`
- `provider`
- `externalTenantId`
- `externalScopeType`
- `ownerId`
- `presetId`
- `instanceClassId`
- `instanceId`
- `status`
- `createdAt`
- `updatedAt`

Optional but useful fields:

- `displayName`
- provider installation metadata
- deployment annotations
- reconnect or reinstall metadata

The important split is:

- `Concierge` is the stable tenant-facing control-plane record
- `instanceId` is the replaceable runtime implementation
- `principalId + provider + externalScopeType + externalTenantId` is the
  durable concierge identity

Spritz should enforce a real uniqueness constraint for active concierge rows on:

- `principalId + provider + externalScopeType + externalTenantId`

### Concierge lifecycle states

At minimum, concierge state should support:

- `provisioning`
- `ready`
- `replacing`
- `disconnected`
- `failed`

Routing should only target concierges in `ready`.

### Routing index model

Spritz may maintain a separate routing index for fast lookup, but it should be
derived from concierge state instead of being a peer source of truth.

That index should map:

- `principalId + provider + externalScopeType + externalTenantId`

to:

- `conciergeId`
- `instanceId`
- current routable status

### Install flow

The install or bootstrap flow should work like this:

1. A shared provider app is installed into an external tenant.
2. The integration calls Spritz with:
   - provider
   - external tenant ID
   - external scope type
   - owner reference or resolved owner
   - preset and preset inputs
3. Spritz performs a deterministic upsert keyed by:
   - `principalId`
   - `provider`
   - `externalScopeType`
   - `externalTenantId`
4. If no active concierge exists, Spritz creates one and runs the normal create path under the
   hood:
   - owner resolution if needed
   - preset create resolvers if configured
   - instance class policy
   - instance materialization
5. If an active concierge already exists for the same key, Spritz returns or
   updates that concierge instead of creating a duplicate.
6. Spritz updates the derived routing index from concierge state.

This keeps provisioning and routing aligned around one canonical concierge
object while still reusing the canonical instance runtime underneath.

### Reinstall and retry behavior

Reinstall, reconnect, and retried create requests must be idempotent.

Rules:

- if the same active concierge already exists, return or update it
- if a disconnected concierge exists, reconnect it or replace its backing
  instance deterministically
- concurrent install attempts for the same key must converge on one concierge
  record

### Inbound event flow

The inbound routing flow should work like this:

1. Spritz receives a webhook or normalized event from a shared app principal.
2. Spritz authenticates the principal.
3. Spritz extracts the provider, external scope type, and external tenant ID
   from the event.
4. Spritz resolves the active concierge through the routing index.
5. Spritz loads the bound concierge.
6. Spritz forwards the event to the concierge's backing instance.
7. The backing instance handles the event through its normal runtime surface.

Routing must be based on concierge identity, not on repeated owner lookup.

### Concierge replacement flow

Because concierge and instance are separate, Spritz can replace a broken or
upgraded runtime without changing the tenant-facing identity.

That flow should work like this:

1. Spritz creates a new backing instance for the existing concierge.
2. Spritz waits until the new instance is healthy.
3. Spritz atomically updates `concierge.instanceId`.
4. Spritz refreshes the derived routing index.
5. Spritz drains and removes the old instance afterward.

## API Direction

Spritz should add a `Concierge` API, but it does not need a new runtime type.
It needs:

- a concierge lifecycle API
- an inbound routing API

Possible native surfaces:

- a concierge create or upsert API
- a concierge lookup API
- provider-specific ingress endpoints that normalize incoming events and resolve
  the bound concierge

The exact REST shape may vary, but the control-plane contract should be:

- create concierge
- materialize backing instance through normal create
- route inbound event by external tenant ID

Example create shape:

```json
{
  "provider": "discord",
  "externalScopeType": "guild",
  "externalTenantId": "123456789012345678",
  "ownerRef": {
    "type": "external",
    "provider": "discord",
    "subject": "987654321098765432"
  },
  "presetId": "openclaw"
}
```

## Relationship to Existing Extension Architecture

This model should reuse the current extension framework rather than add another
feature-specific hook family.

Recommended split:

- existing `owner.resolve` and `preset.create.resolve` style resolvers remain
  responsible for create-time concierge facts
- a new inbound routing operation resolves the target concierge from
  `principal + provider + externalScopeType + externalTenantId`

This keeps the lifecycle clean:

- create-time extensions answer "which concierge backing instance should
  exist?"
- inbound routing answers "which existing concierge should receive this event?"

## Why This Avoids Concept Duplication

This design does not duplicate:

- concierge runtime behavior
- owner semantics
- preset semantics
- instance class semantics
- runtime instance semantics

It adds only two missing control-plane concepts:

- `Concierge` as the stable tenant-facing entry object
- a derived routing index for shared-app ingress

Both compile down to the existing runtime primitives instead of introducing a
parallel execution model.

## Validation

Validation for this architecture should include:

1. Install the same shared provider app into two different external tenants.
2. Create or bootstrap one concierge for each tenant.
3. Verify each concierge materializes a normal backing instance.
4. Verify Spritz stores two distinct concierge identities keyed by
   `principalId + provider + externalScopeType + externalTenantId`.
5. Send inbound events from both tenants through the same app integration.
6. Verify each event routes to the correct concierge and backing instance.
7. Verify concierge replacement updates `instanceId` without changing the
   concierge identity.
8. Verify reinstall or reconnect updates the binding deterministically.
9. Verify uninstall or disconnect disables routing for that tenant.

## Follow-ups

- Define the concierge storage model and API contract.
- Define whether the routing index is persisted or rebuilt from concierge rows.
- Define the normalized `externalScopeType` and `externalTenantId` shapes for
  Slack, Discord, and Teams.
- Define the inbound routing operation contract in the unified extension
  framework.
- Decide whether provider ingress normalization lives in core or in optional
  provider integration packages.

## References

- [2026-03-12-external-identity-resolution-api-architecture.md](2026-03-12-external-identity-resolution-api-architecture.md)
- [2026-03-19-unified-extension-framework-architecture.md](2026-03-19-unified-extension-framework-architecture.md)
- [2026-03-11-external-provisioner-and-service-principal-architecture.md](2026-03-11-external-provisioner-and-service-principal-architecture.md)
