---
date: 2026-03-11
author: Onur <onur@textcortex.com>
title: External Provisioner and Service Principal Architecture
tags: [spritz, provisioning, auth, cli, lifecycle, architecture]
---

## Overview

This document defines the target architecture for letting external automation
create Spritz workspaces for human users.

Typical examples include:

- a chat bot,
- an assistant running in another system,
- a workflow engine,
- a support or onboarding automation.

The target model is:

- Spritz remains the only control plane,
- the external system acts as a narrow service principal,
- the created workspace is owned by the human user,
- the external system cannot later mutate or delete that workspace unless it is
  granted a separate lifecycle role,
- Spritz returns the canonical access URL and lifecycle metadata at creation
  time.

The existing `spz` CLI should be the official machine client for this flow.

## Problem Statement

Spritz already supports authenticated browser users and a CLI/API surface for
creating workspaces. What is missing is a production-ready model for external
systems to create workspaces for someone else without turning those systems into
full administrators or hidden impersonators.

The system must satisfy all of these requirements:

- an external system can create a workspace for a real user,
- the user later accesses that workspace with their normal Spritz login,
- the external system does not need Kubernetes access,
- the external system does not construct access URLs on its own,
- the created workspace has both an idle lifetime and a hard maximum lifetime,
- all policy, audit, and ownership decisions stay centralized in Spritz,
- the design stays portable and backend-agnostic.

## Non-goals

- Letting external systems act as the user after the workspace is created.
- Giving bots direct Kubernetes or CRD write access.
- Making images the main external-facing abstraction.
- Duplicating provisioning logic in the CLI, UI, or bot.
- Adding environment-specific or organization-specific assumptions to Spritz
  core.

## Design Principles

### Spritz is the control plane

Spritz owns:

- provisioning,
- authentication and authorization,
- lifecycle enforcement,
- canonical access URLs,
- ownership,
- policy evaluation,
- audit logging.

External systems must not bypass Spritz and must not write CRDs directly.

### External systems are provisioners, not impersonators

An external system may request workspace creation for a user, but it must not:

- become that user,
- inherit that user's access rights,
- edit the workspace after creation,
- delete the workspace after creation,
- open terminal, SSH, or ACP sessions as that user.

### Presets are the public provisioning abstraction

External systems should request:

- `presetId`

not:

- raw image references,
- raw env sets,
- raw cluster-specific runtime details.

Presets are stable, policy-friendly, and portable. Images remain an internal
deployment concern.

### The CLI stays thin

`spz` should remain a thin client over the Spritz API.

It should not:

- evaluate authorization rules,
- construct URLs,
- generate lifecycle policy,
- implement retry deduplication rules on its own.

It should:

- collect inputs,
- call the API,
- print machine-readable results.

### One runtime path and one ownership model

The same ownership and lifecycle model should apply regardless of whether the
workspace was created:

- from the UI,
- from `spz`,
- from an external bot,
- from any future client.

## Actors and Roles

### Human principal

A human principal:

- authenticates through the normal browser identity flow,
- owns the created workspace,
- can later open, use, chat with, and delete their own workspace subject to
  normal policy.

### Service principal

A service principal is a non-human caller such as a bot or automation system.

It authenticates with bearer-style machine credentials and is evaluated against
explicit scopes and provisioner policy.

It is not a human session and it does not inherit ownership-based rights.

### Admin principal

An admin principal is a separate break-glass role with elevated capabilities.

The external provisioner flow must not depend on admin rights as the normal
path.

## Auth Model

Spritz should use two clean auth paths:

- browser users: gateway-managed browser auth,
- service principals: bearer token auth directly to the API.

This matches the existing portable auth model:

- browser requests go through the normal authenticated host,
- service-to-service clients can use bearer auth without depending on browser
  login flows.

### API auth mode

The preferred API mode for deployments that support both humans and service
principals is:

- `api.auth.mode=auto`

That allows:

- header-derived principals for browser traffic,
- bearer-derived principals for service traffic.

### Network path

For in-cluster automation, the preferred path is the internal API service, not
the browser-facing host:

```text
http://spritz-api.<namespace>.svc.cluster.local:8080/api
```

That avoids pushing service clients through browser auth gateways and public
edge routing.

If external machine clients are needed later, they should use a deliberately
designed machine-auth path, not the browser login surface.

## Permission Model

This is the most important part of the design.

### Core rule

The external system may create a workspace for a human owner, but it may not
act as that owner later.

That means Spritz should not use a broad permission such as "act as owner" or
"impersonate owner" for this workflow.

Instead, the external system should have narrow, action-specific permissions.

### Provisioner role

The external system should receive a dedicated service role such as:

- `spritz.provisioner`

That role should allow only the minimum actions required for create flows.

Recommended actions:

- `spritz.instances.create`
- `spritz.instances.assign_owner`
- `spritz.presets.read`
- `spritz.instances.suggest_name`

Not allowed by default:

- `spritz.instances.update`
- `spritz.instances.delete`
- `spritz.instances.open`
- `spritz.instances.terminal`
- `spritz.instances.ssh`
- `spritz.instances.acp_connect`
- `spritz.instances.list_all`
- `spritz.instances.get_arbitrary`

### Ownership is immutable

Once a workspace is created:

- `spec.owner.id` must be treated as immutable

except for an explicit admin-only break-glass path.

This prevents ownership hijacking and prevents a provisioner from creating a
workspace and reassigning it later.

### Create-for-owner is create-time only

The service principal may set the owner only during the create operation.

That right must not imply any later rights over the created object.

### Separate actor from owner

Every created workspace must record:

- owner: the human who owns and uses the workspace,
- actor: the service principal that requested creation,
- source: the external integration or channel,
- request id: the external idempotency/request identifier.

Recommended annotations:

- `spritz.sh/actor.id`
- `spritz.sh/source`
- `spritz.sh/request-id`

The actor must never replace the owner as the source of authorization for
normal use.

## Provisioner Policy Model

Permissions alone are not enough. The provisioner also needs policy
constraints.

Provisioner policy should define:

- allowed preset ids,
- allowed namespace(s),
- whether custom images are allowed,
- whether custom repos are allowed,
- maximum idle TTL,
- maximum hard TTL,
- maximum active workspaces per owner,
- maximum create rate per actor,
- maximum create rate per owner,
- optional repo allowlist or denylist,
- optional default preset if no preset is specified.

This keeps the external system constrained even if it is compromised or
misconfigured.

### Prefer presets over arbitrary images

The default provisioner policy should deny arbitrary images and require preset
selection.

That keeps runtime selection auditable and keeps environment-specific wiring
inside Spritz deployment overlays rather than inside the bot.

## API Model

### Treat create as a first-class provisioning method

The create surface should be treated as a provisioning API contract, not merely
as a thin CRD write endpoint.

The API should:

- validate caller type and policy,
- normalize preset defaults,
- generate names,
- apply lifecycle limits,
- write audit metadata,
- return canonical URLs and lifecycle metadata,
- enforce idempotency.

This can keep the existing `POST /api/spritzes` path if desired, but its
behavior should be explicitly treated as a provisioning contract rather than a
raw object mirror.

### Create request contract

For external provisioners, the preferred create request shape is:

- `ownerId`
- `presetId`
- `name` or none
- `namePrefix` or none
- `idleTTL`
- `ttl` (hard maximum lifetime)
- `repo` fields only if policy allows them
- `namespace` only if policy allows it
- `idempotencyKey`
- optional source metadata

The request should remain high-level and policy-friendly.

The provisioner should not need to send:

- raw image refs,
- raw secret refs,
- cluster-specific ingress settings,
- backend-specific env wiring.

### Create response contract

The create response should include everything the external system needs to hand
the result back to the user without a follow-up read.

Recommended response fields:

- workspace name,
- owner id,
- actor id,
- namespace,
- preset id,
- canonical access URL,
- optional chat URL,
- current phase/status,
- created at,
- idle TTL,
- max TTL,
- idle expiry timestamp,
- hard expiry timestamp,
- idempotency key,
- whether the response was newly created or replayed from idempotency state.

