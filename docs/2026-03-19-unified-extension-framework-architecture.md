---
date: 2026-03-19
author: Onur Solmaz <onur@textcortex.com>
title: Unified Admission, InstanceClass, and Policy Architecture
tags: [spritz, api, extensions, auth, provisioning, architecture]
---

## Overview

This document defines a unified admission, instance class, and policy
architecture for Spritz.

The goal is to stop adding new feature-specific integration surfaces for
provisioning, auth, login metadata, identity resolution, and future linking
workflows. Instead, Spritz should expose one durable control-plane model that
combines:

- admission-style resolvers and hooks for resolving facts,
- instance classes for defining behavior,
- a policy engine for enforcing intent,
- a stable lifecycle for turning requests into concrete resources.

This keeps `spz`, the UI, and any service-principal caller on the same control
plane path:

- clients send a normal Spritz request,
- Spritz resolves any configured admission steps,
- Spritz evaluates class-driven policy,
- Spritz applies validated mutations,
- Spritz materializes the canonical resource.

The framework must remain portable and deployment-agnostic. Spritz must define
the standard, while deployment-owned systems remain responsible for
business-specific decisions such as account linking, runtime binding, or other
resolved facts. Spritz itself should remain the place where permissions,
lifecycle, collaboration, and credential rules are enforced.

## Problem Statement

Spritz already has more than one extension-style integration point:

- bearer and browser auth configuration,
- login metadata exposed to the UI,
- external owner resolution during provisioning,
- preset-based create-time defaults,
- provisioner-specific behavior for service principals.

These pieces work, but they are managed as separate subsystems with separate
config formats and separate execution paths.

That creates four problems:

- new integrations tend to become one-off configuration surfaces,
- similar concepts such as "resolve this identity" and "resolve this preset
  binding" do not share a standard contract,
- clients cannot rely on one predictable native create path,
- Spritz becomes harder to evolve because extension behavior is scattered across
  unrelated files and environment variables.

## Goals

- Define one standard model for API-driven admission and policy behavior.
- Keep `spz` as the thin canonical client for all provisioning flows.
- Let create-time preset-specific logic run natively inside Spritz.
- Reuse the same admission model for owner resolution, runtime binding, login
  metadata, and future identity-linking flows.
- Make `InstanceClass` the durable behavioral resource instead of image-name
  checks or one-off booleans.
- Support delegated runtime service access, charging, and plan enforcement for
  internal services such as model gateways.
- Prefer declarative, typed policy over opaque feature-specific branching.
- Keep resolved facts deployment-owned while keeping policy enforcement inside
  Spritz.
- Standardize config, request/response shape, logging, timeout handling,
  idempotency, access verbs, and failure mapping.

## Non-goals

- Embedding deployment-specific business logic in Spritz core.
- Turning Spritz into a general-purpose workflow engine.
- Allowing extensions to mutate arbitrary parts of the system without policy
  limits.
- Replacing presets, service-principal scopes, or owner semantics.
- Making `spz` implement authorization or fallback logic on its own.

## Design Principles

### Spritz owns orchestration, not business policy

Spritz should decide when an extension runs, how it is authenticated, how its
result is validated, and how it affects the canonical request.

Deployments should decide the business answer to extension questions such as:

- which external user maps to which owner,
- which runtime binding belongs to a selected target,
- which login URL or refresh URL should be presented,
- which identity-link status is valid.

Spritz should still own the enforcement model for instance creation, access,
discovery, credentials, and lifecycle. Deployment-owned systems may resolve
facts, but Spritz should apply those facts through a stable policy model.

### Resolvers should be admission-style

Resolvers should be narrow, phase-scoped, and admission-like instead of acting
as a general plugin system.

They should attach to a small set of named operations instead of being
hard-coded into feature-specific code paths.

Examples:

- `owner.resolve`
- `preset.create.resolve`
- `auth.login.metadata`
- `identity.link.resolve`
- `instance.lifecycle.notify`

