---
date: 2026-04-17
author: Onur Solmaz <onur@textcortex.com>
title: Channel Install Target Selection Architecture
tags: [spritz, channel-gateway, install, targets, architecture, ui]
---

## Overview

This document defines a provider-agnostic way for a shared channel install flow
to ask the installer which deployment-owned target should back that workspace.

The immediate use case is a Slack install where the user must choose which
assistant to connect to the workspace. The design is intentionally generic so a
deployment can expose any eligible target type, such as:

- personal assistants
- team-owned assistants that the installer administers
- catalog entries
- workflow templates

Spritz should own the picker UX and the generic API contract. The deployment
should own which targets are eligible, what the selected target means, and how
that selection resolves into the final concierge binding.

Related docs:

- [Shared Channel Concierge Lifecycle Architecture](2026-03-31-shared-channel-concierge-lifecycle-architecture.md)
- [Channel Install Result Surface](2026-04-02-channel-install-result-surface.md)
- [Agent Profile API](2026-03-30-agent-profile-api.md)

## Plain Language

During install, Spritz should ask, "Which thing do you want this workspace to
use?"

Spritz should not know what that thing really is. It should ask the deployment
for the list, render the options, save the chosen opaque payload, and send that
payload back later when concierge creation or recovery needs it.

## Problem

Shared channel installs currently assume the deployment can finalize an
installation without an extra user choice.

That breaks down when one installer may have access to more than one valid
target. In those cases, Spritz needs one explicit selection step, but it must
not grow deployment-specific concepts such as:

- "agent"
- "organization admin"
- deployment-specific ownership rules

If Spritz bakes those concepts into core install APIs, the shared channel flow
stops being reusable.

## Goals

- Add a generic install-time target picker to shared channel install flows.
- Keep Spritz provider-agnostic and deployment-agnostic.
- Reuse existing Spritz concepts where possible instead of inventing a new
  domain model.
- Persist the chosen target as part of the durable logical installation, not as
  transient browser state.
- Ensure runtime creation, replacement, and recovery can reapply the same
  selection without asking the user again.

## Non-Goals

- Defining deployment-specific authorization rules for which targets are
  visible.
- Making Spritz understand deployment-specific target types.
- Moving deployment-owned installation storage into Spritz core.
- Replacing the existing per-instance `agentRef` and profile sync model.

## Core Decision

Spritz should model this as installation-time target selection.

That means:

- Spritz owns the browser flow and generic picker contract.
- The deployment owns the list of eligible targets.
- The deployment also owns the meaning of the chosen target.
- The chosen target is stored as opaque `presetInputs`, not as a Spritz-level
  `agentId` or other deployment-specific foreign key.

This is the same boundary used elsewhere in Spritz:

- Spritz understands stable generic fields.
- Deployments own business semantics behind those fields.

## Pinned V1 Decisions

The following decisions are intentionally locked for the first production
implementation.

### Contract

- Spritz defines one generic list operation:
  - `channel.install.targets.list`
- Spritz does not define a target taxonomy.
- Spritz does not add a core `agentId`, `targetType`, or deployment-specific
  enum to this contract.
- The install option shape is minimal and stable:
  - `id`
  - `profile.name`
  - optional `profile.imageUrl`
  - optional `ownerLabel`
  - `presetInputs`
- Spritz treats `presetInputs` as fully opaque and only round-trips it.

### UX

- If the deployment returns zero targets, install fails with a typed product
  error.
- If the deployment returns one target, Spritz auto-selects it and continues.
- If the deployment returns two or more targets, Spritz shows a picker.
- The picker remains intentionally simple in v1:
  - avatar
  - name
  - owner label
- Spritz does not add search, filtering, tabs, or target categories in v1.

### Persistence and binding

- Spritz does not persist the chosen target in Spritz core state.
- The deployment persists the chosen `presetInputs` on its own durable
  installation record.
- The same saved `presetInputs` must be reused for:
  - first provisioning
  - reprovisioning
  - recovery
  - repair

### Validation

- The deployment must validate the chosen `presetInputs` when install is
  finalized.
- The deployment must validate again when replaying the saved selection during
  recovery or reprovisioning.
- Spritz must never trust browser-submitted selection data on its own.

### Errors

- `install_targets_unavailable`
- `install_targets_empty`
- `install_target_invalid`
- `install_target_forbidden`
- `install_target_conflict`

These codes are generic enough for Spritz while still mapping cleanly onto
deployment-owned policy and validation failures.

## Why `presetInputs` Is The Right Payload

Spritz already has a generic way to pass deployment-owned create-time selector
data into preset resolution: `presetInputs`.

That is the preferred payload for install target selection as well.

Reasons:

- it is already part of the create-admission model
- Spritz does not need to understand the inner fields
- deployments can map it to whatever target type they use
- the same payload can be reused during initial create and later recovery

The chosen opaque `presetInputs` should therefore be persisted on the durable
installation object that the deployment already owns, not in Spritz core state.

## Why Existing `agentRef` Is Not Enough

The existing [Agent Profile API](2026-03-30-agent-profile-api.md) is for a
bound instance that already points at one external identity.

That model is useful here only for presentation patterns:

- `name`
- `imageUrl`

It is not the right install contract by itself because install-time choice is a
list-selection problem, not a per-instance bound-identity problem.

