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
asks a configured resolver which Spritz owner should own the instance.

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

The human principal that owns and later accesses an instance.

### External owner reference

A provider-scoped, product-agnostic reference to a human in an external
messaging or workflow system.

### Resolver namespace

The namespace bound to the authenticated service principal. It tells Spritz
which external resolver configuration to use.

In request and policy examples, this should be called `issuer` rather than
plain `namespace` to avoid confusion with Kubernetes namespaces.

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
  "subject": "6f0f9d4f-9b0e-4d52-8c3a-ef0fd64b9b9f"
}
```

Rules:

- `type` MUST be either `owner` or `external`.
- `provider` MUST be a normalized lower-case token.
- `subject` MUST be treated as an opaque string.
- `tenant` MAY be omitted only for providers where the subject is globally
  stable without tenant scope.
- For `msteams`, `tenant` MUST be required.
- The effective identity key inside Spritz is:
  `issuer + provider + tenant + subject`
- `issuer` is derived from the authenticated service principal, not from
  request payload.

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
3. Spritz derives the resolver issuer from that principal.
4. Spritz validates that the provider and tenant are allowed for that issuer.
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
    "subject": "6f0f9d4f-9b0e-4d52-8c3a-ef0fd64b9b9f"
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
- The caller MUST NOT provide `issuer` in the request body.
- Spritz MUST use the resolver issuer bound to the authenticated principal.
- Spritz MUST keep the resolved internal owner ID on the backend side of the
  create flow.
- `ownerRef.type=external` SHOULD be available only to service principals by
  default.

## CLI Shape

`spz` should support both direct-owner and external-identity create flows.

Direct owner form:

```bash
spz create --owner-id user-123 --preset openclaw
```

External identity form:

```bash
spz create --owner-provider discord --owner-subject 123456789012345678 --preset openclaw
```

Tenant-scoped external identity form:

```bash
spz create --owner-provider msteams \
  --owner-tenant 72f988bf-86f1-41af-91ab-2d7cd011db47 \
  --owner-subject 6f0f9d4f-9b0e-4d52-8c3a-ef0fd64b9b9f \
  --preset openclaw