### InstanceClass is the primary control-plane resource

`Preset` is a user-facing entry point.

`InstanceClass` is the durable control-plane abstraction that defines
behavior, access, credential posture, and lifecycle.

That distinction should remain stable even if presets, images, or caller types
change over time.

### Clients stay thin

`spz`, UI callers, and service integrations should submit normal Spritz
requests. They should not reproduce fallback rules, runtime binding logic, or
owner lookup logic locally.

### Facts and policy must stay separate

Extensions should resolve facts.

Examples:

- which owner was resolved,
- which runtime or agent binding applies,
- which collaboration context was selected,
- which identity or team mapping is authoritative.

Policy should decide what those resolved facts mean for creation, discovery,
read access, interactive use, collaboration, credentials, and lifecycle.

This separation keeps extension logic narrow and prevents deployment-specific
permission behavior from leaking into arbitrary resolver code.

### Policy should be declarative and typed

Instance behavior should be described as structured policy data attached to
`InstanceClass`, not as scattered booleans or opaque callback code.

The policy engine should consume:

- `Intent`
- `InstanceClass`
- `ResolvedFacts`
- the current `Resource` state

and produce one typed decision that Spritz can enforce consistently across
create, discover, read, use, share, and manage flows.

### Resource identity should stay explicit

Long-lived control planes age better when class identity, owner identity, and
resolved bindings are explicit parts of resource state.

Spritz should therefore persist:

- owner identity,
- runtime principal identity,
- instance class ID and version,
- policy-relevant resolved facts,
- charged-principal selection when it is material to later delegated service
  use,
- materialized access control entries.

### Mutations are explicit and limited

A resolver or hook should not arbitrarily rewrite requests. It should return a
validated mutation set with a narrow allowed surface.

### Extension results must affect idempotency

If an extension changes the effective create request, that resolved result must
be included in idempotency and replay logic. Otherwise identical idempotency
keys could replay into different runtime bindings.

## Canonical Control-Plane Model

The durable Spritz model should be:

- `Principal`
- `Intent`
- `Resource`
- `Preset`
- `InstanceClass`
- `ResolvedFacts`
- `PolicyDecision`
- `Materialization`

Meaning:

- `Principal`: who is asking
- `Intent`: what they are trying to do, such as create, read, use, share, or
  manage
- `Resource`: the instance or instance being acted on
- `Preset`: the user-facing entry point and default bundle
- `InstanceClass`: the durable behavioral class of the instance
- `ResolvedFacts`: canonical facts returned by extensions or internal
  resolution
- `PolicyDecision`: the allow, deny, or approval-required decision plus the
  derived access and runtime rules
- `Materialization`: the final spec, ACL, credentials, lifecycle, and metadata
  written by Spritz

This is the abstraction that should survive feature churn. Resolvers and hooks
are one part of it, not the whole story.

For runtime-backed instances, Spritz should also distinguish several principal
roles explicitly:

- `ownerPrincipal`: the accountable owner recorded on the instance
- `runtimePrincipal`: the identity the running instance presents to internal
  services
- `requestActor`: the principal currently asking Spritz or a delegated service
  to do something
- `chargedPrincipal`: the principal whose plan, budget, or quota should be
  charged for delegated service use

These roles may coincide for simple personal instances, but they should remain
separate in the model so that future collaborative, team-owned, or
privileged-runtime classes do not hard-code one billing or authorization shape.

## Admission and Policy Model

Spritz should introduce one registry for admission-style resolvers and hooks in
the API layer.

High-level model:

- extensions are declared in API config,
- each extension has an `id`, `kind`, `operation`, and transport,
- operations decide when an extension is invoked,
- Spritz builds a standard request envelope,
- the extension returns a standard response envelope,
- Spritz validates and applies allowed mutations.

This turns feature-specific hooks into data-driven admission configuration.

