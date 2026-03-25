---
date: 2026-03-23
author: Onur Solmaz <onur@textcortex.com>
title: Shared App Tenant Routing Architecture
tags: [spritz, integrations, routing, slack, discord, teams, concierge, architecture]
---

## Overview

This document defines a Spritz-native architecture for serving many
tenant-dedicated agent runtimes behind one shared channel gateway integration.

Examples:

- one shared Slack app installed into many workspaces
- one shared Discord app installed into many guilds
- one shared Teams app installed into many tenants or teams

In this model, each external tenant gets its own concierge instance, but all
tenants still talk through the same shared channel gateway. Spritz routes
each inbound event to the correct concierge instance by looking up the
instance's routing identity instead of assuming one shared runtime per app.

In plain language:

- one shared Slack app, Discord app, or Teams app receives all events
- the shared channel gateway checks which workspace, guild, server, or tenant
  the event came from
- Spritz routes that event to the concierge instance bound to that external
  tenant
- the concierge decides what to do
- replies go back out through the same shared channel gateway and the same
  shared app

The key design choice is:

- `concierge` is a first-class Spritz concept semantically
- but structurally it is still just an instance
- specifically, an instance with a concierge-oriented instance class and
  routing metadata

That keeps the system elegant because Spritz does not need a second resource
type, a wrapper record, or a separate runtime model.

## Goals

- Support one shared Slack, Discord, or Teams app serving many tenants.
- Route each external tenant to its own dedicated concierge instance.
- Let Spritz talk about concierge as a first-class product concept.
- Implement concierge using existing primitives:
  - instance
  - preset
  - instance class
  - admission resolvers
  - normal lifecycle
- Keep ownership and create-time binding flows compatible with the current
  extension framework.

## Non-goals

- Introducing a separate `Concierge` resource that wraps a backing instance.
- Introducing a second runtime or materialization stack separate from
  instances.
- Making Spritz responsible for deployment-specific billing, pricing, or
  ownership policy.
- Replacing normal instance creation with a special bot-only provisioning path.

## Problem Statement

Today, a shared channel gateway identity naturally maps to one integration entry
point, but not to one tenant.

That breaks down when:

- the same Slack app is installed into many workspaces
- the same Discord app is present in many guilds
- the same Teams app serves many organizations

In those cases, the shared channel gateway identity is not enough to decide which
instance should handle an event. The missing key is the external tenant scope.

Spritz therefore needs a first-class way to model:

- one shared integration principal
- many external tenants behind that principal
- one concierge instance per external tenant

## Design Principles

### Concierge is an instance-class pattern

Spritz may expose concierge as a named concept in API, docs, and UI.

But under the hood, concierge should be represented as:

- an ordinary instance
- with `instanceClassId=concierge`
- and explicit routing metadata attached to the instance

This is the cleanest model because there is only one real control-plane object.

### One active concierge instance per tenant

For a given combination of:

- `principalId`
- `provider`
- `externalScopeType`
- `externalTenantId`

Spritz should have exactly one active concierge instance at a time.

That should be enforced as a real uniqueness invariant, not just doc guidance.

### Ownership and routing are different

The owner tells Spritz who should own or administer the instance.

The routing identity tells Spritz which instance should receive an inbound
provider event.

These concerns must remain separate. Routing must not depend on recomputing
owner identity for every inbound event.

### Concierge identity should survive runtime replacement

The elegant version of this system does not introduce a separate wrapper
resource. Instead, the instance system itself should support stable logical
identity with replaceable runtime revisions.

That means:

- the concierge instance remains the stable object
- Spritz may roll its runtime forward internally
- routing still targets the same concierge instance identity

If Spritz does not yet support instance revisions, that should be added to the
instance lifecycle instead of creating a separate concierge wrapper type.

## Canonical Terms

### Channel gateway principal

The authenticated Spritz principal representing the shared channel gateway.

### External tenant ID

The provider-scoped identifier that determines where an inbound event belongs.

Examples:

- Slack workspace ID
- Discord guild ID
- Teams tenant ID

