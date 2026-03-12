---
date: 2026-03-12
author: Spritz Maintainers <user@example.com>
title: External Identity Linking API Architecture
tags: [spritz, auth, provisioning, identity, api, architecture]
---

## Overview

This document defines the long-term stable Spritz API for linking external
identities to Spritz owners.

The goal is to let external systems such as chat bots, workflow engines, or
support tools identify a human user without requiring that user to manually
paste a Spritz-specific owner ID.

Typical examples include:

- a Discord bot,
- a Slack app,
- a Microsoft Teams assistant,
- a Google Chat integration,
- a Mattermost automation.

Spritz must stay backend-agnostic and deployment-agnostic. It must not learn
provider-specific user models from any particular product deployment.

Instead, Spritz should standardize a generic external identity model and own
the lifecycle for linking those identities to normal Spritz owners.

This architecture extends, but does not replace, the external provisioner model
defined in [2026-03-11-external-provisioner-and-service-principal-architecture.md](2026-03-11-external-provisioner-and-service-principal-architecture.md)
and the portable auth model defined in
[2026-02-24-portable-authentication-and-account-architecture.md](2026-02-24-portable-authentication-and-account-architecture.md).

## Goals

- Remove the need for humans to manually provide Spritz owner IDs to external
  systems.
- Keep Spritz core portable and free of provider-specific assumptions.
- Let external provisioners resolve a human owner through a narrow,
  action-specific API.
- Require explicit human confirmation before an external identity is bound to a
  Spritz owner.
- Keep the create/provision flow stable across Discord, Slack, Teams, and
  future providers.
- Align the identity model with widely used federation concepts such as
  issuer-plus-subject identifiers.
- Preserve current explicit ownership and lifecycle guarantees.

## Non-goals

- Turning Spritz into a general identity provider.
- Replacing OIDC, OAuth2, SAML, or SCIM in external systems.
- Letting service principals search arbitrary users by email, name, or handle.
- Letting an external system claim a user account without a human confirmation
  step.
- Embedding Discord-, Slack-, or deployment-specific logic in Spritz core.
- Requiring a provider-specific verification protocol in the core API.

## Design Principles

### External identities are references, not sessions

An external identity is not a login session and not an authorization grant.

It is a stable reference that can later resolve to a Spritz owner.

### Spritz owns the binding

External systems may identify a candidate external subject, but only Spritz may
bind that subject to a human owner after the human authenticates through the
normal Spritz browser path.

### Stable identity keys use namespace plus subject

The canonical identity key is:

- `issuer`
- `provider`
- `subject`

This follows the same broad model as federated identity systems that treat
issuer-plus-subject as the stable user key.

### Linking must be explicit

Provisioning and linking are related, but they are not the same operation.

The stable API should keep linking explicit so that:

- create requests stay idempotent,
- unresolved identities are a well-defined precondition failure,
- link creation remains auditable,
- provider-specific UX can evolve without breaking the create contract.

### Providers stay pluggable

Spritz should not require Discord-specific or Slack-specific fields in canonical
records.

Provider-specific metadata should remain optional, opaque, and advisory.

## Standards Alignment

Spritz should align with existing standards where practical:

- Use issuer-plus-subject semantics similar to OIDC federated identities.
- Treat display name, email, and handle as advisory metadata rather than stable
  identifiers.
- Treat external identity scoping similarly to SCIM `externalId` semantics:
  identifiers are scoped to the external provisioning domain, not globally
  portable by assumption.

Spritz does not need to implement a general OIDC account linking protocol in
core. It needs a portable control-plane API that uses those same identity
principles.

## Canonical Terms

### Spritz owner

A Spritz owner is the normal human principal that owns and later accesses a
workspace.

This is the same owner model used by existing UI and provisioner flows.

### External identity

An external identity is an opaque tuple:

- `issuer`: the namespace that controls the identity binding authority,
- `provider`: the external system category such as `discord`, `slack`, or
  `teams`,
- `subject`: the stable provider-specific user identifier.

### External identity link

A durable binding from one external identity to one Spritz owner.

### External identity link intent

A short-lived, one-time claim artifact created by a service principal and later
claimed by a human in the browser to create or confirm a durable link.

### Owner reference

A stable create-time abstraction that allows Spritz to accept either:

- a direct owner ID, or
- a linked external identity reference.

## Identity Namespace Model

The identity tuple is:

```json
{
  "issuer": "support-bot",
  "provider": "discord",
  "subject": "123456789012345678"
}
```

Rules:

- `issuer` MUST be a stable namespace controlled by the service principal or
  integration owner.
- `provider` MUST be a normalized lower-case token.
- `subject` MUST be treated as an opaque string.
- The tuple `(issuer, provider, subject)` MUST be globally unique within a
  Spritz deployment.