Spritz should also introduce a first-class policy layer for instance behavior.

The unified model should therefore have two parts:

- resolvers and hooks resolve facts and provide bounded mutations,
- instance classes and policy decide how an instance may be created,
  discovered, shared, accessed, credentialed, and operated.

Both parts are needed. Resolvers alone are not enough for long-term security or
collaboration requirements.

## Stable Lifecycle Phases

Spritz should standardize a small set of lifecycle phases:

- `resolve`
- `authorize`
- `materialize`
- `notify`

### Resolve

Gather or normalize facts needed to make a decision.

### Authorize

Evaluate the request against the instance class and resolved facts.

### Materialize

Write the canonical resource state.

### Notify

Emit post-decision or post-create hooks.

This phase model is more durable than creating a separate subsystem for each
new feature.

## Admission Kinds

### Resolver

A resolver runs synchronously before Spritz continues an operation.

Typical uses:

- resolve an external owner into an internal owner,
- resolve a preset-specific runtime binding,
- resolve an identity-link target,
- resolve policy-driven defaults that must be fixed before create.

### Auth Provider

An auth provider exposes login and refresh metadata for browser clients and
other consumers that need to know how to authenticate against the deployment.

Typical uses:

- login URL metadata,
- return-to behavior,
- token refresh endpoint metadata.

### Lifecycle Hook

A lifecycle hook runs after a core operation succeeds or fails.

Typical uses:

- notify an external control plane,
- emit lifecycle events,
- run bookkeeping after delete or expiration.

Resolvers are the most important initial kind because they cover the existing
external owner flow and the create-time preset binding problem.

## InstanceClass as a First-Class Resource

The long-term target should be for `InstanceClass` to become a first-class API
resource, not just deployment config.

In the short term, Spritz may define instance classes in configuration for
speed of implementation. The stable target should still be:

- presets reference a named `InstanceClass`,
- `InstanceClass` has a versioned schema,
- instances record the resolved class and class version,
- policy evaluation consumes the class as structured data,
- clients and operators can reason about class identity independently of
  presets.

This is closer to long-lived control-plane patterns such as class resources in
other infrastructure systems and gives Spritz a more durable contract than a
collection of loosely related config fragments.

Example long-term shape:

```yaml
apiVersion: spritz.dev/v1alpha1
kind: InstanceClass
metadata:
  name: personal-agent
spec:
  creation:
    requireOwner: true
    requiredResolvedFields: [serviceAccountName]
  access:
    verbs:
      discover: owner_only
      read: owner_only
      use: owner_only
      share: denied
      manage: owner_only
  credentials:
    allowedClasses: [safe-runtime]
  lifecycle:
    ttlPolicy: retained
  audit:
    mode: standard
```

## Instance Classes

Spritz should define a first-class `InstanceClass` concept for instance
behavior.

An instance class is attached to a preset and governs how a created instance
is meant to behave.

Examples:

- `personal-agent`
- `internal-collab`
- `privileged-dev`
- `restricted-runtime`

Each class should be versioned and treated as part of the canonical instance
state.

Suggested stored fields on each instance:

- owner
- instance class ID
- instance class version
- resolved extension context
- materialized access control entries

This gives Spritz a portable way to support very different instance types
without encoding behavior into image names or one-off feature flags.

`Policy profile` may still exist as an internal implementation term, but the
public architectural concept should be `InstanceClass`. It is a more durable
control-plane abstraction and better matches other long-lived infrastructure
patterns.

## Declarative Policy Model

`InstanceClass` should describe policy as typed data, not as arbitrary code.

At minimum, the policy engine should be able to evaluate:

- creation requirements,
- allowed access verbs by principal set,
- collaboration and concurrency rules,
- allowed credential classes,
- lifecycle and TTL rules,
- audit and approval requirements.

Resolvers may supply facts used by policy, but they should not become a second
policy engine. A resolver may say "the resolved binding is runtime-123"; it
should not say "therefore everyone in engineering gets terminal access."

