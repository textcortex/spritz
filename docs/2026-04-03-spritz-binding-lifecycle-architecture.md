---
date: 2026-04-03
author: Onur Solmaz <onur@textcortex.com>
title: Spritz Binding Lifecycle Architecture
tags: [spritz, binding, lifecycle, control-plane, rollout, architecture]
---

## Overview

Goal in plain English:

- make Spritz the single owner of long-lived instance lifecycle
- keep `Spritz` instances disposable
- keep shared concierge as just another instance behind the same lifecycle model
- make replacement, cutover, and recovery resumable without external repair

The current split ownership is fragile:

- one system decides rollout policy
- another system owns replace idempotency
- Kubernetes owns the actual runtime objects
- retries reconstruct state from partial side effects

This document proposes one production-ready control-plane model for Spritz:

- one durable logical resource
- one controller that owns lifecycle end to end
- many disposable `Spritz` instances behind that logical resource

That is the control-plane shape Spritz should converge toward.

## Problem

Today the lifecycle of a long-lived logical agent may span multiple stores and
multiple controllers:

- external installation or business state
- external rollout tables
- Spritz replace reservations
- live `Spritz` resources
- routing and cutover state outside Spritz

That split creates several bad failure modes:

- replace can half-succeed and become hard to resume
- retries can collide with stale idempotency state
- a replacement candidate can exist without a durable operation record
- cutover can happen in one system while cleanup is still owned by another
- operator visibility is fragmented across SQL rows, ConfigMaps, and runtime
  objects

The root issue is that the system does not yet model the durable logical thing
separately from the disposable runtime realization.

## Design Principles

### 1. One stable logical object

The durable thing is the logical agent binding, not any concrete runtime name.

### 2. Disposable runtime objects

`Spritz` should remain the concrete workload object:

- one image
- one revision
- one ACP endpoint
- one lifetime

A long-lived logical agent may move across many `Spritz` instances over time.

### 3. One lifecycle owner

Spritz itself should own:

- candidate creation
- readiness gating
- cutover
- old-instance cleanup
- retry and resume semantics

External systems should request desired state, not script lifecycle steps.

### 4. Durable reconcile state

Resume should come from one durable operation state owned by Spritz, not from:

- ad hoc ConfigMap reservations
- backend-specific rollout rows
- inference from whichever runtime currently exists

### 5. Shared concierge is not special

A shared concierge should remain just another bound instance. It should not
require a concierge-specific lifecycle engine.

## Core Model

### `SpritzBinding`

Spritz should add a new CRD named `SpritzBinding`.

`SpritzBinding` is the durable logical resource for any long-lived identity
that must survive replacement of its runtime.

Examples:

- one shared workspace concierge
- one durable personal agent binding
- one durable tool-hosted assistant

Examples that do not need `SpritzBinding` by default:

- one-off ephemeral development environments
- short-lived throwaway instances created directly by users

### `Spritz`

`Spritz` remains the concrete runtime object.

It should not become the durable lifecycle record. It should stay easy to:

- create
- probe
- replace
- delete

### Optional `SpritzOperation`

`SpritzBinding.status` should be the source of truth for current lifecycle
state.

If Spritz later needs durable history or audit, it may add an optional
append-only `SpritzOperation` CRD. That resource should be used for history and
debugging only, not for correctness.

## Why a CRD Is the Right Store

`SpritzBinding` is control-plane state, so a CRD is an appropriate store.

This is a good fit because the data is:

- low-volume
- lifecycle-oriented
- naturally expressed as desired state plus observed state
- reconciled by a controller
- useful to inspect with standard Kubernetes tools

This is not a proposal to use CRDs as a general application database.

The boundary should stay clear:

- CRD: current control-plane state for lifecycle ownership
- external database: business records, analytics, long-term history, tenant
  metadata, or product-specific UI data

## `SpritzBinding` Shape

Suggested shape:

```yaml
apiVersion: spritz.sh/v1
kind: SpritzBinding
metadata:
  name: workspace-tenant-123
spec:
  template:
    presetId: concierge
    owner:
      id: user-123
    principal:
      id: provider-app
      type: service
    request:
      source: provider-install
      spec: {}
  desiredRevision: sha256:abcd
  rolloutStrategy:
    type: create-before-destroy
status:
  observedRevision: sha256:1234
  phase: updating
  activeInstanceRef:
    namespace: agents
    name: concierge-spring-river
    revision: sha256:1234
  candidateInstanceRef:
    namespace: agents
    name: concierge-bright-hill
    revision: sha256:abcd
  conditions:
    - type: Ready
      status: "False"
      reason: CandidateNotReady
    - type: Progressing
      status: "True"
      reason: WaitingCandidateReady
```

Key rules:

- `spec` is the desired logical binding
- `status` is the current observed lifecycle state
- exactly one active instance may be live at a time
- at most one candidate instance may be in progress at a time

## Reconciliation Contract

The `SpritzBinding` controller should own the full lifecycle.

### Initial creation

1. observe a binding with no active instance
2. create a `Spritz` instance from `spec.template`
3. wait until the runtime satisfies the base ready contract
4. publish it as `activeInstanceRef`
5. mark the binding `Ready`

### Revision rollout

When `spec.desiredRevision` differs from `status.observedRevision`:

1. create a candidate `Spritz` instance for the desired revision
2. persist that candidate reference in `status`
3. wait for candidate readiness
4. cut over the binding to the candidate
5. delete the old active runtime
6. set `observedRevision = desiredRevision`
7. clear candidate state
8. mark the binding `Ready`