- One tuple MUST resolve to at most one active Spritz owner at a time.
- One owner MAY have multiple linked external identities.

The `issuer` field is critical. It prevents one integration from asserting or
resolving identities in another integration's namespace.

## Core Resources

### ExternalIdentityRef

```json
{
  "issuer": "support-bot",
  "provider": "discord",
  "subject": "123456789012345678"
}
```

### OwnerRef

```json
{
  "type": "owner",
  "id": "user-123"
}
```

or

```json
{
  "type": "external",
  "issuer": "support-bot",
  "provider": "discord",
  "subject": "123456789012345678"
}
```

### ExternalIdentityLink

Recommended response shape:

```json
{
  "id": "eil_01j...",
  "identity": {
    "issuer": "support-bot",
    "provider": "discord",
    "subject": "123456789012345678"
  },
  "ownerId": "user-123",
  "status": "active",
  "verifiedAt": "2026-03-12T15:00:00Z",
  "verificationMode": "delivery",
  "createdAt": "2026-03-12T15:00:00Z",
  "metadata": {
    "displayName": "onur"
  }
}
```

### ExternalIdentityLinkIntent

Recommended response shape:

```json
{
  "id": "eili_01j...",
  "identity": {
    "issuer": "support-bot",
    "provider": "discord",
    "subject": "123456789012345678"
  },
  "status": "pending",
  "claimUrl": "https://spritz.example.com/link/eili_01j.../claim",
  "userCode": "7J4K-9P",
  "expiresAt": "2026-03-12T15:15:00Z",
  "createdAt": "2026-03-12T15:00:00Z"
}
```

`userCode` is optional but recommended. It gives deployments a stronger defense
against forwarded or copied links without making the core flow provider-specific.

## Stable API Surface

### 1. Resolve a linked identity

`POST /api/external-identity-links/resolve`

Request:

```json
{
  "identity": {
    "issuer": "support-bot",
    "provider": "discord",
    "subject": "123456789012345678"
  }
}
```

Success when linked:

```json
{
  "linked": true,
  "link": {
    "id": "eil_01j...",
    "ownerId": "user-123",
    "status": "active",
    "verifiedAt": "2026-03-12T15:00:00Z"
  }
}
```

Success when unlinked:

```json
{
  "linked": false
}
```

This endpoint MUST match exact tuples only. It MUST NOT perform fuzzy matching
by email, display name, or handle.

### 2. Create a link intent

`POST /api/external-identity-link-intents`

Request:

```json
{
  "identity": {
    "issuer": "support-bot",
    "provider": "discord",
    "subject": "123456789012345678"
  },
  "metadata": {
    "displayName": "onur"
  },
  "ttl": "15m",
  "requireUserCode": true,
  "clientState": "discord-interaction-123"
}
```

Response:

```json
{
  "id": "eili_01j...",
  "status": "pending",
  "claimUrl": "https://spritz.example.com/link/eili_01j.../claim",
  "userCode": "7J4K-9P",
  "expiresAt": "2026-03-12T15:15:00Z"
}
```

Properties:

- Creating an intent MUST be idempotent for the same service principal and
  unresolved identity while the active intent remains pending.
- If the identity is already linked, the API SHOULD return the existing active
  link rather than creating a new intent.
- Intent creation MUST NOT silently relink an identity that is already linked
  to a different owner.

### 3. Inspect a link intent

`GET /api/external-identity-link-intents/{intentId}`

This endpoint exists so an external client can poll intent state without
needing browser callbacks.

Response status values:

- `pending`
- `claimed`
- `expired`
- `cancelled`

### 4. Claim a link intent as a human

`POST /api/external-identity-link-intents/{intentId}/claim`

This endpoint requires normal human browser authentication.

Request:

```json
{
  "userCode": "7J4K-9P"
}
```

Behavior:

- The authenticated human becomes the target owner.
- If the identity is not yet linked, Spritz creates the durable link.
- If it is already linked to the same owner, the operation is idempotent.
- If it is already linked to a different owner, Spritz returns conflict.
- Successful claim marks the intent as `claimed`.

### 5. List current human links

`GET /api/external-identity-links`

Default behavior for human principals:

- return only the links owned by the current principal.

Default behavior for service principals:

- not allowed unless explicitly granted a list scope.

### 6. Unlink

`DELETE /api/external-identity-links/{linkId}`

Rules:

- Human principals may unlink their own links.
- Service principals may not unlink by default.
- Admins may unlink as break-glass.
- Unlinking affects future resolution only. It MUST NOT mutate the ownership of
  existing workspaces.

## Provisioning Integration

The create API should add `ownerRef` while preserving existing `ownerId`
compatibility.

Recommended request:

```json
{
  "ownerRef": {
    "type": "external",
    "issuer": "support-bot",
    "provider": "discord",
    "subject": "123456789012345678"
  },
  "presetId": "openclaw",
  "idempotencyKey": "discord-123"
}
```