That decision belongs to Spritz policy evaluation over the selected
`InstanceClass`.

## Policy Facets

Each instance class should define a small, stable set of behavior facets.

Suggested facets:

- `creationPolicy`
- `ownershipPolicy`
- `discoveryPolicy`
- `accessPolicy`
- `sessionPolicy`
- `delegationPolicy`
- `credentialPolicy`
- `lifecyclePolicy`
- `auditPolicy`

This is a better long-term shape than introducing a new boolean every time a
new security or collaboration requirement appears.

## Access Verbs

Spritz should model access as explicit verbs instead of a single generic access
bit.

Suggested verbs:

- `discover`
- `read`
- `use`
- `chat`
- `terminal`
- `share`
- `manage`

Expected meanings:

- `discover`: see that the instance exists in lists, search, or discovery
  surfaces
- `read`: view metadata, ownership, and non-secret state
- `use`: open the instance and interact with its primary runtime
- `chat`: send normal ACP or chat input
- `terminal`: open shell-like or terminal-style access
- `share`: grant other principals additional access
- `manage`: mutate config, restart, delete, or reassign allowed state

These verbs should be enforced by Spritz regardless of which extension resolved
the create-time facts.

## Delegated Service Intents

Access verbs are not enough for internal service calls made by a running
instance.

Spritz should also model delegated service intents for calls from instance
runtimes into deployment-owned services. These intents are not normal
instance-resource verbs. They represent downstream actions such as:

- `llm.invoke`
- `tool.call`
- `knowledge.read`
- `secret.fetch`

These intents should be authorized by `InstanceClass` policy using the bound
runtime identity and the resolved charged principal.

This keeps downstream service authorization and accounting on the same
control-plane model instead of letting every service invent its own ad hoc
identity rules.

## Runtime Service Delegation

Some instance classes will call internal services such as model gateways,
search services, or deployment-owned tool backends.

Those calls should not be modeled as browser-user calls forwarded through the
instance. They should be modeled as delegated runtime service access.

The durable rules should be:

- the instance authenticates to internal services as its bound
  `runtimePrincipal`
- internal services derive or receive the canonical `chargedPrincipal`
  according to Spritz policy
- the charged principal's plan, budget, model entitlement, or quota is checked
  at the delegated service boundary
- the request fails there if the charged principal is not allowed to use that
  capability
- raw browser session tokens or other end-user tokens should not be handed to
  the container just so it can call internal services

This is especially important for model-gateway traffic. A personal instance may
ultimately bill the bound user, while a collaborative or privileged instance
may later bill an owner, team budget, or some other deployment-defined
principal. That choice must be policy-driven per `InstanceClass`, not baked
into the runtime image or the client.

Spritz therefore needs a first-class way to represent:

- which delegated service intents are allowed for a class
- which credential classes or identity assertions are valid for those intents
- how the `chargedPrincipal` is selected
- whether the current `requestActor` must also be allowed to trigger the
  delegated action

## Delegation Policy

`delegationPolicy` should define the rules for downstream service use by an
instance runtime.

At minimum it should support:

- allowed delegated service intents such as `llm.invoke`
- the permitted authentication mode for the runtime principal
- the charged-principal selection rule, such as owner, bound user, team budget,
  or another resolved principal
- whether runtime calls are actor-mediated or may proceed solely on runtime
  identity
- allowed model, tool, or service classes
- auditing requirements for delegated service use

Example long-term shape:

```yaml
delegation:
  intents:
    llm.invoke:
      authn: runtime_principal
      chargedPrincipal: bound_user
      actorCheck: required
      allowedServiceClasses: [standard-llm]
```

Spritz should own this policy contract even if an internal service such as a
LiteLLM-style gateway ultimately performs the final entitlement or billing
check.

## Standard Admission Configuration

Spritz should expose one config surface for admission-style resolvers and
hooks, for example:

