---
date: 2026-04-17
author: Onur Solmaz <onur@textcortex.com>
title: Channel Install Ownership and Management Architecture
tags: [spritz, channel-gateway, install, ownership, management, architecture]
---

## Overview

This document defines who owns a shared channel installation after install
target selection, who can manage that installation later, and how reinstall,
target changes, and disconnects should behave.

It builds on the install-target selection model and keeps Spritz generic:

- Spritz owns the browser UX and generic install-management surfaces
- the deployment owns target eligibility, target semantics, and authorization
- the durable installation remains keyed by route identity, not by browser
  session or installer identity alone

Related docs:

- [Channel Install Target Selection Architecture](2026-04-17-channel-install-target-selection-architecture.md)
- [Shared Channel Concierge Lifecycle Architecture](2026-03-31-shared-channel-concierge-lifecycle-architecture.md)
- [Channel Install Result Surface](2026-04-02-channel-install-result-surface.md)
- [Shared App Tenant Routing Architecture](2026-03-23-shared-app-tenant-routing-architecture.md)

## Problem

Install target selection answers which target should back a workspace, but it
does not by itself answer who owns the resulting installation.

Those are not always the same thing:

- the installer may be an individual actor
- the chosen target may be owned by that individual
- or the chosen target may be owned by a group that the installer manages

If the installer is treated as the canonical owner even when the chosen target
belongs to a group, the model becomes awkward:

- the selected target and installation ownership no longer line up
- later management rights become unclear
- reinstall and recovery paths risk silently re-binding to the wrong owner

## Core Decision

The effective owner of a shared channel installation must be derived from the
selected target, not from the installer identity.

That means:

- an individually owned target creates or updates an individually owned
  installation
- a group-owned target creates or updates a group-owned installation
- the installer identity remains audit metadata, not the canonical owner

Management rights must then follow the effective owner of the installation.

## Pinned Decisions

### Route identity stays separate from ownership

The durable installation identity remains:

- `principalId`
- `provider`
- `externalScopeType`
- `externalTenantId`

Ownership is attached to that installation record, but does not replace the
route key.

This keeps the model ready for multiple shared apps in the future:

- one app plus one workspace is one installation
- a second app for the same workspace is a different installation because it
  has a different `principalId`

Spritz and deployments must therefore avoid any global "one workspace only"
assumption.

### Effective owner is target-derived

The deployment resolves the selected target into both:

- the opaque `presetInputs` that should be saved on the installation
- the effective owner principal for that installation

Two generic cases matter in v1:

- individually owned target -> individually owned installation
- group-owned target -> group-owned installation

Spritz does not need a built-in taxonomy for owner types beyond what the
deployment already uses for authorization and display.

### Installer identity is audit-only

The actor who completed provider OAuth is still useful, but only as audit
metadata:

- who installed or reinstalled the app
- who completed a reconnect
- who last changed the target

The installer should not override the effective installation owner.

### Management rights follow the effective owner

The caller may manage an installation only if the deployment says the caller is
authorized for that installation's effective owner.

Spritz should not attempt to infer that policy locally.

For v1, Spritz should treat install management as an installation-centric UI
over deployment-owned authorization:

- list manageable installations
- show the current selected target summary
- allow `change target`
- allow `reconnect`
- allow `disconnect`

The deployment remains the source of truth for:

- which installations the caller may see
- which actions are currently allowed
- whether the selected replacement target is valid

### Reinstall updates in place only for the same effective owner

Provider-driven reinstall should reuse the same `(principalId, provider,
externalScopeType, externalTenantId)` installation when possible.

Reinstall may update provider-side auth and other mutable metadata in place,
but it must not silently change ownership.

Pinned rule:

- if the reinstall resolves to the same effective owner, update in place
- if it resolves to a different effective owner, return conflict

This prevents silent workspace takeover during normal provider reinstall flows.

### Explicit target change may change ownership

A deliberate management action to change the selected target may also change
the effective owner.

That is allowed, but only as an explicit management operation, not as an
incidental side effect of reinstall.

Pinned rule:

- the caller must be authorized to manage the current installation
- the deployment must validate the newly selected target
- the target change and owner change must commit atomically

Examples:

- individual-owned install -> group-owned target => installation becomes
  group-owned
- group-owned install -> individually owned target => installation becomes
  individually owned

### Invalid or inaccessible target fails closed

If the saved target is deleted, becomes inaccessible, or otherwise no longer
resolves cleanly, Spritz must not silently fall back to another target.

Pinned rule:

- route resolution fails closed until the installation is repaired
- the install-management surface must show that operator action is required
- an authorized manager must select a replacement target explicitly

Spritz should not auto-retarget and should not invent fallback target
selection behavior.

### Disconnect and uninstall are soft

Provider uninstall or product-side disconnect should soft-disconnect the
installation, not hard-delete it immediately.

Pinned rule:

- routing stops immediately
- provider auth may be cleared according to deployment policy
- the durable installation record remains
- the logical concierge binding may remain for later reuse

This keeps reconnect flows simple and consistent with the existing shared
channel lifecycle model.

## UX Consequences For Spritz

Spritz should present connected workspaces as installation records, not as one
global account-link concept.

For each manageable installation, Spritz should show at least:

- provider/workspace identity
- current state
- current selected target summary
- available actions

The minimum action set is:

- change target
- reconnect
- disconnect

Spritz does not need to expose deployment-specific ownership rules in the UI.
It only needs to render the installations and actions that the deployment says
the caller may manage.

## Contract Consequences

This model implies a few stable contract expectations, even if exact endpoint
shapes vary by deployment:

- target resolution must return both target selection data and effective owner
- installation persistence must store the saved selection on the durable
  installation
- install-management APIs must authorize against the effective owner, not just
  the original installer
- reinstall APIs must detect effective-owner mismatch and return conflict
- management-target-change APIs must update target and owner together

These behaviors matter more than the exact transport details.

## Validation

At minimum, an implementation should validate:

- reinstall of the same route and same effective owner updates in place
- reinstall of the same route and different effective owner returns conflict
- changing to a new valid target updates the installation atomically
- changing to a target owned by a different principal updates effective owner
- deleting or invalidating the saved target blocks routing until repair
- disconnect stops routing but preserves the installation for reconnect
- the same external tenant can still have multiple installations later when
  `principalId` differs

## Follow-Ups

The next design work should define the generic management surfaces that Spritz
needs for:

- listing manageable installations
- updating the selected target on an installation
- reconnecting a disconnected installation
- surfacing repair-needed state when the saved target is no longer valid
