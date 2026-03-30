---
date: 2026-03-30
author: Onur Solmaz <onur@textcortex.com>
title: Preset Catalog Source of Truth
tags: [presets, api, ui, architecture]
---

# Preset Catalog Source of Truth

## TL;DR

- Spritz should have one preset catalog.
- The Spritz API should own that catalog.
- The UI should render launchable presets from `GET /api/presets`.
- Presets that should not appear in the human UI should stay in the API catalog and be marked as hidden from human launch surfaces.
- Deployments should stop maintaining a separate long-lived `ui.presets` list.

## Problem

Spritz currently allows deployments to define presets in two places:

- `api.presets`
- `ui.presets`

That split creates avoidable drift:

- the UI can show a preset that the API does not actually own,
- the UI can launch by raw `spec.image` instead of `presetId`,
- preset-backed env, labels, name prefixes, and instance-class behavior can be skipped,
- rollout bugs become harder to diagnose because the screen and the control plane disagree.

This is a control-plane problem, not a branding problem. Presets carry launch-time behavior, so they should not be duplicated in a display-only config surface.

## Decision

The API preset catalog is the single source of truth for launchable presets.

That means:

- deployments define runtime presets in `api.presets`,
- the API publishes safe public preset metadata through `GET /api/presets`,
- the UI reads that API response and renders the preset picker from it,
- creates that start from a preset must send `presetId`,
- preset-backed env remains server-side and is not copied into UI config.

## Why The API Owns Presets

The API already owns the behaviors that presets control:

- preset ID normalization
- preset-backed env injection
- name-prefix defaults
- instance-class attachment
- allowlist and service-principal policy
- create-time preset resolution

The UI should choose from that catalog, not redefine it.

The existing `GET /api/presets` endpoint is already the right public contract:

- it returns preset IDs and launch metadata,
- it omits env values,
- it already applies service-principal filtering where needed.

## Visibility Model

Some presets should exist without appearing in the human launch picker.

Examples:

- service-only presets used by gateway bots
- internal presets that require resolver-produced fields
- migration-only presets that should remain callable but not discoverable

Those presets should remain in the API catalog and carry explicit visibility metadata rather than living in a second UI-only list.

The long-term model should support at least:

- visible to human launch surfaces
- visible only to service principals
- hidden but still addressable by ID

The exact field name is an implementation detail. The important rule is that visibility belongs to the API catalog, not to a separate UI preset definition.

## UI Contract

The human web UI should:

1. call `GET /api/presets`,
2. render the returned launchable presets,
3. keep the selected preset ID in form state,
4. submit creates with `presetId`,
5. treat image-only launches as the explicit custom-image path.

The UI should not need preset env values, because env injection is a server-side concern.

## Transitional Guidance

Today some deployments still use `ui.presets`.

Until the UI is switched to load presets from the API directly:

- define presets in `api.presets`,
- mirror the same entries into `ui.presets` from the same values source,
- prefer YAML anchors or templating so the two lists cannot drift by hand,
- include stable `id` values in the mirrored UI entries so create requests still submit `presetId`.

This is a temporary deployment pattern. It should not be treated as the end state.

## Validation

Any implementation of this model should be considered complete only when all of the following are true:

- `GET /api/presets` returns the launchable presets needed by the UI
- the UI no longer depends on a separate preset list for launch behavior
- selecting a preset creates with `presetId`, not raw `spec.image`
- preset-backed env is applied on created instances
- hidden or service-only presets do not appear in the human launch picker
- docs and Helm examples stop teaching `ui.presets` as the primary preset definition surface

## References

- [README.md](../README.md)
- [2026-03-11-external-provisioner-and-service-principal-architecture.md](2026-03-11-external-provisioner-and-service-principal-architecture.md)
- [2026-03-13-openclaw-integration.md](2026-03-13-openclaw-integration.md)
- [2026-03-14-local-kind-development-guide.md](2026-03-14-local-kind-development-guide.md)