```yaml
api:
  extensions:
    - id: external-owner
      kind: resolver
      operation: owner.resolve
      transport:
        type: http
        url: https://resolver.example.com/v1/owners/resolve
        authHeaderEnv: SPRITZ_INTERNAL_TOKEN
        timeout: 5s

    - id: runtime-binding
      kind: resolver
      operation: preset.create.resolve
      match:
        presetIds: [assistant-runtime]
      transport:
        type: http
        url: https://resolver.example.com/v1/runtime-bindings/resolve
        authHeaderEnv: SPRITZ_INTERNAL_TOKEN
        timeout: 5s

    - id: web-login
      kind: auth_provider
      operation: auth.login.metadata
      provider:
        loginUrl: https://console.example.com/login
        refreshUrl: https://console.example.com/api/auth/refresh
```

Rules:

- `id` MUST be unique.
- `kind` MUST be one of the supported extension kinds.
- `operation` MUST be one of the supported extension operations.
- `match` MAY further restrict invocation, such as by preset ID.
- HTTP transport MUST support timeout and auth configuration.

## Standard Admission Request Envelope

Spritz should call resolvers with one common request contract:

```json
{
  "version": "v1",
  "extensionId": "runtime-binding",
  "kind": "resolver",
  "operation": "preset.create.resolve",
  "requestId": "req-123",
  "principal": {
    "id": "service-123",
    "type": "service",
    "scopes": ["spritz.instances.create"]
  },
  "context": {
    "namespace": "instances",
    "presetId": "assistant-runtime"
  },
  "input": {
    "owner": {
      "id": "user-123"
    },
    "ownerRef": {
      "type": "external",
      "provider": "chat",
      "subject": "abc"
    },
    "presetInputs": {
      "targetId": "runtime-123"
    },
    "spec": {
      "image": "registry.example.com/runtime:latest"
    }
  }
}
```

Notes:

- `principal` is already authenticated by Spritz.
- `context` is canonicalized by Spritz, not by the caller.
- `input` contains only the operation-specific payload needed by the resolver.
- request shape should stay strongly typed per operation even if the transport
  envelope is generic.

## Standard Admission Response Envelope

Resolvers should answer with one common response contract:

```json
{
  "status": "resolved",
  "output": {
    "targetId": "runtime-123"
  },
  "mutations": {
    "ownerId": "user-123",
    "spec": {
      "serviceAccountName": "runtime-user-123"
    },
    "annotations": {
      "spritz.sh/resolved-target-id": "runtime-123"
    }
  }
}
```

Supported statuses should include:

- `resolved`
- `unresolved`
- `forbidden`
- `ambiguous`
- `invalid`
- `unavailable`

Spritz should map these to stable API responses and should not require each
feature-specific resolver to invent its own error format.

## Allowed Mutation Surface

Spritz should only allow resolvers and hooks to mutate a narrow set of fields.

Initial allowed surface:

- resolved owner ID
- selected spec fields such as `serviceAccountName`
- selected annotations
- selected labels
- selected policy-facing resolved metadata such as collaboration context or
  binding identity
- normalized preset metadata returned in response payloads

Not allowed initially:

- arbitrary namespace rewrite,
- arbitrary image rewrite unless explicitly allowed by preset policy,
- arbitrary owner change after create,
- arbitrary post-create permission grants,
- arbitrary credential attachment that bypasses the instance class policy.

Mutation allowlists should be enforced in code per operation.

This should feel closer to admission-style mutation than to a general-purpose
extension runtime.

## Presets, Images, and Policy

Security behavior should not be keyed directly off image names.

Instead:

- presets should reference instance classes,
- instance classes should define allowed behavior,
- classes may optionally restrict which images are valid for that preset or
  class.

This matters because two instances may use similar runtime images but require
very different collaboration and credential rules.

For example:

- a personal assistant runtime may need private ownership and restricted
  credentials,