### External scope type

The provider-specific scope type for the tenant ID.

Examples:

- `workspace`
- `guild`
- `tenant`
- `team`

This keeps the routing model explicit without overloading the tenant ID field.

Initial provider mapping:

| Provider token | External scope type | External tenant ID |
| --- | --- | --- |
| `slack` | `workspace` | Slack `team_id` |
| `discord` | `guild` | Discord `guild_id` |
| `msteams` | `tenant` | Microsoft Teams tenant ID |

If Teams later needs team-level rather than tenant-level concierge routing,
Spritz can add `externalScopeType=team` without changing the rest of the
model.

### Concierge instance

A normal Spritz instance whose class and routing metadata declare that it is
the tenant entry point behind a shared channel gateway.

There is no separate backing-instance resource in this model.

### Routing identity

The tuple:

- `principalId`
- `provider`
- `externalScopeType`
- `externalTenantId`

This tuple uniquely identifies which concierge instance should receive an
inbound event.

## Proposed Architecture

The native Spritz model should add:

- a `concierge` instance class
- explicit routing metadata on instances
- an inbound route-resolution path that resolves directly to an instance

The control-plane pieces become:

- existing service principal authentication for the shared channel gateway
- existing create flow for instances
- a concierge-oriented instance class
- a uniqueness constraint or derived index on routing identity
- a channel gateway ingress surface that resolves routing identity to an
  instance

### Current-repo implementation path

To start implementation in the current repo without inventing new API or CRD
surfaces up front, Spritz should use the primitives it already has today:

- `presetId`
- preset-to-instance-class mapping
- create-time admission resolvers
- the unified extension resolver envelope
- existing lifecycle notifications

Important current constraints:

- `createRequest` does not currently expose a top-level `instanceClassId`
  field
- create-time extension mutations currently support:
  - `spec.serviceAccountName`
  - `annotations`
  - `labels`

So the first implementation should work like this:

- create or reuse a concierge preset whose preset catalog entry resolves to
  `instanceClass=concierge`
- keep create-time ownership and binding logic on the existing resolver path:
  - `owner.resolve`
  - `preset.create.resolve`
- introduce one new resolver operation for inbound routing:
  - `channel.route.resolve`
- let deployment-specific systems persist installation records however they
  want and expose them through that resolver contract
- optionally mirror route metadata onto instance annotations or labels for
  observability, but not as the only source of truth when an external routing
  registry is configured

That lets implementation start without adding a new CRD field surface first and
without hardcoding a storage engine such as SQL into Spritz.

If this proves too limiting later, Spritz can promote the same routing metadata
into an explicit structured instance field in a follow-up change.

### Concierge instance model

Concierge should be expressed on the instance itself.

Recommended shape:

- normal owner fields
- normal preset fields
- `instanceClassId=concierge`
- routing metadata:
  - `principalId`
  - `provider`
  - `externalScopeType`
  - `externalTenantId`

This routing metadata may live in one of these places:

- structured instance spec fields
- structured resolved-facts fields
- structured annotations materialized from create-time resolution

The preferred direction is explicit structured fields rather than opaque
annotations, because routing is core control-plane behavior.

For the current repo, the first implementation should use reserved annotations
as the source of truth:

- `spritz.sh/concierge.principal-id`
- `spritz.sh/concierge.provider`
- `spritz.sh/concierge.external-scope-type`
- `spritz.sh/concierge.external-tenant-id`
- `spritz.sh/concierge.state`
- `spritz.sh/concierge.route-id`

The derived lookup label should be:

- `spritz.sh/concierge-route=<route-id>`

where `route-id` is a deterministic hash of the canonical routing identity.

This is preferable to putting raw IDs directly in labels because Kubernetes
label values are constrained in length and character set.

### Concierge instance lifecycle states

At minimum, concierge instances should support:

- `provisioning`
- `ready`
- `replacing`
- `disconnected`
- `failed`

Routing should only target concierge instances in `ready`.

### Routing invariant

