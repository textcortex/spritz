---
date: 2026-03-19
author: Onur Solmaz <onur@textcortex.com>
title: Runtime Service Delegation Architecture
tags: [spritz, delegation, workload-identity, policy, auth]
---

## Overview

This document defines a first-class runtime service delegation model for
Spritz.

The goal is to let a running instance call internal deployment-owned services
through one durable control-plane contract instead of ad hoc image wiring,
opaque bearer forwarding, or service-specific identity conventions.

The model should support service calls such as:

- `llm.invoke`
- `tool.call`
- `knowledge.read`
- `secret.fetch`

Delegation should be authorized by `InstanceClass` policy, authenticated by the
bound runtime identity, and charged to the correct principal according to
declared policy.

## Goals

- Make delegated runtime service use a first-class Spritz capability.
- Reuse workload identity as the root proof of runtime identity.
- Keep instance class policy as the canonical place that decides:
  - which intents are allowed,
  - which authentication modes are accepted,
  - which principal should be charged,
  - whether a live actor is also required.
- Let downstream services receive a narrow, short-lived delegation token
  instead of raw browser or refresh tokens.
- Keep deployment-specific entitlement, pricing, and business logic outside
  Spritz.
- Provide one standard exchange and token format that any internal service can
  consume.

## Non-goals

- Do not move deployment-specific billing or entitlement rules into Spritz.
- Do not turn Spritz into a general-purpose service mesh or workflow engine.
- Do not let runtimes mint arbitrary tokens without class policy.
- Do not make images responsible for deciding who should be charged.
- Do not require browser-user session tokens inside runtime containers.

## Design Principles

### Runtime identity is the root proof

The runtime should authenticate as its bound runtime identity. A delegated
service call should start with proof such as a projected workload token, not a
forwarded browser bearer.

### Delegation is intent-scoped

Delegation should be requested for an explicit intent such as `llm.invoke`,
not for generic full access to downstream services.

### Delegation is audience-bound

A delegation token should be minted for one named downstream audience, such as
an internal model gateway or search backend. Tokens should not be reusable
across unrelated services.

### Charged principal is policy-driven

The principal that pays for or consumes quota from a delegated action should be
selected by `InstanceClass` policy, not by runtime image logic.

### Actor mediation is explicit

Some delegated actions may require both a valid runtime identity and a valid
request actor. Others may proceed on runtime identity alone. This must be an
explicit policy decision.

### Downstream services keep business decisions

Spritz should authorize delegation and assert canonical facts. The downstream
service should still decide its own product-specific entitlement, billing, and
resource semantics.

## Core Model

Spritz should treat delegated service use as a first-class control-plane
concept with these roles:

- `ownerPrincipal`
- `runtimePrincipal`
- `requestActor`
- `chargedPrincipal`

These roles may coincide for a simple personal instance, but they must remain
separate in the model.

Spritz should also model:

- `intent`
- `audience`
- `serviceClass`
- `delegationPolicy`
- `delegationToken`

## InstanceClass Delegation Policy

`InstanceClass` should declare which delegated service intents are allowed and
how they are authorized.

Example shape:

```yaml
delegation:
  intents:
    llm.invoke:
      authn: workload_identity
      chargedPrincipal: owner
      actorCheck: optional
      allowedServiceClasses: [standard-llm]
      token:
        ttl: 15m
        audienceMode: exact
        includeActor: true
```

The policy should support at minimum:

- allowed intents
- accepted runtime authentication mode
- charged-principal selection rule
- actor check mode:
  - `forbidden`
  - `optional`
  - `required`
- allowed service classes
- token TTL and audience rules
- audit requirements

## Delegation Exchange

Spritz should expose one generic token-exchange endpoint for runtimes, for
example:

```http
POST /internal/v1/runtime/delegations/exchange
```

The request should include:

- `instanceId`
- `intent`
- `audience`
- `serviceClass`
- `subjectToken`
- optional `actorToken`

The exchange handler should:

1. validate the runtime workload identity token
2. load the instance resource
3. verify that the presented runtime identity matches the instance binding
4. load the instance class and its delegation policy
5. verify that the requested intent, audience, and service class are allowed
6. resolve the canonical `chargedPrincipal`
7. enforce actor policy if the intent requires a request actor
8. mint a short-lived signed delegation token