This avoids granting broad read permissions to the external system after
creation.

### Idempotency is required

External systems retry. Create must be idempotent.

The create API should require:

- `idempotencyKey`

The same actor submitting the same idempotency key should get the same
provisioning result rather than a second workspace.

Typical external ids include:

- chat interaction ids,
- request ids,
- message ids,
- workflow execution ids.

## `spz` CLI Model

`spz` should become the official external machine client for Spritz.

### Why reuse `spz`

`spz` already exists and already behaves like a thin HTTP client over the
Spritz API.

Reusing it avoids:

- a second CLI,
- duplicated auth behavior,
- duplicated request formatting,
- duplicated output conventions.

### Required `spz` behavior

`spz` should support service-principal usage cleanly:

- bearer token auth,
- machine-readable JSON output,
- no interactive prompts in automation mode,
- no local business rule duplication,
- stable exit codes.

### Recommended CLI shape

Examples:

```bash
spz create \
  --owner-provider discord \
  --owner-subject 123456789012345678 \
  --preset openclaw \
  --idle-ttl 24h \
  --ttl 168h \
  --idempotency-key req-abc \
  --json
```

```bash
spz suggest-name --preset openclaw --json
```

The CLI should also support:

- `--owner-id` for direct internal/admin creates when the caller already knows
  the canonical internal owner ID
- `--owner-provider` and `--owner-subject` for external platform identities
- `--api-url`
- `--token`
- `--namespace` when allowed by policy
- `--repo` and `--branch` only if the provisioner policy permits them

For chat-platform-triggered creates, external platform user IDs must not be
sent through `--owner-id`.

The CLI should not construct canonical URLs or infer authorization semantics on
its own.

## URL Model

Spritz must own the canonical access URL.

External systems must not build it locally from host assumptions.

The API should derive the URL from deployment configuration and return it in the
create response.

This keeps all clients consistent across:

- staging and production,
- host changes,
- route changes,
- gateway or auth changes.

The same model applies to:

- workspace open URLs,
- chat URLs,
- any future terminal or deep-link URLs.

## Lifecycle Model

Every externally provisioned workspace should support two lifetime controls.

### Idle TTL

Delete the workspace after a period of inactivity.

Example:

- `idleTTL = 24h`

### Hard maximum TTL

Delete the workspace after a maximum lifetime regardless of activity.

Example:

- `maxTTL = 168h` (`7d`)

The system should enforce both.

### Data model

The clean long-term model is:

- `spec.lifecycle.idleTTL`
- `spec.lifecycle.maxTTL`
- `status.lastActivityAt`
- `status.idleExpiresAt`
- `status.maxExpiresAt`
- `status.lifecyclePhase`
- `status.lifecycleReason`

The reaper/controller should evaluate:

- delete if `now - lastActivityAt > idleTTL`
- delete if `now - createdAt > maxTTL`

### Default policy

For external provisioners, defaults should be server-owned.

Recommended defaults:

- idle TTL default: `24h`
- hard maximum TTL default: `7d`

Provisioner policy may only tighten those values, not loosen them beyond the
configured maximums.

## Activity Model

Idle expiry only works if activity is defined centrally and consistently.

Spritz should update `lastActivityAt` when it observes real user activity such
as:

- ACP prompt submission,
- ACP conversation activity that represents user interaction,
- terminal input activity,
- SSH session activity,
- other explicit interactive control-plane actions.

Spritz should not treat these as activity:

- health checks,
- metadata refresh,
- ACP capability probes,
- page loads with no real user interaction,
- idempotency lookups.

This logic must live in one canonical owner, ideally the API/control plane, not
scattered across multiple clients.

## Naming Model

Names should remain backend-owned and deterministic.

If no explicit name is supplied:

- Spritz should generate a name,
- the name should be prefixed from the preset/image slug,
- the name should remain DNS-safe and unique.

Examples:

- `openclaw-tide-wind`
- `claude-code-quiet-harbor`