Spritz should enforce a real uniqueness rule for active concierge instances on:

- `principalId + provider + externalScopeType + externalTenantId`

If a separate index is used for performance, it should be derived from instance
state rather than acting as a peer source of truth.

For the current repo, the first implementation should use:

- annotations as the source of truth
- a derived route-hash label as the lookup index

Route hash construction should be deterministic from the canonical string:

```text
principalId + "\u0000" + provider + "\u0000" + externalScopeType + "\u0000" + externalTenantId
```

The route hash should be stored in full in the annotation and in a label-safe
truncated form for lookup.

## Install Flow

The install or bootstrap flow should work like this:

1. A shared channel gateway is connected to an external tenant.
2. The integration calls Spritz with:
   - `provider`
   - `externalScopeType`
   - `externalTenantId`
   - owner reference or resolved owner
   - preset and preset inputs
3. Spritz performs a deterministic upsert keyed by the routing identity.
4. If no active concierge instance exists, Spritz creates a normal instance:
   - owner resolution if needed
   - preset create resolvers if configured
   - instance class policy
   - instance materialization
5. Spritz marks the instance as `instanceClassId=concierge` and persists the
   routing metadata on that instance.
6. If an active concierge instance already exists for the same routing
   identity, Spritz returns or updates that instance instead of creating a
   duplicate.

This keeps provisioning aligned with the canonical instance model.

### Reinstall and retry behavior

Reinstall, reconnect, and retried create requests must be idempotent.

Rules:

- if the same active concierge instance already exists, return or update it
- if a disconnected concierge instance exists, reconnect it or roll it forward
  deterministically
- concurrent install attempts for the same routing identity must converge on
  one instance

Concrete current-repo upsert algorithm:

1. Compute the canonical route hash from:
   - `principalId`
   - `provider`
   - `externalScopeType`
   - `externalTenantId`
2. List instances by `spritz.sh/concierge-route=<route-hash>`
3. Verify annotation equality on any returned candidates
4. If zero candidates exist:
   - create a new concierge instance
5. If exactly one active candidate exists:
   - return or update that instance in place
6. If more than one active candidate exists:
   - fail closed and require repair

## Inbound Event Flow

The inbound routing flow should work like this:

1. Spritz receives a webhook or normalized event from a shared channel gateway
   principal.
2. Spritz authenticates the principal.
3. Spritz extracts:
   - `provider`
   - `externalScopeType`
   - `externalTenantId`
4. Spritz resolves the active concierge instance directly from the routing
   identity.
5. Spritz forwards the event to that concierge instance.
6. The concierge handles the event through its normal runtime surface.
7. If the concierge wants to reply or perform a channel action, the outbound
   request goes back through the shared channel gateway.
8. The shared channel gateway sends the actual reply or action through the same
   shared Slack app, Discord app, or Teams app.

Routing must be based on the instance's routing identity, not on owner lookup.

The practical consequence is:

- one shared app receives traffic for many external tenants
- Spritz does not create one app per tenant
- Spritz routes to different concierge instances based on the server,
  workspace, guild, or tenant the event came from

Normalized ingress envelope:

```json
{
  "principalId": "shared-discord-bot",
  "provider": "discord",
  "externalScopeType": "guild",
  "externalTenantId": "123456789012345678",
  "requestId": "event-123",
  "event": {}
}
```

The channel gateway should normalize Slack, Discord, and Teams payloads into
this shape before route resolution.

The channel gateway should extract tenant identity like this:

- Slack:
  - `provider=slack`
  - `externalScopeType=workspace`
  - `externalTenantId=team_id`
- Discord:
  - `provider=discord`
  - `externalScopeType=guild`
  - `externalTenantId=guild_id`
- Teams:
  - `provider=msteams`
  - `externalScopeType=tenant`
  - `externalTenantId=tenant_id`

## Runtime Replacement Flow

If a concierge runtime needs replacement, the instance model itself should own
that replacement.

The desired behavior is:

