---
date: 2026-03-30
author: Onur Solmaz <onur@textcortex.com>
title: Agent Profile API
tags: [spritz, agents, ui, api, architecture]
---

## Overview

This document defines a provider-agnostic agent profile API for rendering a
Spritz instance with deployment-owned cosmetic metadata such as:

- name
- image URL

The goal is to let deployment-owned systems tell Spritz how an instance should
appear in the UI without making ACP runtime identity or deployment-wide
branding carry that responsibility.

The design keeps three concepts separate:

- deployment-wide product branding
- per-instance agent profile
- ACP runtime identity

## Goals

- Make per-instance agent profile data a first-class Spritz concept.
- Keep the contract provider-agnostic and safe for open-source use.
- Preserve a clean control-plane split between desired state and synced
  external state.
- Give all Spritz UIs one canonical read shape for agent name and image.
- Allow deployment-owned systems to sync profile data using the extension
  framework.
- Keep ACP `agentInfo` focused on runtime protocol identity, not UI branding.

## Non-goals

- Do not add deployment-specific business logic to Spritz core.
- Do not turn ACP `agentInfo` into a mutable branding surface.
- Do not make browser clients call deployment-owned systems directly to fetch
  profile data.
- Do not introduce per-tenant or per-user global UI theming here.
- Do not require every instance to have an external agent reference.

## Problem statement

Today, Spritz has two nearby but different concepts:

- deployment-wide UI branding documented in
  [2026-03-20-ui-branding-customization.md](2026-03-20-ui-branding-customization.md)
- ACP runtime identity in `status.acp.agentInfo`

Neither is the right home for per-instance agent profile data.

Deployment-wide branding is too coarse because one Spritz install may host many
instances that should present as different agents.

ACP `agentInfo` is also the wrong source because it represents runtime protocol
identity exposed by the image or ACP adapter. It should not be overloaded to
carry deployment-owned display choices such as:

- "show this instance as Research Assistant"
- "use this image from an external agent catalog"

If Spritz keeps using ACP metadata for rendering, UI identity becomes coupled
to runtime image behavior instead of control-plane state.

## Design principles

### Profile data is control-plane data

Per-instance profile data should be synced and stored in Spritz control-plane
state, not fetched by the browser at render time.

### Desired and observed state stay separate

Deployment-owned references and local overrides belong in `spec`.

Resolved display values from external systems belong in `status`.

### Profile data is not runtime identity

ACP metadata continues to answer:

- what runtime is this
- what protocol version and capabilities does it expose

Profile data answers:

- what should the UI call this instance
- what image should the UI show

### UIs should read one canonical profile shape

Native Spritz UI and embedded consumers should use the same precedence and the
same profile fields.

### External sync should be explicit

If a deployment wants Spritz to show an instance as an external agent, the
instance should contain an explicit opaque reference instead of encoding that
knowledge indirectly in annotations or ACP metadata.

## Canonical resource model

The recommended model is:

```yaml
spec:
  agentRef:
    type: external
    provider: example-catalog
    id: agent-123
  profileOverrides:
    name: "Example Assistant"
    imageUrl: "https://console.example.com/assets/example-assistant.png"

status:
  profile:
    name: "Example Assistant"
    imageUrl: "https://console.example.com/assets/example-assistant.png"
    source: synced
    observedGeneration: 7
    syncer: deployment-agent-profile
    lastSyncedAt: "2026-03-30T12:00:00Z"
```

Recommended types:

- `spec.agentRef`
  - optional
  - opaque reference to a deployment-owned agent or catalog entry
  - Spritz validates shape, not business semantics
  - use `type` for the internal field name; if an external payload sends
    `kind`, convert it at the boundary
- `spec.profileOverrides`
  - optional
  - operator- or caller-supplied local override values
  - highest-priority desired-state input
- `status.profile`
  - canonical UI output
  - what every UI should read

## Proposed type definitions

Suggested CRD additions:

```go
type SpritzAgentRef struct {
    Type     string `json:"type,omitempty"`
    Provider string `json:"provider,omitempty"`
    ID       string `json:"id,omitempty"`
}

type SpritzAgentProfile struct {
    Name     string `json:"name,omitempty"`
    ImageURL string `json:"imageUrl,omitempty"`
}

type SpritzAgentProfileStatus struct {
    Name               string       `json:"name,omitempty"`
    ImageURL           string       `json:"imageUrl,omitempty"`
    Source             string       `json:"source,omitempty"`
    Syncer             string       `json:"syncer,omitempty"`
    ObservedGeneration int64        `json:"observedGeneration,omitempty"`
    LastSyncedAt       *metav1.Time `json:"lastSyncedAt,omitempty"`
    LastError          string       `json:"lastError,omitempty"`
}
```

Suggested placements:

- `spritz.spec.agentRef`
- `spritz.spec.profileOverrides`
- `spritz.status.profile`

If conversation resources need profile snapshots later, they should carry their
own optional derived snapshot as a cache, not as the canonical source of
truth.

## Why this model is preferred

This model is cleaner than storing synced profile data in `spec` because:

- `spec` remains caller intent
- `status` remains observed and reconciled state
- the system can refresh external profile data later without rewriting desired
  state