This endpoint should be generic. It should not know service-specific pricing,
model names, or deployment business logic.

For the concrete control-plane read contract a downstream service can use to
validate runtime identity against an instance binding, see
[2026-03-19-runtime-binding-resolution-api-implementation-plan.md](2026-03-19-runtime-binding-resolution-api-implementation-plan.md).

## Delegation Token

The delegated credential should be a short-lived signed token with explicit
claims.

Suggested claims:

- `iss`
- `sub`
- `aud`
- `exp`
- `iat`
- `jti`
- `spritz_instance_id`
- `spritz_instance_class_id`
- `spritz_instance_class_version`
- `spritz_runtime_principal`
- `spritz_owner_principal`
- `spritz_charged_principal_ref`
- `spritz_intent`
- `spritz_service_class`
- optional `spritz_request_actor`

Important properties:

- short TTL
- explicit audience
- single intent
- sufficient identity context for audit
- no embedded product-specific pricing or entitlement state

## Resource State

Spritz should persist the minimum state needed to authorize delegation
deterministically.

That includes:

- owner principal
- runtime principal binding
- instance class ID and version
- policy-relevant resolved facts
- any materialized charged-principal reference when the policy requires it

This state should live on the canonical instance resource or in typed attached
state owned by Spritz. It should not depend on image-local environment
conventions alone.

## Downstream Service Contract

Downstream internal services should accept Spritz-issued delegation tokens and
use them as the authoritative runtime assertion.

At minimum the downstream service should:

1. verify token signature and audience
2. verify intent matches the requested operation
3. consume the charged-principal reference
4. apply product-specific entitlement, quota, or billing checks
5. record audit and usage under the correct principal

This lets Spritz stay generic while deployment-owned services keep their own
business rules.

## Actor-Mediated Delegation

Some instance classes may require a live actor in addition to runtime identity.

Examples:

- a collaborative developer runtime that may call tools only when triggered by
  an allowed collaborator
- a privileged runtime that may read secrets only when a current actor has the
  matching access verb

For those cases, the exchange endpoint should accept an optional actor token
and evaluate `actorCheck` policy before minting a delegation token.

## Security Properties

The architecture should guarantee:

- no raw browser or refresh token is required inside the runtime container
- runtime credentials are short-lived and scoped
- delegation is limited to explicit intents and audiences
- the charged principal comes from policy, not image logic
- downstream services can reject misuse without custom heuristics
- replay can be bounded with TTL, `jti`, and audit correlation

## Observability

Spritz should record:

- instance ID
- instance class
- runtime principal
- charged principal reference
- request actor, when present
- intent
- audience
- service class
- exchange outcome
- denial reason
- token TTL
- request ID

Recommended metrics:

- delegation exchange count by intent and outcome
- exchange latency by intent
- denial count by policy reason
- actor-mediated delegation rate
- downstream rejection count by audience

## Migration Path

1. Add first-class `delegation` policy to `InstanceClass`.
2. Persist explicit runtime-principal binding on instances.
3. Add the generic runtime delegation exchange endpoint.
4. Add signed delegation token issuance and verification helpers.
5. Migrate one downstream service class, such as `llm.invoke`, onto the
   delegation model.
6. Remove image-specific bearer-forwarding conventions once the delegated path
   is proven.

## Validation

Validation should include:

- policy parsing tests for delegation rules
- exchange tests for successful workload-identity delegation
- denial tests for:
  - wrong audience
  - wrong intent
  - wrong service class
  - mismatched runtime identity
  - missing or unauthorized actor
- token verification tests for downstream services
- idempotency and replay tests
- audit-log coverage tests
- compatibility tests for runtimes that do not use delegated services

## References

- [2026-03-19-runtime-binding-resolution-api-implementation-plan.md](2026-03-19-runtime-binding-resolution-api-implementation-plan.md)
- [2026-03-19-unified-extension-framework-architecture.md](2026-03-19-unified-extension-framework-architecture.md)
- [2026-03-11-external-provisioner-and-service-principal-architecture.md](2026-03-11-external-provisioner-and-service-principal-architecture.md)
- [2026-03-12-external-identity-resolution-api-architecture.md](2026-03-12-external-identity-resolution-api-architecture.md)