1. Spritz prepares a new runtime revision for the same concierge instance.
2. Spritz waits until the new revision is healthy.
3. Spritz atomically flips the active revision for that instance.
4. Routing continues to target the same concierge instance identity.
5. Spritz drains and removes the previous revision afterward.

This is more elegant than introducing a separate concierge resource that points
to a backing instance.

In the current repo, the first implementation can keep the same instance
identity and update that instance in place. A later revision-aware rollout
model can improve this without changing concierge routing semantics.

## API Direction

Spritz does not need a separate concierge resource API.

Instead, it should:

- extend instance create/update flows to support concierge-class instances
- expose concierge semantics through instance class and routing metadata
- add channel gateway ingress endpoints that resolve directly to concierge
  instances

Possible native surfaces:

- normal instance create or upsert API with concierge routing fields
- concierge-filtered list and lookup views over instances
- channel gateway ingress endpoints that normalize incoming events and resolve
  the target instance

If a deployment wants installation records to live outside Spritz, Spritz
should call that external system through the same extension-style resolver
contract it already uses elsewhere.

The exact REST shape may vary, but the control-plane contract should be:

- create or upsert concierge instance
- materialize it through the normal instance path
- route inbound event by routing identity

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
  "presetId": "concierge-openclaw"
}
```

In the current repo, `presetId` should select a preset whose configured
instance class is `concierge`. A dedicated top-level `instanceClassId` request
field is not required to start implementation.

## Relationship to Existing Extension Architecture

This model should reuse the current extension framework rather than add another
feature-specific hook family.

Recommended split:

- existing `owner.resolve` and `preset.create.resolve` style resolvers remain
  responsible for create-time facts
- a new inbound routing operation resolves the target instance from:
  - `principalId`
  - `provider`
  - `externalScopeType`
  - `externalTenantId`

This keeps the lifecycle clean:

- create-time extensions answer "which concierge instance should exist?"
- inbound routing answers "which existing concierge instance should receive
  this event?"

## External Routing Registry Contract

Spritz should not require installation records to live inside Kubernetes
metadata or inside a Spritz-owned SQL schema.

Instead, if a deployment wants external persistence, it should implement an
external routing registry behind the same extension transport style Spritz
already uses for owner and preset resolution.

The recommended read path is:

- `operation = "channel.route.resolve"`
- input:
  - `provider`
  - `externalScopeType`
  - `externalTenantId`
- output:
  - `instanceId`
  - optional normalized route metadata
  - optional lifecycle state such as `ready` or `disconnected`

Example request envelope:

```json
{
  "version": "v1",
  "extensionId": "channel-routing",
  "type": "resolver",
  "operation": "channel.route.resolve",
  "requestId": "req-123",
  "principal": {
    "id": "shared-discord-bot",
    "type": "service",
    "scopes": ["spritz.channel.route.resolve"]
  },
  "context": {
    "namespace": "instances",
    "instanceClassId": "concierge"
  },
  "input": {
    "provider": "discord",
    "externalScopeType": "guild",
    "externalTenantId": "123456789012345678"
  }
}
```

Example response envelope:

```json
{
  "status": "resolved",
  "output": {
    "instanceId": "zeno-acme",
    "state": "ready"
  }
}
```

Supported statuses should follow the existing extension pattern:

- `resolved`
- `unresolved`
- `forbidden`
- `ambiguous`
- `invalid`
- `unavailable`

For write-back, Spritz should not invent a second custom sync API. It should
reuse `instance.lifecycle.notify` so an external registry can observe:

- instance created
- instance ready
- instance updated
- instance disconnected
- instance deleted

That gives deployments one consistent way to keep external install state in
sync without teaching Spritz how that state is persisted.

## Deployment Boundary

Spritz should own:

- concierge-as-instance semantics
- channel gateway ingress surfaces
- extension contracts for route resolution
- routing an inbound event to a resolved instance

Deployments should own:

- where installation records are persisted
- how owner and billing identity are chosen
- provider-specific installation state
- audit semantics for external senders

This keeps Spritz reusable while still letting deployments implement
Slack-, Discord-, and Teams-specific behavior.

## Operational Defaults

The intended default behavior should be:

- installation happens when the shared app is connected to an external tenant,
  not on first inbound message
- install handling should be idempotent
- disconnect should stop routing immediately
- reconnect should reuse the same concierge instance identity when the
  deployment still considers that installation valid
- outbound channel actions should go back through the shared channel gateway,
  not directly from the concierge runtime to the provider

Spritz does not need to own the install database for these rules to hold. It
only needs to expose the routing contract cleanly enough that a deployment can
implement them predictably.

### Install

The deployment should create or upsert the installation record before normal
message routing begins.

Spritz should then either:

- create a concierge instance through the normal instance flow, or
- reuse the existing concierge instance already bound to that routing identity

This gives each tenant a stable concierge before the first real conversation.

### Disconnect and reconnect

When an installation is disconnected:

- the external registry should mark it disconnected
- `channel.route.resolve` should return `unresolved`
- the concierge instance may remain in place for later reuse, depending on
  deployment policy

When an installation reconnects:

- the external registry should upsert it back to an active state
- the same concierge instance identity should be reused when possible
- only if policy requires a clean replacement should a new concierge be
  created

### Outbound action path

The clean path is:

1. shared channel gateway receives the inbound event
2. Spritz routes it to the concierge instance
3. concierge decides what to send
4. concierge asks the deployment-owned channel gateway to send it
5. channel gateway performs the real provider API call

That keeps provider credentials out of concierge runtimes and preserves one
controlled egress path for Slack, Discord, and Teams.

### Teams v1 scope

The default v1 assumption should be:

- `provider = msteams`
- `externalScopeType = tenant`

If product requirements later need a separate concierge per team rather than
per tenant, the same routing model can support:

- `externalScopeType = team`

without changing the rest of the architecture.

## Why This Is More Elegant

This design avoids:

- a separate `Concierge` table
- a wrapper-to-backing-instance split
- duplicated lifecycle state
- duplicated routing state
- duplicated replacement semantics

There is only one real object:

- the instance

Concierge is simply:

- a semantic role
- an instance class
- routing metadata

That is the cleanest long-term shape because it keeps Spritz's core model small
while still making concierge a first-class concept for users and deployments.

## Validation

Validation for this architecture should include:

1. Connect the same shared channel gateway to two different external tenants.
2. Create or bootstrap one concierge instance for each tenant.
3. Verify each concierge instance is a normal instance with
   `instanceClassId=concierge`.
4. Verify the configured routing registry resolves two distinct routing
   identities keyed by:
   - `principalId + provider + externalScopeType + externalTenantId`
5. Send inbound events from both tenants through the same app integration.
6. Verify each event routes to the correct concierge instance.
7. Verify reinstall or reconnect returns the same concierge instance identity.
8. Verify runtime replacement preserves concierge instance identity.
9. Verify uninstall or disconnect disables routing for that tenant.

Disconnect and uninstall behavior should be explicit:

- uninstall or disconnect sets `spritz.sh/concierge.state=disconnected`
- routing must stop immediately for disconnected instances
- reconnect may reuse the same instance identity if policy allows

## Follow-ups

- Define the exact instance field shape for routing metadata.
- Decide whether instance metadata is only mirrored for debugging or also used
  as a fallback cache when an external registry is configured.
- Define the normalized `externalScopeType` and `externalTenantId` shapes for
  Slack, Discord, and Teams.
- Add `channel.route.resolve` to the unified extension framework.
- Define how instance revisioning should work for concierge-class instances.
- Decide whether structured routing fields should be added after the initial
  extension-driven implementation.

## References

- [2026-03-12-external-identity-resolution-api-architecture.md](2026-03-12-external-identity-resolution-api-architecture.md)
- [2026-03-19-unified-extension-framework-architecture.md](2026-03-19-unified-extension-framework-architecture.md)
- [2026-03-11-external-provisioner-and-service-principal-architecture.md](2026-03-11-external-provisioner-and-service-principal-architecture.md)