In other words:

- `agentRef` is for "this running instance points at that external thing"
- install target selection is for "show the user eligible options, then save
  the chosen opaque selector"

## UX Flow

Recommended browser flow:

1. The user starts a shared channel install such as Slack.
2. Provider OAuth completes and returns to Spritz.
3. Spritz resolves the installer identity and install context.
4. Spritz calls a deployment-owned target-listing contract.
5. Spritz renders the returned options in a picker.
6. The user chooses one option.
7. Spritz submits the chosen option's `presetInputs` during install
   finalization.
8. The deployment validates the choice again, persists it on the installation
   record, and provisions or reuses the logical concierge binding.

Rules:

- Spritz must not trust a client-supplied choice without deployment-side
  revalidation.
- If zero targets are returned, Spritz must stop the install with a typed
  product error.
- If exactly one target is returned, Spritz should auto-select it.
- If more than one target is returned, Spritz should show the picker.
- The picker should render only the minimal display fields needed for a clear
  choice:
  - avatar
  - name
  - owner label
- If the install later recreates or repairs the runtime, the saved
  `presetInputs` must be reused.
- Existing installs without saved `presetInputs` may keep their current
  deployment-defined fallback behavior until reconfigured.

## Generic Contract

### 1. List install targets

Spritz should define one generic deployment contract for listing install
targets. One possible operation name is:

- `channel.install.targets.list`

The exact transport can follow the existing deployment adapter pattern, but the
contract should be stable and generic.

The request should include only install facts Spritz already knows, for
example:

```json
{
  "version": "v1",
  "type": "resolver",
  "operation": "channel.install.targets.list",
  "requestId": "req_123",
  "context": {
    "principalId": "shared-channel-gateway",
    "provider": "slack",
    "externalScopeType": "workspace",
    "externalTenantId": "T123456",
    "presetId": "channel-concierge"
  },
  "input": {
    "installer": {
      "type": "external",
      "provider": "slack",
      "subject": "U123456"
    }
  }
}
```

The deployment response should return display-ready options plus opaque
`presetInputs`:

```json
{
  "status": "resolved",
  "output": {
    "targets": [
      {
        "id": "personal-assistant",
        "profile": {
          "name": "Research Assistant",
          "imageUrl": "https://console.example.com/assets/research-assistant.png"
        },
        "ownerLabel": "Personal",
        "presetInputs": {
          "targetId": "assistant-123"
        }
      },
      {
        "id": "team-support",
        "profile": {
          "name": "Support Concierge",
          "imageUrl": "https://console.example.com/assets/support-concierge.png"
        },
        "ownerLabel": "Example Team",
        "presetInputs": {
          "targetId": "assistant-456"
        }
      }
    ]
  }
}
```

Contract rules:

- `targets[].id` is a stable client key only
- `targets[].profile` is for display only
- `targets[].profile.name` is required
- `targets[].profile.imageUrl` is optional
- `targets[].ownerLabel` is optional
- `targets[].presetInputs` is opaque to Spritz
- Spritz must round-trip `presetInputs` unchanged
- the deployment may return zero, one, or many targets
- Spritz must not infer target type or business meaning from any returned field

### 2. Finalize install with chosen target

When the user submits the picker, Spritz should send the chosen `presetInputs`
through the normal install finalization path.

Spritz should not extract or reinterpret inner fields.

The deployment must:

- validate that the selection is still allowed
- reject tampered or stale selections
- persist the validated `presetInputs` on the durable installation record it
  already owns

### 3. Use the saved target during provisioning and recovery

Whenever the logical installation provisions, reprovisions, or repairs its live
runtime, the deployment should feed the saved `presetInputs` back into normal
preset resolution.

That keeps install-time selection and runtime binding on the same path.

## Scope Split

### Spritz owns

- the install picker step
- the generic target-listing contract
- the browser-facing rendering of options
- collection and round-tripping of the chosen `presetInputs`
- typed install-result handling when target listing or target submission fails

### Deployment-owned integration owns

- resolving the installer's identity
- deciding which targets are eligible
- deciding whether eligibility depends on personal ownership, team admin
  rights, or any other policy
- validating the selected target
- persisting the chosen `presetInputs`
- mapping that selection to the real concierge binding

## Error Handling

The picker flow should follow the same normalized install-result model as the
rest of channel install.

Recommended cases:

- `install_targets_unavailable`
- `install_targets_empty`
- `install_target_invalid`
- `install_target_forbidden`
- `install_target_conflict`

Spritz should render those as product outcomes on the install result surface,
not as raw upstream failures.

## Validation

Minimum validation for this model:

- a user with one eligible target can finish install successfully
- a user with multiple eligible targets sees a picker and the chosen target is
  the one that is bound
- a tampered `presetInputs` submission is rejected by the deployment
- runtime replacement reuses the saved `presetInputs`
- installs without saved `presetInputs` preserve existing fallback behavior
  until explicitly reconfigured
- the UI renders from the returned display fields without needing deployment
  knowledge in the browser

## Follow-Ups

- define the exact Spritz route and UI component for the picker step
- wire the shared channel gateway callback to the target-listing contract
- extend deployment-owned installation finalizers to persist `presetInputs`
- reuse the same pattern for future shared channel providers beyond Slack
