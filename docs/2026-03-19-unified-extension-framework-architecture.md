---
date: 2026-03-19
author: Onur Solmaz <onur@textcortex.com>
title: Unified Extension and Policy Framework Architecture
tags: [spritz, api, extensions, auth, provisioning, architecture]
---

## Overview

This document defines a unified extension and policy framework for Spritz.

The goal is to stop adding new feature-specific integration surfaces for
provisioning, auth, login metadata, identity resolution, and future linking
workflows. Instead, Spritz should expose one durable control-plane model that
combines:

- extensions for resolving facts,
- workspace classes for defining behavior,
- a policy engine for enforcing intent,
- a stable lifecycle for turning requests into concrete resources.

This keeps `spz`, the UI, and any service-principal caller on the same control
plane path:

- clients send a normal Spritz request,
- Spritz resolves any configured extension steps,
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

- Define one standard model for API-driven integration behavior and policy.
- Keep `spz` as the thin canonical client for all provisioning flows.
- Let create-time preset-specific logic run natively inside Spritz.
- Reuse the same extension model for owner resolution, runtime binding, login
  metadata, and future identity-linking flows.
- Make workspace behavior durable through first-class workspace classes instead
  of image-name checks or one-off booleans.
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

Spritz should still own the enforcement model for workspace creation, access,
discovery, credentials, and lifecycle. Deployment-owned systems may resolve
facts, but Spritz should apply those facts through a stable policy model.

### Extensions are operation-based

Extensions should attach to a small set of named operations instead of being
hard-coded into feature-specific code paths.

Examples:

- `owner.resolve`
- `preset.create.resolve`
- `auth.login.metadata`
- `identity.link.resolve`
- `workspace.lifecycle.notify`

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

### Mutations are explicit and limited

An extension should not arbitrarily rewrite requests. It should return a
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
- `WorkspaceClass`
- `ResolvedFacts`
- `PolicyDecision`
- `Materialization`

Meaning:

- `Principal`: who is asking
- `Intent`: what they are trying to do, such as create, read, use, share, or
  manage
- `Resource`: the workspace or instance being acted on
- `Preset`: the user-facing entry point and default bundle
- `WorkspaceClass`: the durable behavioral class of the workspace
- `ResolvedFacts`: canonical facts returned by extensions or internal
  resolution
- `PolicyDecision`: the allow, deny, or approval-required decision plus the
  derived access and runtime rules
- `Materialization`: the final spec, ACL, credentials, lifecycle, and metadata
  written by Spritz

This is the abstraction that should survive feature churn. Extensions are one
part of it, not the whole story.

## Unified Model

Spritz should introduce one extension registry in the API layer.

High-level model:

- extensions are declared in API config,
- each extension has an `id`, `kind`, `operation`, and transport,
- operations decide when an extension is invoked,
- Spritz builds a standard request envelope,
- the extension returns a standard response envelope,
- Spritz validates and applies allowed mutations.

This turns feature-specific hooks into data-driven extension configuration.

Spritz should also introduce a first-class policy layer for workspace behavior.

The unified model should therefore have two parts:

- extensions resolve facts and provide bounded mutations,
- workspace classes and policy decide how a workspace may be created,
  discovered, shared, accessed, credentialed, and operated.

Both parts are needed. Extensions alone are not enough for long-term security
or collaboration requirements.

## Stable Lifecycle Phases

Spritz should standardize a small set of lifecycle phases:

- `resolve`
- `authorize`
- `materialize`
- `notify`

### Resolve

Gather or normalize facts needed to make a decision.

### Authorize

Evaluate the request against the workspace class and resolved facts.

### Materialize

Write the canonical resource state.

### Notify

Emit post-decision or post-create hooks.

This phase model is more durable than creating a separate subsystem for each
new feature.

## Extension Kinds

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

## Workspace Classes

Spritz should define a first-class `WorkspaceClass` concept for workspace
behavior.

A workspace class is attached to a preset and governs how a created workspace
is meant to behave.

Examples:

- `personal-agent`
- `internal-collab`
- `privileged-dev`
- `restricted-runtime`

Each class should be versioned and treated as part of the canonical workspace
state.

Suggested stored fields on each workspace:

- owner
- workspace class ID
- workspace class version
- resolved extension context
- materialized access control entries

This gives Spritz a portable way to support very different workspace types
without encoding behavior into image names or one-off feature flags.

`Policy profile` may still exist as an internal implementation term, but the
public architectural concept should be `WorkspaceClass`. It is a more durable
control-plane abstraction and better matches other long-lived infrastructure
patterns.

## Policy Facets

Each workspace class should define a small, stable set of behavior facets.

Suggested facets:

- `creationPolicy`
- `ownershipPolicy`
- `discoveryPolicy`
- `accessPolicy`
- `sessionPolicy`
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

