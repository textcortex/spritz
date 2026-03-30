---
date: 2026-03-30
author: Onur Solmaz <onur@textcortex.com>
title: Agent Presentation Resolution Architecture
tags: [spritz, agents, ui, api, architecture]
---

## Overview

This document defines a provider-agnostic model for rendering a Spritz instance
as an agent with deployment-owned presentation metadata such as:

- display name
- avatar URL

The goal is to let deployment-owned systems tell Spritz how an instance should
be presented in the UI without making ACP runtime identity or deployment-wide
branding carry that responsibility.

The design keeps three concepts separate:

- deployment-wide product branding
- per-instance agent presentation
- ACP runtime identity

## Goals

- Make per-instance agent presentation a first-class Spritz concept.
- Keep the contract provider-agnostic and safe for open-source use.
- Preserve a clean control-plane split between desired state and resolved
  external state.
- Give all Spritz UIs one canonical read shape for display name and avatar.
- Allow deployment-owned systems to resolve presentation using the extension
  framework.
- Keep ACP `agentInfo` focused on runtime protocol identity, not UI branding.

## Non-goals

- Do not add deployment-specific business logic to Spritz core.
- Do not turn ACP `agentInfo` into a mutable branding surface.
- Do not make browser clients call deployment-owned systems directly to fetch
  presentation data.
- Do not introduce per-tenant or per-user global UI theming here.
- Do not require every instance to have an external agent reference.

## Problem statement

Today, Spritz has two nearby but different concepts:

- deployment-wide UI branding documented in
  [2026-03-20-ui-branding-customization.md](2026-03-20-ui-branding-customization.md)
- ACP runtime identity in `status.acp.agentInfo`

Neither is the right home for per-instance agent presentation.

Deployment-wide branding is too coarse because one Spritz install may host many
instances that should present as different agents.

ACP `agentInfo` is also the wrong source because it represents runtime protocol
identity exposed by the image or ACP adapter. It should not be overloaded to
carry deployment-owned display choices such as:

- "show this instance as Research Assistant"
- "use this avatar from an external agent catalog"

If Spritz keeps using ACP metadata for rendering, UI identity becomes coupled
to runtime image behavior instead of control-plane state.

## Design principles

### Presentation is control-plane data

Per-instance presentation should be resolved and stored in Spritz control-plane
state, not fetched by the browser at render time.

### Desired and observed state stay separate

Deployment-owned references and local overrides belong in `spec`.

Resolved display values from external systems belong in `status`.

### Presentation is not runtime identity

ACP metadata continues to answer:

- what runtime is this
- what protocol version and capabilities does it expose

Presentation answers:

- what should the UI call this instance
- what avatar should the UI show

### UIs should read one canonical resolved shape

Native Spritz UI and embedded consumers should use the same precedence and the
same resolved fields.

### External resolution should be explicit

If a deployment wants Spritz to present an instance as an external agent, the
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
  presentationOverrides:
    displayName: "Example Assistant"
    avatarUrl: "https://console.example.com/assets/example-assistant.png"

status:
  resolvedPresentation:
    displayName: "Example Assistant"
    avatarUrl: "https://console.example.com/assets/example-assistant.png"
    source: resolved
    observedGeneration: 7
    resolver: deployment-agent-presentation
    lastResolvedAt: "2026-03-30T12:00:00Z"
```

Recommended types:

- `spec.agentRef`
  - optional
  - opaque reference to a deployment-owned agent or catalog entry
  - Spritz validates shape, not business semantics
  - use `type` for the internal field name; if an external payload sends
    `kind`, convert it at the boundary
- `spec.presentationOverrides`
  - optional
  - operator or caller supplied local override values
  - highest-priority desired-state input
- `status.resolvedPresentation`
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

type SpritzPresentation struct {
    DisplayName string `json:"displayName,omitempty"`
    AvatarURL   string `json:"avatarUrl,omitempty"`
}

type SpritzResolvedPresentation struct {
    DisplayName        string       `json:"displayName,omitempty"`
    AvatarURL          string       `json:"avatarUrl,omitempty"`
    Source             string       `json:"source,omitempty"`
    Resolver           string       `json:"resolver,omitempty"`
    ObservedGeneration int64        `json:"observedGeneration,omitempty"`
    LastResolvedAt     *metav1.Time `json:"lastResolvedAt,omitempty"`
    LastError          string       `json:"lastError,omitempty"`
}
```

Suggested placements:

- `spritz.spec.agentRef`
- `spritz.spec.presentationOverrides`
- `spritz.status.resolvedPresentation`

If conversation resources need presentation snapshots later, they should carry
their own optional resolved snapshot as a derived cache, not as the canonical
source of truth.

## Why this model is preferred

This model is cleaner than storing resolved presentation in `spec` because:

- `spec` remains caller intent
- `status` remains observed and reconciled state
- the system can refresh external presentation later without rewriting desired
  state
- UIs can trust one stable resolved output
- overrides remain explicit and inspectable