- UIs can trust one stable profile output
- overrides remain explicit and inspectable

This is also cleaner than using only `metadata.annotations` because:

- annotations are untyped
- validation is weaker
- UI consumers need field-specific parsing logic
- the contract becomes harder to evolve safely

## Sync model

Spritz should add one extension operation for agent profile sync:

- `agent.profile.sync`

Its input should contain only the facts needed to compute the profile:

```json
{
  "version": "v1",
  "extensionId": "deployment-agent-profile",
  "type": "resolver",
  "operation": "agent.profile.sync",
  "context": {
    "namespace": "spritz-system",
    "instanceClassId": "personal-agent"
  },
  "input": {
    "owner": { "id": "user-123" },
    "agentRef": {
      "type": "external",
      "provider": "example-catalog",
      "id": "agent-123"
    },
    "profileOverrides": {
      "name": "Example Assistant"
    }
  }
}
```

The response should be narrow:

```json
{
  "status": "synced",
  "output": {
    "profile": {
      "name": "Example Assistant",
      "imageUrl": "https://console.example.com/assets/example-assistant.png"
    }
  }
}
```

The extension should return profile data only. It should not mutate arbitrary
resource state.

## Precedence rules

For the instance name, canonical precedence should be:

1. `spec.profileOverrides.name`
2. synced extension output from `agent.profile.sync`
3. ACP `agentInfo.title`
4. ACP `agentInfo.name`
5. `metadata.name`

For the image URL, canonical precedence should be:

1. `spec.profileOverrides.imageUrl`
2. synced extension output from `agent.profile.sync`
3. no image URL

This precedence should be materialized into `status.profile` so the UI does not
need to re-implement the logic in multiple places.

That means the browser should normally read:

- `status.profile.name`
- `status.profile.imageUrl`

If `status.profile.imageUrl` is empty, the UI can fall back to initials or a
generic placeholder.

## Conversation model

The canonical source of per-instance profile data should stay on the instance
resource, not on `SpritzConversation`.

Conversation resources already reference the parent instance by `spritzName`.
Native UI and embedded consumers can join against the parent instance when
needed.

If later profiling shows that repeated joins are too expensive, Spritz can add
an optional derived snapshot to conversation state. That snapshot should still
be treated as a cache of instance profile data, not the source of truth.

## API and controller changes

### API changes

- extend `operator/api/v1/spritz_types.go` with:
  - `SpritzAgentRef`
  - `SpritzAgentProfile`
  - `SpritzAgentProfileStatus`
- update public API serialization so `profile` is included in
  instance reads and lists
- keep `status.acp.agentInfo` unchanged

### Extension framework changes

- add `agent.profile.sync` as a supported operation in the extension registry
- define a typed request and response envelope for profile sync
- validate that the extension can only return profile fields

### Reconciliation changes

Spritz needs a control-plane component that computes `status.profile`.

Recommended sequence:

1. normalize `spec.agentRef` and `spec.profileOverrides`
2. if overrides fully satisfy the profile, use them directly
3. else, if `agentRef` is present, call `agent.profile.sync`
4. merge using the canonical precedence rules
5. write the result to `status.profile`
6. record sync metadata such as:
   - `source`
   - `syncer`
   - `observedGeneration`
   - `lastSyncedAt`
   - `lastError`

The first implementation can run this logic in the API create/update path plus
an explicit refresh endpoint if needed.

The long-term preferred implementation is a reconciliation loop that keeps
`status.profile` current whenever:

- `spec.agentRef` changes
- `spec.profileOverrides` changes
- a caller requests refresh

## Suggested implementation phases

### Phase 1: typed model and UI read path

- add typed `agentRef`, `profileOverrides`, and `profile`
- add UI helpers that prefer `status.profile`
- keep ACP metadata as fallback only

This phase creates the durable contract first.

### Phase 2: extension integration

- add `agent.profile.sync`
- sync profile data during create and update
- materialize the merged result into `status.profile`

This phase gives deployments a provider-agnostic hook.

### Phase 3: refresh and reconciliation

- add explicit refresh semantics
- reconcile stale or missing profile data after create
- support background re-sync without rewriting `spec`

This phase makes external profile data durable over time instead of treating it
as a one-time create artifact.

## Validation

Required validation:

- unit tests for precedence logic
- unit tests for merge behavior between overrides, synced profile data, ACP
  metadata, and instance name
- API tests for instance list and get responses
- extension tests for:
  - synced
  - missing
  - forbidden
  - invalid
- reconciliation tests proving `status.profile` updates when
  `spec.profileOverrides` changes
- UI tests proving:
  - `status.profile` is preferred
  - ACP metadata remains a fallback
  - instance name remains the final fallback

## Migration notes

Existing installations may already render from ACP metadata or instance name.

Migration should therefore be additive:

1. introduce the new fields
2. ship UIs that prefer `status.profile`
3. start writing `status.profile`
4. keep ACP fallback behavior until the new field is broadly available

This avoids breaking existing runtimes or forcing immediate deployment-specific
extension adoption.

## References

- [2026-03-19-unified-extension-framework-architecture.md](2026-03-19-unified-extension-framework-architecture.md)
- [2026-03-20-ui-branding-customization.md](2026-03-20-ui-branding-customization.md)
