---
date: 2026-04-03
author: Onur Solmaz <onur@textcortex.com>
title: Unified Public Error Architecture
tags: [spritz, error-handling, api, ui, architecture]
---

## Overview

Goal in plain English:

If a user tries to install a channel app, create an instance, open a runtime,
or start a session and something expected goes wrong, Spritz should always say:

- what failed
- what the user can do next
- whether retry makes sense
- which request ID to share with support

It should not leak a raw proxy error, a vague upstream failure, or a generic
operation-local code that hides the real category of the problem.

The long-term goal is one canonical public error model and one app-controlled
rendering model across all user-visible Spritz flows.

Related docs:

- [Channel Install Result Surface](2026-04-02-channel-install-result-surface.md)
- [External Identity Resolution API Architecture](2026-03-12-external-identity-resolution-api-architecture.md)
- [Unified Extension Framework Architecture](2026-03-19-unified-extension-framework-architecture.md)

## Problem

Spritz currently has the start of the right model, but not the full system.

What already exists:

- install flow now has a typed result surface
- the shared channel gateway can redirect to a Spritz-controlled result page
- some internal APIs already produce typed states such as `resolved`,
  `unresolved`, `forbidden`, `ambiguous`, `invalid`, and `unavailable`

What is still fragmented:

- create-time resolver failures still collapse into operation-local codes such
  as `preset_create_unresolved`
- browser flows and API flows do not share one explicit public error contract
- the same logical failure can look different depending on which component hit
  it first
- internal causes are not consistently separated from user-facing meaning

This creates three recurring problems:

1. users get vague or misleading errors
2. adapters and UIs re-invent error handling per flow
3. operators have to infer product meaning from low-level logs

## Goals

- define one canonical public error contract for Spritz
- keep browser-visible failures app-controlled
- make API, gateway, and UI surfaces render from the same typed reason model
- separate user-facing meaning from internal cause chains
- keep deployment-specific branding and copy overrides possible without letting
  deployments invent new semantics ad hoc

## Non-Goals

- exposing raw upstream payloads to end users
- replacing all existing transport envelopes immediately
- moving deployment-specific business policy into Spritz core
- requiring every deployment to use the same copy or visual design

## Core Decision

Every user-visible Spritz flow should terminate in a Spritz-owned public error
contract.

That includes:

- channel install
- instance create
- runtime bootstrap
- chat/session startup
- channel message delivery when the user is expected to take an action

The core pattern is:

1. a backend, resolver, hook, or adapter produces an internal result
2. Spritz normalizes that result into one canonical public error shape
3. the caller surface renders that normalized error
4. internal causes remain in logs, traces, and operator metadata only

## Canonical Public Error Contract

Spritz should define a reusable public error object, independent of the
transport used to deliver it.

Recommended shape:

```json
{
  "code": "identity.unresolved",
  "operation": "spritz.create",
  "title": "Account could not be linked",
  "message": "This request could not be linked to an owner account yet.",
  "retryable": false,
  "action": {
    "type": "link_account",
    "label": "Link account"
  },
  "requestId": "req_123",
  "subject": {
    "presetId": "tcdev"
  }
}
```

Required fields:

- `code`: stable machine-readable reason
- `operation`: stable machine-readable operation such as `channel.install` or
  `spritz.create`
- `message`: safe default user-facing description
- `retryable`: whether immediate retry is sensible
- `requestId`: correlation identifier shown to the user and logs

Recommended fields:

- `title`
- `action`
- `subject`
- `docsUrl`

Rules:

- `code` is the stable contract
- `message` and `title` are safe defaults, not the source of truth
- `operation` tells the UI which flow failed without inventing a new error
  taxonomy per flow
- the same logical reason should keep the same `code` across flows

## Canonical Reason Taxonomy

Spritz should use generic, cross-flow reason codes instead of operation-local
codes such as `preset_create_unresolved`.

Recommended initial taxonomy:

- `state.invalid`
- `state.expired`
- `auth.denied`
- `auth.failed`
- `identity.unresolved`
- `identity.forbidden`
- `identity.ambiguous`
- `policy.forbidden`
- `resolver.invalid`
- `resolver.unavailable`
- `binding.unavailable`
- `registry.conflict`
- `runtime.unavailable`
- `internal.error`

The important rule is that `code` describes the failure category, while
`operation` describes where it happened.

Example mappings:

- install callback could not link identity:
  - `operation=channel.install`
  - `code=identity.unresolved`
- create-time preset resolver could not resolve owner:
  - `operation=spritz.create`
  - `code=identity.unresolved`
- runtime binding backend is temporarily unreachable:
  - `operation=spritz.create`
  - `code=resolver.unavailable`

This avoids creating parallel taxonomies such as:

- `external_identity_unresolved`
- `preset_create_unresolved`
- `runtime_binding_unavailable`

when the public meaning is actually the same category with different
operations.

## Internal Cause Chain

The public error should not carry the full internal cause chain.

Spritz should keep an internal error record for logs and traces with fields
such as:

- `requestId`
- `operation`
- `publicCode`
- `component`
- `resolverId`
- `upstreamStatus`
- `retryable`
- `cause`
- `details`

Example:

```json
{
  "requestId": "req_123",
  "operation": "spritz.create",
  "publicCode": "identity.unresolved",
  "component": "preset-resolver",
  "resolverId": "preset-create-resolver",
  "upstreamStatus": "unresolved",
  "cause": "owner lookup returned no matching account"
}
```

Rules:

- internal logs may contain raw cause data
- public responses must not
- every public error should be traceable to one request ID

## Transport And Rendering Model

### API responses

Spritz should keep its existing response envelope style if desired, but public
errors should be embedded in a consistent structured payload rather than
operation-specific ad hoc objects.

For example:

```json
{
  "status": "fail",
  "data": {
    "error": {
      "code": "identity.unresolved",
      "operation": "spritz.create",
      "message": "This request could not be linked to an owner account yet.",
      "retryable": false,
      "requestId": "req_123"
    }
  }
}
```

The current `jsend.go` helpers are too small to express this well. Spritz
should add explicit writers for typed public errors rather than relying on
free-form `message` strings or flow-local `error` keys.

### Browser flows

Browser-visible flows should always terminate in an app-controlled surface.

Examples:

- install callback redirects to `/install/result`
- create UI can render inline from the same error contract
- flows that cannot render inline may redirect to `/result` with a stable error
  payload reference or query-state key

Rules:

- expected failures must not fall through to raw proxy pages
- browser copy must come from `operation + code`
- request ID must always be visible

### CLI and agent flows

CLI and agent clients should render the same structured contract in text form.

That means:

- a clear one-line summary
- next action if one exists
- request ID

They should not need operation-specific parsing logic for each backend path.

## Extension And Adapter Contract

Resolvers and hooks should continue returning operation-local status, but they
should also be able to provide a canonical public reason.

Recommended extension response fields:

```json
{
  "status": "unresolved",
  "code": "identity.unresolved",
  "retryable": false,
  "safeDetails": {
    "provider": "slack"
  }
}
```

Rules:

- `status` remains the resolver-local execution result
- `code` becomes the public semantic reason
- `safeDetails` may be shown to the UI or gateway
- raw upstream bodies stay out of the public payload

If a resolver does not provide a `code`, Spritz core should map the local
status to a default public code.

## Flow-Specific Behavior

### Install

Install already has the right direction:

- normalize backend outcomes
- redirect to a Spritz-owned result surface
- render copy from typed codes

The next step is to move install onto the shared public error contract instead
of keeping a separate install-only shape long term.

### Create

Create is the biggest current gap.

Today, create admission still returns operation-local codes such as
`preset_create_unresolved`.

The desired model is:

- normalize resolver output into canonical `code`
- preserve `operation=spritz.create`
- render the same structure in API, UI, and CLI
- keep resolver IDs and low-level causes in logs only

### Runtime bootstrap and session startup

These flows often fail after create succeeds.

They should still use the same public error contract:

- `operation=runtime.bootstrap`
- `operation=acp.connect`
- `operation=channel.deliver`

The user-facing error model should not depend on whether the failure happened
before or after the instance resource existed.

## Scope Split

### Spritz owns

- canonical public reason codes
- public error object shape
- default titles, messages, and retryability
- request ID propagation
- app-controlled result surfaces
- mapping local resolver statuses into public reasons

### Deployments own

- branding
- copy overrides
- flow-specific action targets such as a deployment login page or account-link
  page
- low-level integration implementation

Deployments may override presentation, but they should not invent their own
semantic error categories outside the Spritz contract.

## Phased Rollout

### Phase 1: Canonical error primitives

- add a reusable public error type in core
- add typed writers in the API layer
- define the first stable reason taxonomy

### Phase 2: Resolver-aware normalization

- let extension responses carry optional canonical `code`
- add default mappings from existing statuses to public reasons
- emit structured operator logs with `requestId`, `operation`, and `publicCode`

### Phase 3: Create flow adoption

- replace create-time `preset_create_*` public responses with canonical public
  errors
- update UI and CLI create surfaces to render from the shared contract

### Phase 4: Runtime and session adoption

- apply the same model to bootstrap, session startup, and channel delivery
- ensure browser-visible failures stay app-controlled

### Phase 5: Install convergence

- move install result flow onto the same shared public error object
- keep the install result route, but make it consume the canonical contract

## Validation

Minimum validation for this architecture:

- install, create, and runtime flows all expose `code`, `operation`,
  `retryable`, and `requestId`
- expected failures never surface as raw proxy pages in browser flows
- the same logical failure category uses the same `code` across flows
- operator logs include the internal cause chain for every public error
- CLI and browser surfaces can render without flow-specific parsing hacks

## Holy Grail

The holy grail is simple:

- one public error model
- one rendering model
- many internal causes

If a user sees an error in Spritz, it should behave like a large production
platform:

- clear
- typed
- correlated
- actionable
- controlled by the application surface, not by infrastructure accidents

That is the standard every major user-visible Spritz flow should meet.
