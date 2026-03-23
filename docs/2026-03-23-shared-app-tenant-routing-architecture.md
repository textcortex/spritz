---
date: 2026-03-23
author: Onur Solmaz <onur@textcortex.com>
title: Shared App Tenant Routing Architecture
tags: [spritz, integrations, routing, slack, discord, teams, architecture]
---

## Overview

This document defines a Spritz-native architecture for serving many
tenant-dedicated agent instances behind one shared external app integration.

Examples:

- one shared Slack app installed into many workspaces
- one shared Discord app installed into many guilds
- one shared Teams app installed into many tenants or teams

In this model, each external tenant gets its own Spritz instance, but all
tenants still talk through the same provider app registration. Spritz routes
each inbound event to the correct instance by looking up the external tenant
binding instead of assuming one shared instance per app.

The design must reuse the existing Spritz control-plane concepts:

- service principals
- owner references
- presets
- instance classes
- admission resolvers
- normal instance lifecycle

Spritz must not add a new product-specific "concierge" concept. A concierge is
just a normal Spritz instance that happens to be the tenant entry point for a
deployment.

## Goals

- Support one shared Slack, Discord, or Teams app serving many tenants.
- Route each tenant to its own dedicated Spritz instance.
- Reuse normal Spritz instance creation instead of inventing a separate bot or
  concierge resource.
- Let deployments keep using existing owner-resolution and preset-resolution
  flows during install or bootstrap.
- Keep the inbound routing contract generic enough to work across Slack,
  Discord, Teams, and future shared-app providers.

## Non-goals

- Defining product-specific agent behavior inside Spritz.
- Making Spritz responsible for account linking outside the existing
  resolver-based model.
- Introducing a new "concierge instance" resource type.
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

In those cases, the provider app identity is not enough to decide which Spritz
instance should handle an event. The missing key is the external tenant scope.

Spritz therefore needs a first-class way to bind:

- a provider integration
- plus an external tenant key
- to one target instance

## Design Principles

### Reuse instance creation as-is

Tenant-specific app entry points should still be normal instances created from
presets and instance classes.

The install flow may resolve owners, selectors, or preset inputs through the
existing admission framework, but the result must still be an ordinary Spritz
instance.

### Owner resolution and tenant routing are different

The owner tells Spritz who should own or administer the instance.

The external tenant key tells Spritz which instance should receive an inbound
provider event.

These concerns must remain separate. Routing must not depend on recomputing
owner identity on every inbound event.

### One active tenant binding routes to one instance

The routing contract should be deterministic.

For a given combination of:

- integration principal
- provider
- external tenant key

Spritz should have one active route target at a time.

### Provider-specific support is acceptable

Spritz may natively support Slack, Discord, Teams, and similar providers.

What must remain out of Spritz core is deployment-specific product logic such
as pricing, agent semantics, or organization-specific ownership rules.

## Canonical Terms

### Shared app principal

The authenticated Spritz principal representing the shared provider app
integration.

### External tenant key

The provider-scoped identifier that determines where an inbound event belongs.

Examples:

- Slack workspace ID
- Discord guild ID
- Teams tenant or tenant-plus-team key

Spritz should treat this as an opaque provider-scoped string or structured key.

### Tenant binding

The durable Spritz record that maps one external tenant key to one target
instance.

### Tenant entry instance

The normal Spritz instance that receives routed inbound events for that tenant.

This may host OpenClaw or any other preset. Spritz does not need a separate
concierge concept for it.

## Proposed Architecture

The native Spritz model should add tenant binding and inbound routing as a new
integration layer.

The control-plane pieces become:

- existing service principal authentication for the shared app
- existing create flow for instance creation
- a new tenant-binding store
- a new inbound route-resolution step
- a provider webhook or event ingress surface that resolves the tenant binding
  before forwarding to the instance

### Tenant binding model

Spritz should persist a binding record with at least:

- `principalId`
- `provider`
- `externalTenantKey`
- `instanceId`
- `ownerId`
- `presetId`
- `status`
- `createdAt`
- `updatedAt`

Optional but useful fields:

- provider installation metadata
- last inbound event timestamp
- uninstall or disconnect metadata
- annotations for deployment-owned context

The durable uniqueness rule should be:

- one active binding per `principalId + provider + externalTenantKey`

### Install flow

The install or bootstrap flow should work like this:

1. A shared provider app is installed into an external tenant.
2. The integration calls Spritz with:
   - provider
   - external tenant key
   - owner reference or resolved owner
   - preset and preset inputs
3. Spritz runs the normal create path:
   - owner resolution if needed
   - preset create resolvers if configured
   - instance class policy
   - instance materialization
4. Spritz writes a tenant binding that points the external tenant to the newly
   created instance.

This keeps provisioning and routing aligned around one canonical instance.

### Inbound event flow

The inbound routing flow should work like this:

1. Spritz receives a webhook or normalized event from a shared app principal.
2. Spritz authenticates the principal.
3. Spritz extracts the provider and external tenant key from the event.
4. Spritz resolves the active tenant binding.
5. Spritz forwards the event to the bound instance.
6. The instance handles the event through its normal runtime surface.

Routing must be based on the tenant binding, not on repeated owner lookup.

## API Direction

Spritz does not need a new instance type. It needs a binding and routing API.

Possible native surfaces:

- a tenant-binding create or upsert API
- a tenant-binding lookup API for operational tooling
- provider-specific ingress endpoints that normalize incoming events and resolve
  the bound instance

The exact REST shape may vary, but the control-plane contract should be:

- create instance through normal create
- bind external tenant to instance
- route inbound event by external tenant

## Relationship to Existing Extension Architecture

This model should reuse the current extension framework rather than add another
feature-specific hook family.

Recommended split:

- existing `owner.resolve` and `preset.create.resolve` style resolvers remain
  responsible for create-time facts
- a new inbound routing operation resolves the target instance from
  `principal + provider + externalTenantKey`

This keeps the lifecycle clean:

- create-time extensions answer "which instance should exist?"
- inbound routing answers "which existing instance should receive this event?"

## Why This Avoids Concept Duplication

This design does not duplicate:

- owner semantics
- preset semantics
- instance class semantics
- runtime identity semantics

It adds only the missing concept that existing Spritz primitives do not yet
cover:

- external tenant to instance binding for shared-app ingress

That is the minimum new concept required to support one shared app with many
tenant-dedicated instances.

## Validation

Validation for this architecture should include:

1. Install the same shared provider app into two different external tenants.
2. Create or bootstrap one tenant entry instance for each tenant.
3. Verify Spritz stores two distinct tenant bindings.
4. Send inbound events from both tenants through the same app integration.
5. Verify each event routes to the correct bound instance.
6. Verify reinstall or reconnect updates the binding deterministically.
7. Verify uninstall or disconnect disables routing for that tenant.

## Follow-ups

- Define the tenant-binding storage model and API contract.
- Define the normalized external tenant key shape for Slack, Discord, and Teams.
- Define the inbound routing operation contract in the unified extension
  framework.
- Decide whether provider ingress normalization lives in core or in optional
  provider integration packages.

## References

- [2026-03-12-external-identity-resolution-api-architecture.md](2026-03-12-external-identity-resolution-api-architecture.md)
- [2026-03-19-unified-extension-framework-architecture.md](2026-03-19-unified-extension-framework-architecture.md)
- [2026-03-11-external-provisioner-and-service-principal-architecture.md](2026-03-11-external-provisioner-and-service-principal-architecture.md)