### Recovery

If the active runtime disappears unexpectedly:

1. keep the binding as the durable owner
2. create a new runtime for the currently desired revision
3. republish the new active reference
4. do not require external repair to recover

## Explicit Lifecycle Phases

The controller should expose explicit phases in `status.phase`, for example:

- `pending`
- `creating`
- `waiting_ready`
- `cutting_over`
- `cleaning_up`
- `ready`
- `degraded`
- `failed`

The controller should also publish conditions such as:

- `Ready`
- `Progressing`
- `CandidateReady`
- `CutoverReady`
- `CleanupBlocked`
- `Failed`

The phase machine must be resumable. A controller restart must not require
reconstructing state from side effects.

## Readiness Contract

The controller should gate cutover only on the base runtime readiness contract.

That contract should mean:

- the `Spritz` object exists
- the runtime reached the normal usable phase
- ACP, if required by the preset, is ready
- the live endpoint or binding is routable

Secondary work should not silently extend the create path forever.

Examples of secondary work:

- profile synchronization
- shared mount hydration
- convenience bootstrap jobs

Those should reconcile independently and report their own status. They should
not make replacement unrecoverable.

## Routing and Live Resolution

External systems should resolve the logical binding, not the raw runtime name.

That means:

- stable routing should target `SpritzBinding`
- `SpritzBinding.status.activeInstanceRef` is the authoritative live runtime
- callers should not treat a previous runtime name as durable truth

This avoids the current drift where different systems hold different ideas of
which instance is live.

## Idempotency and Retry Semantics

The current replace primitive should not remain the primary source of lifecycle
truth.

Instead:

- the durable operation state lives on `SpritzBinding.status`
- retries reconcile against current `status`
- candidate names and operation phases are resumed, not rediscovered

Idempotency should mean:

- replay returns current operation state
- replay never requires manual reservation cleanup
- replay never poisons future progress when no valid replacement was created

The existing `:replace` API may remain as a compatibility surface, but it
should become a thin shim that drives binding-owned lifecycle rather than
storing its own independent truth.

## Replace API Compatibility

Spritz should keep the current internal replace primitive during migration:

```text
POST /api/internal/v1/spritzes/{namespace}/{instanceId}:replace
```

But its implementation should change:

- resolve the owning `SpritzBinding`
- update desired revision and operation intent on the binding
- return the binding's current source and candidate state

That keeps compatibility for callers while moving ownership into the Spritz
control plane.

## Naming and Candidate Selection

Candidate runtime naming should be deterministic enough to resume safely.

Recommended approach:

- binding controller allocates one candidate name per operation attempt
- that name is persisted in binding status immediately
- the controller never "forgets" the candidate it already chose

This avoids the current pattern where retries rediscover names via external
idempotency records.

## Failure Handling

### Candidate creation fails

- keep the active instance serving
- mark `Progressing=True`
- set a typed failure reason in status
- retry reconcile without losing the candidate decision boundary

### Candidate becomes terminal before cutover

- leave active instance unchanged
- clear only the invalid candidate state
- create a new candidate on the next reconcile

### Cutover succeeds but old cleanup fails

- keep the binding pointed at the new active instance
- mark cleanup as pending or blocked
- retry old-instance deletion independently

### Controller restarts mid-operation

- resume from `SpritzBinding.status`
- do not depend on external rollout tables
- do not require manual state repair for normal crash recovery

## Migration Plan

### Phase 1: introduce binding CRD and controller

- add `SpritzBinding`
- support create and reconcile for a binding-owned runtime
- keep direct `Spritz` creation unchanged for ephemeral use cases

### Phase 2: make shared channel concierge use bindings

- the external installation record stores binding identity, not runtime name
- shared concierge lifecycle resolves through `SpritzBinding`
- replacement and recovery become controller-owned

### Phase 3: route compatibility APIs through binding state

- keep `:replace` as a compatibility shim
- stop treating replace reservations as the durable lifecycle record

### Phase 4: remove split ownership

- external rollout systems stop owning candidate creation, cutover, and cleanup
- backend or deployment systems request desired revision only

## Why This Is the Elegant End State

This model keeps the boundaries clean:

- `SpritzBinding` is the stable logical thing
- `Spritz` is the disposable runtime thing
- the controller is the lifecycle owner
- external systems ask for desired state and observe status

It also keeps shared concierge generic:

- not a special top-level resource
- not a custom lifecycle engine
- just another binding whose runtime happens to use a concierge preset

That is the production-ready control-plane shape Spritz should aim for.

## Validation

The architecture is successful when all of the following are true:

1. a binding can roll from revision A to revision B without external orchestration
2. a controller restart during rollout resumes cleanly from binding status
3. cutover never happens before the candidate satisfies the base ready contract
4. failure in shared-mount or profile sync does not poison replacement identity
5. a shared concierge is recoverable without manual cleanup of reservation state
6. external systems no longer need to persist active runtime identity as
   authoritative truth

## Related Docs

- `docs/2026-03-31-instance-replacement-rollout-architecture.md`
- `docs/2026-03-31-shared-channel-concierge-lifecycle-architecture.md`

This document supersedes the split-ownership direction from the earlier
instance replacement and concierge rollout design. The generic replace
primitive may remain as a compatibility API, but long-lived lifecycle should be
owned by `SpritzBinding`.
