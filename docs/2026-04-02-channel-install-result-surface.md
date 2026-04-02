---
date: 2026-04-02
author: Onur Solmaz <onur@textcortex.com>
title: Channel Install Result Surface
tags: [spritz, channel-gateway, slack, oauth, error-handling, architecture]
---

## Overview

Shared channel installs must always end on a Spritz-controlled result surface.

In concrete terms, the browser should never fall through from a provider
callback to an infrastructure-generated error page such as a CDN or proxy
`502`. If the install fails for an expected product reason, Spritz should show
that failure as product UI.

This document defines the generic Spritz-side contract for:

- install success
- install failure
- typed browser-facing install errors
- the split between Spritz-owned UX and deployment-owned install policy

The immediate motivation is Slack workspace install UX, but the same model
should apply to any shared channel gateway.

Related docs:

- [Shared App Tenant Routing Architecture](2026-03-23-shared-app-tenant-routing-architecture.md)
- [Slack Channel Gateway Implementation Plan](2026-03-24-slack-channel-gateway-implementation-plan.md)
- [External Identity Resolution API Architecture](2026-03-12-external-identity-resolution-api-architecture.md)

## Problem

The callback flow currently has an important UX gap:

1. the user starts a provider install flow
2. the provider redirects back to the shared channel gateway callback
3. the gateway exchanges the callback code and finalizes install state
4. a deployment-owned backend returns an expected business failure
5. the browser sees a generic proxy or upstream error page

That is the wrong contract.

Expected install failures such as unresolved identity, denied authorization, or
installation conflict are not infrastructure failures. They are product
outcomes and must be rendered as product UI.

## Goals

- always show a clear success or failure surface after install callback
- keep the install result flow provider-agnostic inside Spritz
- preserve typed error information without leaking backend internals
- keep request correlation so operators can map a visible failure back to logs

## Non-Goals

- changing deployment-specific owner or account-linking policy
- defining provider-specific copy inside Spritz core
- moving deployment-specific installation storage into Spritz
- replacing typed API errors with free-form HTML from external backends

## Core Decision

The provider callback is app-controlled.

Here, "app-controlled" means the result is owned by the shared channel gateway
or the Spritz UI, not by a reverse proxy, ingress, CDN, or provider-generated
error page.

The callback must always terminate in one of two Spritz-owned outcomes:

- install success
- install failure

Recommended high-level flow:

1. gateway receives the provider callback
2. gateway validates callback state
3. gateway exchanges the provider code
4. gateway calls the deployment-owned install finalizer
5. gateway normalizes the outcome into a stable Spritz result
6. gateway redirects to or renders a Spritz-owned result surface

## Result Surface

Spritz should expose one stable install-result surface, for example:

- a UI route such as `/install/result`
- or a minimal HTML page rendered directly by the gateway when no richer UI is
  available

The long-term preferred shape is a dedicated Spritz UI route, but either option
is acceptable as long as the surface is app-controlled.

The normalized result payload should carry at least:

- `status`: `success | error`
- `provider`
- `code`
- `requestId`
- `retryable`
- optional safe display metadata such as next-step hints

Rules:

- browser-facing copy must come from the normalized result code
- raw backend payloads must not be shown directly to the user
- expected failures must not render as raw `5xx` infrastructure pages

## Error Taxonomy

Spritz should define a stable set of install error codes.

Recommended initial set:

- `install_state_invalid`
- `install_state_expired`
- `provider_authorization_denied`
- `provider_authorization_failed`
- `external_identity_unresolved`
- `external_identity_forbidden`
- `external_identity_ambiguous`
- `installation_conflict`
- `installation_registry_unavailable`
- `runtime_binding_unavailable`
- `internal_error`

Notes:

- the `external_identity_*` codes should align with the typed error model from
  [External Identity Resolution API Architecture](2026-03-12-external-identity-resolution-api-architecture.md)
- retryable upstream failures should map to typed availability codes, not a raw
  browser-visible `502`
- unknown failures may collapse to `internal_error`

## Scope Split

### Spritz owns

- callback termination behavior
- the install result route or result page
- the install error taxonomy
- safe default result-page copy
- request ID propagation into logs and user-visible result pages

### Deployment-owned integration owns

- install finalization policy
- identity and account-linking policy
- installation registry implementation
- branding and deployment-specific copy overrides
- any fallback policy involving an existing first-party browser session

This keeps Spritz reusable while still letting each deployment enforce its own
ownership and identity rules.

## Backend And Adapter Contract

The deployment-owned install finalizer should not force the gateway to infer
product meaning from arbitrary upstream failures.

Preferred contract:

- success returns a normalized install success payload
- expected failures return typed machine-readable codes
- temporary failures return typed availability errors

If an existing deployment backend does not yet expose the normalized shape, the
deployment adapter in the gateway must translate backend-specific responses into
the Spritz install-result taxonomy before the browser sees them.

## User-Facing Behavior

The result surface should stay simple and production-oriented:

- always show success or failure explicitly
- always explain the next action when one exists
- always show a request ID for support and debugging
- never show stack traces or raw upstream payloads

Examples:

- `external_identity_unresolved`: tell the user the install could not be linked
  to a product account yet
- `install_state_expired`: tell the user the install link expired and should be
  started again
- `installation_registry_unavailable`: tell the user the install could not be
  completed right now and can be retried

## Validation

Minimum validation for this flow:

- invalid or expired state shows a controlled error surface
- unresolved external identity shows a controlled error surface
- temporary install-finalizer outage shows a controlled retryable error surface
- successful install shows a controlled confirmation surface
- every result logs the same `requestId` visible to the user

## Follow-Ups

- add the install-result route or gateway-rendered fallback
- wire the shared Slack gateway callback to the normalized result contract
- reuse the same result surface for future Discord and Teams shared installs