External systems may request a name suggestion, but they should not own the
allocation logic.

## Quotas and Abuse Controls

Thinking like a large production platform means quotas are mandatory.

Recommended controls:

- max active workspaces per owner,
- max creates per owner per time window,
- max creates per service principal per time window,
- optional org/team quotas,
- preset-specific quotas if needed later.

This prevents:

- duplicate retry storms,
- external bot abuse,
- user-specific resource explosions.

## Audit Model

Every create request should be auditable.

Audit records should include:

- actor principal id,
- actor principal type,
- owner id,
- preset id,
- namespace,
- idle TTL,
- hard TTL,
- source,
- idempotency key,
- result,
- created workspace name,
- canonical access URL,
- policy decisions that affected the request.

Expiry-driven deletion should also be auditable, including:

- reason: idle expiry or hard expiry,
- actor: system lifecycle controller,
- original owner,
- original actor if available.

## Service Principal Representation

Spritz should treat service principals as a first-class principal type, not just
as "non-admin bearer callers".

The long-term principal model should include:

- `type`: `human | service | admin`
- `subject`
- `issuer`
- `scopes`
- optional policy binding reference

This keeps authorization explicit and avoids hidden behavior based only on
caller id string matching.

## Deployment Model for External Systems

The preferred deployment path is:

- package `spz` into the external system image,
- inject credentials at runtime,
- call the internal Spritz API service,
- never bake credentials into the image.

Credentials should be injected via:

- secrets,
- workload identity,
- or another deployment-native credential mechanism.

The external system should not need:

- Kubernetes credentials,
- CRD write access,
- direct access to workspace pods,
- browser cookies,
- access through the browser login host.

## End-to-End Flow

The full target flow is:

1. A user asks an external system to create a workspace.
2. The external system resolves that user to a stable Spritz owner id.
3. The external system runs `spz create` with:
   - owner id,
   - preset id,
   - idle TTL,
   - hard TTL,
   - idempotency key.
4. `spz` calls the Spritz API with the service principal token.
5. Spritz validates:
   - the caller is a service principal,
   - the caller has provisioner permissions,
   - the caller may assign the requested owner at create time,
   - preset and lifecycle policy are allowed,
   - quota and rate-limit checks pass.
6. Spritz creates the workspace with:
   - human owner,
   - service actor audit metadata,
   - canonical lifecycle fields,
   - canonical name and URLs.
7. Spritz returns the creation response including the access URL.
8. The external system gives that URL back to the user.
9. The user visits the URL and logs in through the normal Spritz browser auth
   path.
10. From that point on, the user uses the workspace as its owner, and the
    external system has no lifecycle control over it.

## Validation Criteria

The design is correct only if all of the following are true:

- an external provisioner can create a workspace for a human user,
- the created workspace is owned by the human user,
- the provisioner cannot later edit or delete it,
- the provisioner does not need Kubernetes access,
- the provisioner does not construct access URLs locally,
- the same create request with the same idempotency key does not create
  duplicates,
- idle TTL and hard TTL are enforced centrally,
- activity updates are not triggered by probes or passive page loads,
- audit records clearly distinguish owner from actor,
- policy is evaluated over presets and lifecycle inputs, not raw image strings,
- browser auth and service auth remain separate and predictable.

## Implementation Direction

The clean implementation sequence is:

1. Treat `spz` as the official external machine client and add the missing
   service-principal capabilities there.
2. Add first-class service principal support and action-based authorization in
   the Spritz API.
3. Add provisioner policy configuration and enforce it at create time.
4. Make the create response return canonical URLs and lifecycle metadata.
5. Add required idempotency for external provisioners.
6. Add lifecycle fields, activity tracking, and a reaper/controller.
7. Add quota enforcement and structured audit logging.

## References

- `README.md`
- `docs/2026-02-24-portable-authentication-and-account-architecture.md`
- `docs/2026-02-24-simplest-spritz-deployment-spec.md`
- `docs/2026-03-10-acp-adapter-and-runtime-target-architecture.md`
- `cli/src/index.ts`
