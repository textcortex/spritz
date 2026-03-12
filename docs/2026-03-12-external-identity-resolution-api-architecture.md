---
date: 2026-03-12
author: Spritz Maintainers <user@example.com>
title: External Identity Resolution API Architecture
tags: [spritz, auth, provisioning, identity, api, architecture]
---

## Overview

This document defines the long-term stable Spritz architecture for resolving an
external identity to a Spritz owner during provisioning.

In this model, Spritz does not own account linking. Spritz consumes an external
identity resolver that is already authoritative for mappings such as:

- Microsoft Teams user -> product user
- Slack user -> product user
- Discord user -> product user

Spritz stays provider-agnostic and product-agnostic. It accepts a normalized
external owner reference, derives the caller namespace from authentication, and
asks a configured resolver which Spritz owner should own the workspace.

The external caller sends only the external platform identity it already has,
such as a Microsoft Teams user ID. Internal Spritz owner identifiers remain a
backend concern and do not need to cross the bot boundary.

This architecture extends the external provisioner model defined in
[2026-03-11-external-provisioner-and-service-principal-architecture.md](2026-03-11-external-provisioner-and-service-principal-architecture.md)
and the portable auth model defined in
[2026-02-24-portable-authentication-and-account-architecture.md](2026-02-24-portable-authentication-and-account-architecture.md).

## Goals

- Let external systems provision for a human without exposing manual owner-ID
  lookup UX.
- Keep Spritz core portable and free of product-specific account-linking logic.
- Make the public create API stable across Microsoft Teams, Slack, Discord, and
  future providers.
- Keep ownership resolution narrow, explicit, auditable, and deterministic.
- Ensure external bots never need to send or learn internal owner IDs.
- Support enterprise providers where identity is tenant-scoped.

## Non-goals

- Turning Spritz into a general identity provider.
- Making Spritz the source of truth for account linking.
- Adding browser claim flows, link intents, or user-entered verification codes
  to Spritz core.
- Letting service principals search users by email, display name, or handle.
- Embedding Microsoft Teams-specific or deployment-specific mapping logic in
  Spritz core.

## Design Principles

### Spritz resolves, but does not link

Spritz is a consumer of authoritative identity mappings, not the owner of those
mappings.

If a deployment already knows which external user belongs to which product
account, Spritz should ask that system directly instead of recreating a second
linking database.

### Caller namespace comes from authentication

The integration namespace is derived from the authenticated service principal.

The caller must not be allowed to spoof namespace selection by sending an
arbitrary `issuer` value in the request body.

### External identity is opaque and provider-scoped

Spritz treats provider user IDs as opaque strings.

Canonicalization belongs to the provider adapter or external resolver, not to
Spritz core.

### Bots speak only in external IDs

The bot should send only the provider identity it already knows, such as a
Teams or Discord user ID plus tenant when required.

It should not collect, store, or forward internal Spritz owner IDs as part of
the normal provisioning path.

### Enterprise providers need tenant scope

For enterprise messaging systems, a provider user ID is often not globally
meaningful without tenant context.

The public API must support an explicit tenant or realm field.

### Create is the main contract

The stable public contract should center on create-time owner resolution.

Spritz may expose operational debug endpoints later, but the main integration
surface is still `POST /spritzes`.

## Canonical Terms

### Spritz owner

The human principal that owns and later accesses a workspace.

### External owner reference

A provider-scoped, product-agnostic reference to a human in an external
messaging or workflow system.

### Resolver namespace

The namespace bound to the authenticated service principal. It tells Spritz
which external resolver configuration to use.

### External identity resolver

A deployment-owned authoritative system that maps an external owner reference
to a Spritz owner.

## Core Model