```

CLI rules:

- `--owner-id` and external owner flags MUST be mutually exclusive.
- `spz` should build `ownerRef.type=external` when external owner flags are
  used.
- `spz` should require `--owner-tenant` for providers that are tenant-scoped by
  policy.
- `spz` should keep supporting `--owner-id` for direct internal and admin use.

## External Resolver Contract

Spritz core should define an internal resolver interface:

- `ResolveExternalOwner(ctx, issuer, identity) -> result`

Recommended canonical result states:

- `resolved`
- `unresolved`
- `forbidden`
- `ambiguous`
- `unavailable`

Recommended internal shape:

```go
type ExternalOwnerResolver interface {
    ResolveExternalOwner(ctx context.Context, principal principal, ref ExternalOwnerRef) (ExternalOwnerResolution, error)
}
```

The resolver is an internal Spritz dependency. The bot does not call it
directly.

### Resolver policy binding

Spritz should not let the caller choose which resolver to trust.

Instead, each authenticated service principal should be bound to resolver
policy that defines:

- resolver endpoint or adapter reference
- resolver authentication reference
- allowed providers
- allowed tenants
- resolver timeout
- issuer identifier for resolver calls

That policy should be selected from the authenticated service principal
identity, not from request payload.

### Resolver transport

The recommended production default is one HTTP adapter inside Spritz that calls
the deployment's authoritative identity service.

Why this is the right default:

- it keeps the public Spritz API small
- it avoids duplicating identity state inside Spritz
- it lets deployments use an existing product API instead of building a second
  control plane
- it preserves a strict backend-only trust boundary

### Resolver authentication

Spritz should authenticate to the resolver with its own backend credential.

Recommended order of preference:

1. workload identity or mTLS if the deployment already has it
2. a dedicated Spritz-to-resolver bearer token

Rules:

- Spritz MUST NOT forward the bot's bearer token to the resolver as the primary
  trust mechanism.
- Resolver authentication is between Spritz and the resolver.
- The external bot authenticates only to Spritz.
- The default resolver timeout SHOULD be 5 seconds.
- Spritz SHOULD perform no automatic retries in v1.

For HTTP-backed deployments, a portable resolver API can look like this:

`POST /v1/external-owners/resolve`

Request:

```json
{
  "issuer": "support-bot",
  "identity": {
    "provider": "msteams",
    "tenant": "72f988bf-86f1-41af-91ab-2d7cd011db47",
    "subject": "6f0f9d4f-9b0e-4d52-8c3a-ef0fd64b9b9f"
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

### Audit and persistence

When Spritz creates an instance from `ownerRef.type=external`, it should record
enough information for audit without leaking raw external identifiers in common
resource metadata.

Recommended stored fields:

- issuer
- provider
- tenant
- `subjectHash`
- actor principal ID
- resolution timestamp

Rules:

- Spritz SHOULD NOT store raw external `subject` values in Kubernetes labels.
- Spritz SHOULD NOT store raw external `subject` values in normal annotations.
- `subjectHash` SHOULD be an HMAC-SHA-256 derived from the external subject and
  a deployment secret.
- If raw external subject values are needed for incident response, they should
  appear only in secured audit logs.

### Create-time resolver behavior

Resolution should happen during create request normalization, before any create
attempt reaches the normal provisioning transaction.

Rules:

- If resolution returns `resolved`, Spritz substitutes the resolved internal
  owner ID and continues through the normal create path.
- If resolution returns `unresolved`, `forbidden`, `ambiguous`, or
  `unavailable`, Spritz MUST fail the create before creating any resource.
- Service-principal create responses SHOULD omit `ownerId` once the owner was
  resolved from an external identity.

Recommended service-principal response shape for `ownerRef.type=external`:

- `spritz`
- `accessUrl`
- `chatUrl`
- `instanceUrl`
- Kubernetes namespace for the instance
- `presetId`
- `idempotencyKey`
- `replayed`

The response SHOULD NOT include the resolved internal owner ID.

### Idempotency rule

Resolver lookup should happen before a create is finalized, but successful
creates must still preserve normal Spritz idempotency guarantees.

Rules:

- The idempotency fingerprint for external-owner create requests SHOULD include
  `issuer + provider + tenant + subject` plus the rest of the normalized
  create request.
- Before the first successful create, Spritz may resolve again on retried
  requests with the same idempotency key.
- Once a request has resolved successfully, Spritz SHOULD persist the resolved
  owner in the idempotency reservation payload used for that create attempt.
- After a successful create, retries with the same idempotency key MUST replay
  the same created instance even if the external mapping later changes.
- Resolver failure or timeout MUST leave no partially created instance behind.

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
    "subject": "6f0f9d4f-9b0e-4d52-8c3a-ef0fd64b9b9f"
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

- Resolver issuer MUST be bound to the authenticated service principal.
- A service principal MUST be allowed to resolve only within its own issuer
  unless explicitly granted broader authority.
- Provider allowlists and tenant allowlists SHOULD be policy-controlled per
  issuer.
- Resolver selection and resolver credentials SHOULD be policy-controlled per
  service principal.

## V1 Consistency Model

Spritz should treat the external resolver as the source of truth.

Safe default behavior for v1:

- no durable copy of identity mappings in Spritz core
- no cache required
- synchronous resolution during create

If a mapping changes in the external system, future creates should follow the
current resolver answer.

Existing instances keep their existing owner. Resolution affects only future
create operations.

## Microsoft Teams Guidance

The first concrete provider should be `msteams`.

Provider guidance:

- `provider` value: `msteams`
- `tenant` MUST be the Microsoft Entra tenant ID
- `subject` MUST be the Microsoft Entra user object ID
- If the bot receives a Teams chat-surface identifier such as a `29:` ID, the
  integration must translate it to the canonical Entra user object ID before
  calling Spritz

Spritz core should not attempt to normalize Teams identifiers itself. The Teams
adapter or authoritative resolver must keep that canonical subject format
consistent.

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
7. `spz` accepts either direct owner ID input or external identity input, but
   rejects requests that try to send both.
8. Service-principal create responses omit `ownerId` when the request used
   `ownerRef.type=external`.
9. Retrying a successful create with the same idempotency key replays the same
   instance even if the resolver mapping later changes.
10. Microsoft Teams requests use Entra tenant ID plus Entra user object ID as
    the canonical external identity.

## Recommended Sequencing

### Phase 1 - Public API and internal abstraction

- Add `ownerRef` to create requests.
- Derive resolver issuer from authenticated service principal.
- Define the internal resolver interface and typed result states.
- Make external-ID-only bot calls the preferred integration path.
- Add `spz` flags for external owner input while keeping direct `--owner-id`
  support.
- Add resolver policy binding to service-principal configuration.

### Phase 2 - Resolver-backed provisioning

- Implement the HTTP resolver adapter.
- Add backend-only resolver authentication.
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