- a privileged internal development runtime may need broad internal discovery,
  shared interaction, and stronger audit requirements.

Those differences belong in policy, not in image-name checks.

## Example Policy Shapes

### Personal agent runtime

Example intent:

- owned by one human,
- may require a create-time runtime binding,
- private by default,
- not broadly discoverable,
- restricted runtime credentials,
- collaboration disabled or narrowly scoped.

This is a typical fit for a `personal-agent` instance class.

Typical delegated-service posture:

- runtime principal is bound to the selected runtime identity
- charged principal is the bound user
- model calls fail if the bound user lacks plan entitlement
- browser tokens are never forwarded into the runtime just to satisfy model
  billing

### Internal collaborative developer runtime

Example intent:

- owned by one human for accountability,
- discoverable to a trusted internal population,
- readable by a broader internal audience,
- usable by an explicitly allowed collaboration group,
- simultaneous interaction allowed,
- privileged credentials allowed only through this instance class,
- stronger audit and session controls.

This is a typical fit for a `privileged-dev` or `internal-collab` instance
class.

The important point is that ownership and collaboration do not have to be the
same thing. Spritz can preserve a single owner while still allowing multi-user
read and use access through policy.

Typical delegated-service posture:

- runtime principal may represent the instance itself or a bound team runtime
- charged principal may be owner, team budget, or another policy-selected
  principal
- delegated service use may require both runtime identity and an allowed
  request actor
- privileged model or database access is granted by class policy, not by image
  name

## Native Preset Create Resolution

This is the most important new capability.

Spritz should add first-class `presetInputs` to create requests and allow a
preset-scoped resolver to run before instance creation.

That enables native flows such as:

- caller uses `spz create --preset assistant-runtime`,
- caller optionally passes selector inputs,
- Spritz resolves the owner,
- Spritz invokes the preset create resolver,
- the resolver returns required runtime binding fields,
- Spritz applies them and creates the instance.

This keeps the CLI thin and guarantees that create-time binding logic is always
enforced server-side.

The create resolver should return facts such as:

- resolved owner,
- resolved runtime or agent binding,
- required `serviceAccountName`,
- normalized collaboration context,
- classification annotations.

It should not directly decide final permissions. Those should still be derived
from the selected instance class.

## CLI Contract

`spz` should remain a thin client over the native API shape.

It should support `presetInputs` directly, for example:

```bash
spz create \
  --preset assistant-runtime \
  --owner-provider chat \
  --owner-subject 123456 \
  --preset-input targetId=runtime-123
```

or:

```bash
spz create \
  --preset assistant-runtime \
  --owner-provider chat \
  --owner-subject 123456
```

If the resolver defines fallback behavior for missing inputs, that fallback must
run in the API, not in the CLI.

## Idempotency and Replay

Resolver results must be included in the effective resolved create payload.

Rules:

- the canonical request fingerprint MUST include applied extension mutations,
- replay MUST restore the same resolved result,
- pending idempotent reservations MUST not re-run a resolver with different
  semantics,
- resolver output SHOULD be stored in the resolved idempotent payload when it
  affects create.

This follows the same principle already used for external owner resolution.

## Authorization and Admission Rules

- only explicitly configured resolvers and hooks may run,
- resolvers and hooks should be invoked only for allowed principals and
  operations,
- service-principal scopes still gate whether the operation may proceed,
- resolver and hook auth headers and secrets must stay in deployment config,
- Spritz should reject unconfigured extension references,
- resolver outputs must be validated before any mutation is applied.

Additional policy requirements:

- the selected instance class must be part of the canonical request,
- create must fail if required class inputs or required resolver outputs are
  missing,
- credentials attached to an instance must be allowed by the instance class,
- delegated internal service use must be allowed by the instance class and
  resolved against runtime and charged-principal policy,
- discovery and collaboration rules must be enforced by Spritz even when a
  resolver suggests additional context,