The public owner reference should support either a direct owner ID or an
external identity:

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
  "provider": "msteams",
  "tenant": "72f988bf-86f1-41af-91ab-2d7cd011db47",
  "subject": "29:1A2BcD3EfG4HiJ5KlM6NoP"
}
```

Rules:

- `type` MUST be either `owner` or `external`.
- `provider` MUST be a normalized lower-case token.
- `subject` MUST be treated as an opaque string.
- `tenant` MAY be omitted only for providers where the subject is globally
  stable without tenant scope.
- For `msteams`, `tenant` SHOULD be required by policy.
- The effective identity key inside Spritz is:
  `resolverNamespace + provider + tenant + subject`
- `resolverNamespace` is derived from the authenticated service principal, not
  from request payload.

## High-Level Architecture

Components:

- external bot or automation
- Spritz API
- resolver configuration bound to the service principal
- deployment-owned external identity resolver

Flow:

1. The bot calls `POST /spritzes` with `ownerRef.type=external` and the normal
   external user identity it already has.
2. Spritz authenticates the service principal.
3. Spritz derives the resolver namespace from that principal.
4. Spritz validates that the provider and tenant are allowed for that namespace.
5. Spritz calls the configured external resolver.
6. The resolver returns either `resolved`, `unresolved`, `forbidden`, or
   `ambiguous`.
7. Spritz either provisions for the resolved owner or returns a typed error.

The bot never handles the internal owner ID directly. That translation happens
inside the trusted backend path between Spritz and the resolver.

## Stable Public API

The create API should add `ownerRef` while preserving existing `ownerId`
compatibility.

Recommended request:

```json
{
  "ownerRef": {
    "type": "external",
    "provider": "msteams",
    "tenant": "72f988bf-86f1-41af-91ab-2d7cd011db47",
    "subject": "29:1A2BcD3EfG4HiJ5KlM6NoP"
  },
  "presetId": "openclaw",
  "idempotencyKey": "msteams-123"
}
```

Rules:

- `ownerId` and `ownerRef` MUST be mutually exclusive.
- If `ownerRef.type=owner`, Spritz uses the explicit owner ID.
- If `ownerRef.type=external`, Spritz MUST resolve the owner through the
  configured resolver before create.
- New bot and automation integrations SHOULD use `ownerRef.type=external`.
- Create MUST NOT require the caller to know the internal owner ID.
- The caller MUST NOT provide `resolverNamespace` or `issuer` in the request
  body.
- Spritz MUST use the resolver namespace bound to the authenticated principal.
- Spritz MUST keep the resolved internal owner ID on the backend side of the
  create flow.

## External Resolver Contract

Spritz core should define an internal resolver interface:

- `ResolveExternalOwner(ctx, namespace, identity) -> result`

Recommended canonical result states:

- `resolved`
- `unresolved`
- `forbidden`
- `ambiguous`
- `unavailable`

For HTTP-backed deployments, a portable resolver API can look like this:

`POST /v1/external-owners/resolve`

Request:

```json
{
  "namespace": "support-bot",
  "identity": {
    "provider": "msteams",
    "tenant": "72f988bf-86f1-41af-91ab-2d7cd011db47",
    "subject": "29:1A2BcD3EfG4HiJ5KlM6NoP"
  },
  "requestId": "req_01j..."
}
```

Response when resolved:

```json
{
  "status": "resolved",
  "ownerId": "user-123"
}
```

Response when unresolved:

```json
{
  "status": "unresolved"
}
```

Response when forbidden:

```json
{
  "status": "forbidden"
}
```

Response when ambiguous:

```json
{
  "status": "ambiguous"
}
```

Properties:

- The resolver MUST return at most one owner.
- The resolver MUST NOT perform fuzzy matching by email, name, or handle.
- The resolver is the source of truth for external-to-owner mappings.
- The resolved owner ID is an internal backend value for Spritz and does not
  need to be exposed to the bot.

## Public Error Model

When create is called with `ownerRef.type=external`, Spritz should return typed
errors rather than leaking resolver internals.

Recommended errors:

- `external_identity_unresolved`
- `external_identity_forbidden`
- `external_identity_ambiguous`
- `external_identity_provider_unsupported`
- `external_identity_resolution_unavailable`

Recommended unresolved example:

```json
{
  "error": "external_identity_unresolved",
  "identity": {
    "provider": "msteams",
    "tenant": "72f988bf-86f1-41af-91ab-2d7cd011db47",
    "subject": "29:1A2BcD3EfG4HiJ5KlM6NoP"
  }
}
```

Recommended status semantics:

- `external_identity_unresolved`: `409`
- `external_identity_forbidden`: `403`
- `external_identity_ambiguous`: `409`
- `external_identity_provider_unsupported`: `400`
- `external_identity_resolution_unavailable`: `503`

## Authorization Model

Recommended service capabilities:

- `spritz.external_identities.resolve_via_create`

Rules:

- Resolver namespace MUST be bound to the authenticated service principal.
- A service principal MUST be allowed to resolve only within its own namespace
  unless explicitly granted broader authority.
- Provider allowlists and tenant allowlists SHOULD be policy-controlled per
  namespace.

## V1 Consistency Model

Spritz should treat the external resolver as the source of truth.

Safe default behavior for v1:

- no durable copy of identity mappings in Spritz core
- no cache required
- synchronous resolution during create

If a mapping changes in the external system, future creates should follow the
current resolver answer.

Existing workspaces keep their existing owner. Resolution affects only future
create operations.

## Microsoft Teams Guidance

The first concrete provider should be `msteams`.

Provider guidance:

- `provider` value: `msteams`
- `tenant` SHOULD be the Microsoft tenant identifier and SHOULD be required by
  policy
- `subject` MUST be the stable Teams user identifier chosen by the deployment's
  Teams integration

Spritz core should not attempt to normalize Teams identifiers itself. The Teams
adapter or authoritative resolver must choose one canonical subject format and
keep it consistent.

## Backward Compatibility

The stable migration path is:

1. Keep `ownerId` as-is.
2. Add `ownerRef`.
3. Add resolver-backed handling for `ownerRef.type=external`.
4. Keep direct owner-ID creates only for existing clients that already use them.
5. Move new bot integrations to external-ID-only create requests.

This avoids a breaking change to current provisioner clients while giving new
integrations a portable path that does not require manual owner-ID exchange.

## Validation

The architecture is complete when Spritz can demonstrate all of these flows:

1. A service principal provisions successfully with `ownerRef.type=external`
   for a resolved Microsoft Teams user.
2. An unresolved external identity fails with `external_identity_unresolved`.
3. A forbidden namespace or provider fails with `external_identity_forbidden`.
4. An ambiguous resolver answer fails with `external_identity_ambiguous`.
5. A resolver outage fails with `external_identity_resolution_unavailable`.
6. The bot never sends an internal owner ID during the normal provisioning path.

## Recommended Sequencing

### Phase 1 - Public API and internal abstraction

- Add `ownerRef` to create requests.
- Derive resolver namespace from authenticated service principal.
- Define the internal resolver interface and typed result states.
- Make external-ID-only bot calls the preferred integration path.

### Phase 2 - Resolver-backed provisioning

- Implement the HTTP resolver adapter.
- Add provider and tenant policy validation.
- Add typed create-time error mapping.
- Keep resolved owner IDs internal to Spritz after resolver lookup.

## Potential Future Features

- A separate `spritz.external_identities.check` capability or debug endpoint if
  deployments later need preflight inspection outside create.
- A short positive cache if synchronous resolver lookups become a proven
  performance bottleneck. Avoid negative caching unless there is a demonstrated
  need.
- Additional reliability controls such as timeout tuning, retries,
  circuit-breakers, and richer resolution metrics.
- A signed owner assertion flow if a deployment later wants Spritz to verify a
  short-lived resolver-signed identity result locally instead of performing a
  live resolver call during every create.

## References

- [2026-03-11-external-provisioner-and-service-principal-architecture.md](2026-03-11-external-provisioner-and-service-principal-architecture.md)
- [2026-02-24-portable-authentication-and-account-architecture.md](2026-02-24-portable-authentication-and-account-architecture.md)