This is also cleaner than using only `metadata.annotations` because:

- annotations are untyped
- validation is weaker
- UI consumers need field-specific parsing logic
- the contract becomes harder to evolve safely

## Resolution model

Spritz should add one resolver operation for presentation:

- `agent.presentation.resolve`

Its input should contain only the facts needed for resolution:

```json
{
  "version": "v1",
  "extensionId": "deployment-agent-presentation",
  "type": "resolver",
  "operation": "agent.presentation.resolve",
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
    "presentationOverrides": {
      "displayName": "Example Assistant"
    }
  }
}
```

The response should be narrow:

```json
{
  "status": "resolved",
  "output": {
    "presentation": {
      "displayName": "Example Assistant",
      "avatarUrl": "https://console.example.com/assets/example-assistant.png"
    }
  }
}
```

The resolver should return resolved presentation only. It should not mutate
arbitrary resource state.

## Precedence rules

Canonical precedence should be:

1. `spec.presentationOverrides`
2. resolved extension output from `agent.presentation.resolve`
3. ACP `agentInfo.title`
4. ACP `agentInfo.name`
5. `metadata.name`

This precedence should be materialized into `status.resolvedPresentation` so
the UI does not need to re-implement the logic in multiple places.

That means the browser should normally read:

- `status.resolvedPresentation.displayName`
- `status.resolvedPresentation.avatarUrl`

and only fall back further if `resolvedPresentation` is absent.

## Conversation model

The canonical source of per-instance presentation should stay on the instance
resource, not on `SpritzConversation`.

Conversation resources already reference the parent instance by `spritzName`.
Native UI and embedded consumers can join against the parent instance when
needed.

If later profiling shows that repeated joins are too expensive, Spritz can add
an optional derived snapshot to conversation state. That snapshot should still
be treated as a cache of instance presentation, not the source of truth.

## API and controller changes

### API changes

- extend `operator/api/v1/spritz_types.go` with:
  - `SpritzAgentRef`
  - `SpritzPresentation`
  - `SpritzResolvedPresentation`
- update public API serialization so `resolvedPresentation` is included in
  instance reads and lists
- keep `status.acp.agentInfo` unchanged

### Extension framework changes

- add `agent.presentation.resolve` as a supported operation in the extension
  registry
- define a typed request and response envelope for presentation resolution
- validate that the resolver can only return presentation fields

### Reconciliation changes

Spritz needs a control-plane component that computes
`status.resolvedPresentation`.

Recommended sequence:

1. normalize `spec.agentRef` and `spec.presentationOverrides`
2. if overrides fully satisfy presentation, use them directly
3. else, if `agentRef` is present, call `agent.presentation.resolve`
4. merge using the canonical precedence rules
5. write the result to `status.resolvedPresentation`
6. record resolution metadata such as:
   - `source`
   - `resolver`
   - `observedGeneration`
   - `lastResolvedAt`
   - `lastError`

The first implementation can run this logic in the API create/update path plus
an explicit refresh endpoint if needed.

The long-term preferred implementation is a reconciliation loop that keeps
`status.resolvedPresentation` current whenever:

- `spec.agentRef` changes
- `spec.presentationOverrides` changes
- a caller requests refresh

## Suggested implementation phases

### Phase 1: typed model and UI read path

- add typed `agentRef`, `presentationOverrides`, and `resolvedPresentation`
- add UI helpers that prefer `status.resolvedPresentation`
- keep ACP metadata as fallback only

This phase creates the durable contract first.

### Phase 2: resolver integration

- add `agent.presentation.resolve`
- resolve presentation during create and update
- materialize the merged result into `status.resolvedPresentation`

This phase gives deployments a provider-agnostic hook.

### Phase 3: refresh and reconciliation

- add explicit refresh semantics
- reconcile stale or missing presentation after create
- support background re-resolution without rewriting `spec`

This phase makes external presentation durable over time instead of treating it
as a one-time create artifact.

## Validation

Required validation:

- unit tests for precedence logic
- unit tests for merge behavior between overrides, resolved presentation, ACP
  metadata, and instance name
- API tests for instance list and get responses
- extension tests for:
  - resolved
  - unresolved
  - forbidden
  - invalid
- reconciliation tests proving `status.resolvedPresentation` updates when
  `spec.presentationOverrides` changes
- UI tests proving:
  - resolved presentation is preferred
  - ACP metadata remains a fallback
  - instance name remains the final fallback

## Migration notes

Existing installations may already render from ACP metadata or instance name.

Migration should therefore be additive:

1. introduce the new fields
2. ship UIs that prefer `status.resolvedPresentation`
3. start writing `resolvedPresentation`
4. keep ACP fallback behavior until the new field is broadly available

This avoids breaking existing runtimes or forcing immediate deployment-specific
resolver adoption.

## References

- [2026-03-19-unified-extension-framework-architecture.md](2026-03-19-unified-extension-framework-architecture.md)
- [2026-03-20-ui-branding-customization.md](2026-03-20-ui-branding-customization.md)
