---
date: 2026-04-25
author: Spritz Team
title: Binding Target Reconciliation
tags: [spritz, binding, reconciliation, channel-gateway, architecture]
---

## Overview

Spritz needs one clear readiness rule for long-lived channel bindings:

> The desired target saved by the deployment must match the target rendered into
> the live `SpritzBinding`.

If those two targets differ, the binding is not ready.
It should be repaired automatically and reported as provisioning until the live
runtime catches up.

This document describes the generic Spritz-side contract.
It does not depend on any specific deployment, provider, model, or agent system.

Related docs:

- [Spritz Binding Lifecycle Architecture](2026-04-03-spritz-binding-lifecycle-architecture.md)
- [Channel Install Target Selection Architecture](2026-04-17-channel-install-target-selection-architecture.md)
- [Channel Installation Data Model](2026-04-24-channel-installation-data-model.md)

## Problem

A shared channel installation has two layers of state:

- desired business state, usually stored by the deployment
- live runtime state, stored in Spritz and Kubernetes

For target-selected installs, the desired business state includes an opaque
target selector such as `presetInputs`.

The live runtime state includes the rendered `SpritzBinding` template and the
currently active `Spritz` instance.

If these layers disagree, Spritz can route traffic to the wrong runtime while a
management API or UI shows the desired target.

That is a correctness bug.

## Required Invariant

For any binding backed by a saved target selection:

```text
saved target selector == rendered binding target
```

In v1, the target selector is deployment-owned and opaque to Spritz.

Spritz should not interpret the selector as an agent, organization, workspace,
or provider-specific object.

Spritz only needs a deployment-provided way to compare:

```text
expected target identity from saved desired state
actual target identity from rendered SpritzBinding
```

If they differ, the binding must not be treated as ready.

## Source of Truth

The deployment-owned installation record remains the source of truth for:

- provider installation identity
- target selection payload
- owner or tenant business metadata
- provider auth metadata

The `SpritzBinding` is derived control-plane state.

Spritz should not invent a new target from Kubernetes state.
Kubernetes state can prove what is running, but it should not decide what should
be running.

## Simple Production Fix

No schema change is required for the first production fix.

The route or status resolver should check target consistency before returning
ready.

For each active installation:

1. Load the saved target selection from the deployment installation record.
2. Load the live `SpritzBinding` by binding key.
3. Extract the rendered target identity from the binding template.
4. Compare the saved target identity with the rendered target identity.
5. If they differ, trigger the existing binding upsert/reconcile path.
6. Return provisioning until the corrected binding becomes ready.

This turns stale runtime state into self-healing state.

## Where the Check Belongs

The check should live in the shared path that decides whether a channel binding
is usable.

It should not be only a management UI check.

It should run for:

- channel route resolution
- channel session exchange
- workspace or installation status APIs
- any force-refresh or repair path

That makes the next real lookup repair drift automatically.

## Repair Behavior

When a mismatch is found:

```text
saved desired target = A
live binding target = B
```

the resolver should:

1. log a target mismatch event with the binding key and both target identities
2. emit a metric for alerting
3. call the normal binding upsert path using the saved desired state
4. return provisioning

It should not patch the live Kubernetes object directly.

Direct Kubernetes patching is only an emergency operator action.
Normal repair must use the same desired-state path as initial provisioning.

## Readiness Rule

A binding can be reported ready only when all of these are true:

```text
saved target == rendered binding target
desired binding revision == active runtime revision, or the equivalent observed revision
SpritzBinding phase == ready
active runtime phase == Ready
```

If the target check fails, readiness is false even if the active runtime is
healthy.

A healthy wrong runtime is still wrong.

## Cached Status Promotion

A deployment may keep its own installation status, such as `provisioning`,
`ready`, or `disconnected`.

That status is a deployment cache. It is not stronger than the live
`SpritzBinding` state.

When a deployment status still says `provisioning`, the resolver should refresh
the binding before rejecting channel traffic or runtime identity exchange. If
the binding matches the saved target and the active runtime has reached the
desired binding revision, the deployment should promote its cached installation
status to `ready` and record the applied revision.

This avoids a permanent stuck state where:

```text
deployment status = provisioning
SpritzBinding phase = ready
active runtime phase = Ready
active runtime revision = desired binding revision
```

In that state, the deployment cache is stale.
The correct repair is to update the deployment cache from the binding, not to
manually edit the database and not to leave the route closed forever.

If the binding is mismatched, stale, disconnected, missing, or still converging,
the resolver must keep reporting provisioning.

## API Need

The binding status/read API should expose enough information for a deployment to
check the rendered target.

At minimum, the response should include one of:

- the rendered `spec.template`
- a normalized rendered target identity
- a deployment extension response that includes the resolved target identity

The safest generic option is to expose the rendered binding template in the
internal binding read response.

Deployments can then compare their saved selector with the part of the rendered
template they own.

## Pseudocode

```python
def resolve_channel_binding(installation):
    expected = deployment.extract_expected_target(installation.saved_target)
    binding = spritz.get_binding(installation.binding_key)
    actual = deployment.extract_rendered_target(binding.template)

    if expected != actual:
        log_target_mismatch(
            binding_key=installation.binding_key,
            expected=expected,
            actual=actual,
        )
        binding = spritz.upsert_binding(
            binding_key=installation.binding_key,
            desired_state=installation.to_binding_desired_state(),
        )
        return provisioning(binding)

    if (
        binding.ready
        and binding.active_runtime_ready
        and binding.active_revision == binding.desired_revision
    ):
        deployment.record_ready_binding(
            installation.id,
            runtime=binding.active_runtime,
            applied_revision=binding.active_revision,
        )
        return ready(binding)

    return provisioning(binding)
```

The deployment owns `extract_expected_target` and `extract_rendered_target`.
Spritz owns the idempotent binding reconcile.

## Versioning

Do not add desired and observed generation fields for the first fix.

The mismatch check is enough to prevent the known failure mode:

```text
saved target changed
live binding did not change
system still reported ready
```

Generation fields can be added later if Spritz needs stronger ordering,
high-frequency target changes, or detailed audit history.

The first production fix should stay small:

```text
compare
repair
return provisioning until ready
```

## Tests

Add tests for:

1. saved target and rendered target match, binding ready: returns ready
2. saved target and rendered target differ: calls binding upsert and returns provisioning
3. saved target and rendered target differ, upsert fails: returns unavailable or failed with a typed error
4. rendered target is missing: treats as mismatch and repairs
5. active runtime is healthy but target differs: does not return ready
6. management status path shows provisioning while repair is in progress
7. route/session path triggers the same repair as the status path
8. cached deployment status is provisioning but binding is ready at the desired revision: promotes to ready
9. cached deployment status is provisioning but binding is stale or mismatched: stays provisioning

## Non-Goals

This document does not require:

- moving deployment business records into Spritz
- teaching Spritz deployment-specific target semantics
- adding new SQL columns
- adding a full audit log
- making Kubernetes the source of truth for desired target selection

## Final Rule

The simple long-term rule is:

```text
desired target comes from the deployment record
live target comes from SpritzBinding
ready requires both to match
```

That rule keeps Spritz generic and prevents stale runtimes from silently serving
traffic after a target change.