- post-create reads and interactions must use the same owner plus ACL plus
  policy evaluation path regardless of how the instance was created.

For HTTP resolvers and hooks:

- require explicit timeouts,
- treat network failures as `unavailable`,
- log request IDs and extension IDs,
- avoid passing secrets or unnecessary request fields.

For delegated runtime service calls:

- internal services should authenticate the runtime via Spritz-managed identity,
  not raw browser tokens
- the delegated-service boundary should enforce plan, quota, or entitlement
  checks for the resolved charged principal
- service calls should carry enough canonical identity metadata for audit
  attribution without leaking end-user credentials into the runtime

## Observability

Each resolver or hook invocation should log:

- extension ID,
- operation,
- principal ID and type,
- request ID,
- outcome status,
- duration,
- whether the result came from replay or a live call.

Suggested metrics:

- invocation count by extension ID and status,
- latency histogram by extension ID,
- unavailable/error count,
- replay reuse count for resolved payloads.
- delegated-service calls by instance class, intent, charged-principal type,
  and outcome.

## Migration Plan

### Phase 1: Introduce the resolver and hook registry

- add generic resolver and hook config parsing and validation,
- add a standard HTTP executor,
- add request and response envelope types,
- keep existing feature-specific code working.

### Phase 2: Migrate external owner resolution

- adapt the existing external owner resolver to `owner.resolve`,
- preserve the current public API shape,
- keep a compatibility layer for existing env vars.

### Phase 3: Add preset create resolution

- add `presetInputs` to create requests,
- add `preset.create.resolve`,
- allow presets to require resolver-produced fields before create succeeds.

### Phase 4: Add instance classes

- add first-class instance class configuration,
- let presets reference instance classes,
- store instance class ID and version on created instances,
- enforce access verbs, discovery, credential policy, and lifecycle through the
  policy engine.
- keep config-backed classes as the short-term shape, but target a first-class
  `InstanceClass` API resource.

### Phase 5: Add runtime service delegation policy

- add `delegationPolicy` and delegated service intents such as `llm.invoke`,
- define runtime principal and charged-principal handling,
- require internal services to consume Spritz-managed runtime identity instead
  of raw browser tokens,
- make charged-principal selection policy-driven per `InstanceClass`.

### Phase 6: Move login metadata under the same roof

- represent deployment-specific login and refresh metadata as an auth-provider
  extension,
- keep UI config behavior backward-compatible during migration.

### Phase 7: Add future identity-link and lifecycle operations

- reuse the same framework for linking and post-create hooks instead of adding
  new one-off config paths.

## Validation

Validation should include:

- config parsing tests for the resolver and hook registry,
- resolver transport tests,
- create-path tests proving resolver mutations affect idempotency,
- preset create tests proving `presetInputs` are passed through and validated,
- instance-class policy tests for discover, read, use, chat, terminal, share,
  and manage behavior,
- tests proving credentials and privileged runtime features are gated by
  instance-class policy,
- delegated-service tests proving runtime principal authentication and
  charged-principal selection behave as configured,
- tests proving model-gateway style failures surface correctly when the charged
  principal lacks entitlement,
- tests proving multiple principals can collaborate when allowed while owner
  metadata remains stable,
- compatibility tests for existing external owner resolution,
- CLI tests proving `spz` remains a thin request builder,
- auth/UI tests for migrated login metadata behavior.

## References

- [2026-03-11-external-provisioner-and-service-principal-architecture.md](2026-03-11-external-provisioner-and-service-principal-architecture.md)
- [2026-03-12-external-identity-resolution-api-architecture.md](2026-03-12-external-identity-resolution-api-architecture.md)
- [2026-03-13-spz-audience-and-external-owner-guidance.md](2026-03-13-spz-audience-and-external-owner-guidance.md)
- [2026-02-24-portable-authentication-and-account-architecture.md](2026-02-24-portable-authentication-and-account-architecture.md)