Rules:

- `ownerId` and `ownerRef` MUST be mutually exclusive.
- If `ownerRef.type=owner`, Spritz uses the explicit owner ID.
- If `ownerRef.type=external`, Spritz MUST resolve the link before create.
- If the external identity is unlinked, create MUST fail with a typed conflict.
- Create MUST NOT implicitly create link intents in the stable v1 contract.

Recommended typed error:

```json
{
  "error": "external_identity_unlinked",
  "identity": {
    "issuer": "support-bot",
    "provider": "discord",
    "subject": "123456789012345678"
  }
}
```

This keeps create semantics clean and preserves the idempotency guarantees from
the current provisioner architecture.

## Verification Modes

The core API should define a generic `verificationMode` field on durable links
and intents.

Stable modes:

- `delivery`: the external system delivered the claim artifact out-of-band to
  the target subject.
- `provider_oauth`: the link was completed after provider-specific external
  OAuth verification.
- `attested`: a trusted external bridge supplied an additional signed
  verification signal.

The core contract only requires `delivery`.

This keeps the API portable while allowing stronger provider-specific
verification in overlays or extensions later.

## Authorization Model

Recommended new service scopes:

- `spritz.external_identities.resolve`
- `spritz.external_identities.link_intents.create`

Recommended human capabilities:

- claim own pending intents,
- list own links,
- unlink own links.

Recommended admin capabilities:

- inspect links across owners,
- revoke links,
- relink by explicit override.

Service principals MUST be scoped to their own `issuer` namespace unless
explicitly granted broader authority.

## State Model

### Link intent states

- `pending`
- `claimed`
- `expired`
- `cancelled`

### Durable link states

- `active`
- `revoked`

State transition rules:

- `pending -> claimed` when a human successfully completes the claim.
- `pending -> expired` when TTL elapses.
- `active -> revoked` when owner or admin unlinks.
- `revoked` links MUST NOT resolve for future creates.

## Storage and Implementation Strategy

The API contract should not depend on a specific storage backend.

Spritz should define two internal store interfaces:

- `ExternalIdentityLinkStore`
- `ExternalIdentityLinkIntentStore`

Recommended initial backend for Kubernetes-native deployments:

- store records in the control namespace,
- derive object names from a hash of `(issuer, provider, subject)`,
- keep raw `subject` out of object names,
- store advisory metadata separately from canonical key fields,
- garbage-collect expired intents opportunistically or through a small cleanup
  loop.

For deployments without a dedicated database, a control-namespace store is
acceptable for v1. The store interface keeps open the option of a SQL or KV
backend later without changing the public API.

## Security Requirements

- Never use email, display name, or handle as the canonical identity key.
- Never allow fuzzy resolution.
- Require short TTLs on link intents.
- Treat claim URLs as one-time artifacts.
- Support optional `userCode` confirmation for higher assurance.
- Keep issuer namespace boundaries strict.
- Record actor ID, owner ID, identity tuple, verification mode, and timestamps
  in audit logs.

## Backward Compatibility

The stable migration path is:

1. Keep `ownerId` as-is.
2. Add `ownerRef`.
3. Prefer `ownerRef` for new integrations.
4. Keep direct owner-ID creates for existing clients.

This avoids a breaking change to current provisioner clients while giving new
integrations a portable path that does not leak internal user identifiers.

## Validation

The architecture is complete when Spritz can demonstrate all of these flows:

1. A service principal creates a link intent for an unresolved external
   identity.
2. A human logs in and claims the link.
3. A service principal resolves the identity and receives the correct owner.
4. A service principal provisions a workspace using `ownerRef.type=external`.
5. An unlinked identity fails create with a typed conflict.
6. A revoked link no longer resolves.
7. Cross-issuer lookup is denied.
8. Duplicate claim attempts remain idempotent for the same owner and conflict
   for different owners.

## Recommended Sequencing

### Phase 1 - Core resource model

- Define `ExternalIdentityRef`, durable link records, and link intent records.
- Implement store interfaces.
- Implement resolve, create-intent, and claim flows.

### Phase 2 - Provisioning integration

- Add `ownerRef` to create requests.
- Keep `ownerId` compatibility.
- Add typed unresolved-link errors.

### Phase 3 - Human management

- Add list-self-links and unlink flows.
- Add audit-friendly UI surfaces.

### Phase 4 - Optional stronger verification

- Add provider-specific verification overlays where deployments need them.
- Keep the stable core contract unchanged.

## References

- [2026-03-11-external-provisioner-and-service-principal-architecture.md](2026-03-11-external-provisioner-and-service-principal-architecture.md)
- [2026-02-24-portable-authentication-and-account-architecture.md](2026-02-24-portable-authentication-and-account-architecture.md)