- `discover`: see that the workspace exists in lists, search, or discovery
  surfaces
- `read`: view metadata, ownership, and non-secret state
- `use`: open the workspace and interact with its primary runtime
- `chat`: send normal ACP or chat input
- `terminal`: open shell-like or terminal-style access
- `share`: grant other principals additional access
- `manage`: mutate config, restart, delete, or reassign allowed state

These verbs should be enforced by Spritz regardless of which extension resolved
the create-time facts.

## Standard Extension Configuration

Spritz should expose one config surface for extensions, for example:

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

## Standard Request Envelope

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
    "namespace": "workspaces",
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

## Standard Response Envelope

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

Spritz should only allow extensions to mutate a narrow set of fields.

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
- arbitrary credential attachment that bypasses the workspace class policy.

Mutation allowlists should be enforced in code per operation.

## Presets, Images, and Policy

Security behavior should not be keyed directly off image names.

Instead:

- presets should reference workspace classes,
- workspace classes should define allowed behavior,
- classes may optionally restrict which images are valid for that preset or
  class.

This matters because two workspaces may use similar runtime images but require
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

This is a typical fit for a `personal-agent` workspace class.

### Internal collaborative developer runtime

Example intent:

- owned by one human for accountability,
- discoverable to a trusted internal population,
- readable by a broader internal audience,
- usable by an explicitly allowed collaboration group,
- simultaneous interaction allowed,
- privileged credentials allowed only through this workspace class,
- stronger audit and session controls.

This is a typical fit for a `privileged-dev` or `internal-collab` policy
profile.

The important point is that ownership and collaboration do not have to be the
same thing. Spritz can preserve a single owner while still allowing multi-user
read and use access through policy.

## Native Preset Create Resolution

This is the most important new capability.

Spritz should add first-class `presetInputs` to create requests and allow a
preset-scoped resolver to run before workspace creation.

That enables native flows such as:

- caller uses `spz create --preset assistant-runtime`,
- caller optionally passes selector inputs,
- Spritz resolves the owner,
- Spritz invokes the preset create resolver,
- the resolver returns required runtime binding fields,
- Spritz applies them and creates the workspace.

This keeps the CLI thin and guarantees that create-time binding logic is always
enforced server-side.

The create resolver should return facts such as:

- resolved owner,
- resolved runtime or agent binding,
- required `serviceAccountName`,
- normalized collaboration context,
- classification annotations.

It should not directly decide final permissions. Those should still be derived
from the selected workspace class.

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

Extension results must be included in the effective resolved create payload.

Rules:

- the canonical request fingerprint MUST include applied extension mutations,
- replay MUST restore the same resolved result,
- pending idempotent reservations MUST not re-run a resolver with different
  semantics,
- extension output SHOULD be stored in the resolved idempotent payload when it
  affects create.

This follows the same principle already used for external owner resolution.

## Security and Policy

- only explicitly configured extensions may run,
- extensions should be invoked only for allowed principals and operations,
- service-principal scopes still gate whether the operation may proceed,
- extension auth headers and secrets must stay in deployment config,
- Spritz should reject unconfigured extension references,
- resolver outputs must be validated before any mutation is applied.

Additional policy requirements:

- the selected workspace class must be part of the canonical request,
- create must fail if required profile inputs or required resolver outputs are
  missing,
- credentials attached to a workspace must be allowed by the workspace class,
- discovery and collaboration rules must be enforced by Spritz even when a
  resolver suggests additional context,
- post-create reads and interactions must use the same owner plus ACL plus
  policy evaluation path regardless of how the workspace was created.

For HTTP extensions:

- require explicit timeouts,
- treat network failures as `unavailable`,
- log request IDs and extension IDs,
- avoid passing secrets or unnecessary request fields.

## Observability

Each extension invocation should log:

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

## Migration Plan

### Phase 1: Introduce the extension registry

- add generic extension config parsing and validation,
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

### Phase 4: Add workspace classes

- add first-class workspace class configuration,
- let presets reference workspace classes,
- store workspace class ID and version on created workspaces,
- enforce access verbs, discovery, credential policy, and lifecycle through the
  profile engine.

### Phase 5: Move login metadata under the same roof

- represent deployment-specific login and refresh metadata as an auth-provider
  extension,
- keep UI config behavior backward-compatible during migration.

### Phase 6: Add future identity-link and lifecycle operations

- reuse the same framework for linking and post-create hooks instead of adding
  new one-off config paths.

## Validation

Validation should include:

- config parsing tests for the extension registry,
- resolver transport tests,
- create-path tests proving resolver mutations affect idempotency,
- preset create tests proving `presetInputs` are passed through and validated,
- policy-profile tests for discover, read, use, chat, terminal, share, and
  manage behavior,
- tests proving credentials and privileged runtime features are gated by
  profile policy,
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
