---
date: 2026-03-20
author: Onur Solmaz <onur@textcortex.com>
title: Shared-Host Auth Gateway Architecture
tags: [spritz, auth, oauth2, oidc, gateway, architecture]
---

## Overview

This document defines the supported browser authentication architecture for a
Spritz deployment that serves UI, API, and instance surfaces from one public
host.

The core rule is simple:

- one browser host should have one browser-facing auth gateway by default

For example, if a deployment uses `https://spritz.example.com` for the Spritz
console, that host should have one auth gateway that owns browser login,
callback handling, and session cookies for all protected paths on that host.

## Problem

A shared host can expose more than one protected surface:

- `/` for the UI
- `/api` for the API
- `/i/*` for instance access
- `/c/*` for canonical chat URLs

If each surface runs its own independent auth gateway instance, the deployment
stops having one coherent browser auth boundary. Even if those gateway
instances use different cookie names, they can still interfere with each other
when they share callback ownership on the same host.

Typical failure mode:

1. The browser loads `/`.
2. The UI or a background poll also hits `/api`.
3. A second gateway instance starts a fresh login flow for `/api`.
4. The identity provider returns to the shared callback path on the host.
5. The wrong gateway instance receives the callback and tries to redeem the
   code.
6. PKCE verification fails because that gateway did not initiate the flow.

This usually surfaces as:

- `invalid_grant`
- `PKCE verification failed`
- redirect loops
- login succeeding in one browser and failing in another depending on timing

## Goals

- Keep browser auth deterministic on a shared host.
- Make callback ownership unambiguous.
- Avoid per-surface OAuth state collisions.
- Keep downstream Spritz services free of browser login logic.
- Preserve a portable deployment model that works across ingress stacks.

## Default Architecture

For one public browser host, use one browser-facing auth gateway.

### Topology

- `https://spritz.example.com` is the public host.
- One auth gateway handles:
  - login initiation
  - callback handling
  - browser session cookies
  - identity header forwarding to protected upstreams
- Upstream routing behind that gateway fans out to:
  - `spritz-ui`
  - `spritz-api`
  - instance-facing upstreams

### Route Ownership

Recommended route ownership:

- `/oauth2/*` -> auth gateway endpoints
- `/` -> Spritz UI upstream behind the auth gateway
- `/api/*` -> Spritz API upstream behind the auth gateway
- `/i/*` -> instance upstream behind the auth gateway
- `/c/*` -> chat UI route behind the auth gateway

The important invariant is:

- browser requests on the host cross one auth state machine before they reach
  Spritz surfaces

### Downstream Responsibilities

With this topology:

- Spritz UI does not initiate OIDC itself.
- Spritz API trusts gateway-provided identity headers when configured for
  header auth.
- Instance-facing upstreams do not run their own browser auth flow on the same
  host.

## Product-Owned Contract

This shared-host browser model should be a first-class Spritz deployment
contract, not an environment-specific convention.

Spritz should define and own:

- the supported shared-host route surface:
  - `/`
  - `/api`
  - `/oauth2`
  - `/i`
  - `/c`
- the rule that one browser host uses one browser-facing auth gateway by
  default,
- the trusted identity-header contract from the auth gateway to `spritz-api`,
- the generic auth gateway configuration surface,
- the advanced-mode rules for multi-gateway deployments on one host,
- the validation guidance operators should use to prove the deployment is
  correct.

This keeps the product-level auth model stable even when different operators
use different ingress controllers or cluster providers.

## Deployment-Owned Contract

Deployment overlays should still own environment-specific details.

Examples:

- public hostname,
- TLS secret and certificate provisioning,
- ingress class or `GatewayClass`,
- namespace layout,
- service exposure details,
- cloud- or mesh-specific policy objects,
- external secret wiring,
- provider endpoints and client credentials.

The environment should choose how to realize the Spritz contract, but it should
not redefine the browser auth topology for the host unless it explicitly opts
into the advanced multi-gateway mode.

## Preferred Implementation Shape

The preferred operator-facing package is:

- one shared-host browser deployment mode in Spritz,
- one auth gateway service,
- one callback namespace,
- one route contract,
- one documented identity-header contract for API trust.

That package should be available through Spritz-owned deployment assets rather
than being rebuilt from scratch in each consumer environment.

At minimum, Spritz should provide generic templates and values for:

- the auth gateway deployment and service,
- the public route ownership for `/`, `/api`, `/oauth2`, `/i`, and `/c`,
- the forwarding contract for authenticated identity headers,
- the default callback path namespace,
- advanced-mode configuration for distinct callback paths when an operator
  explicitly chooses multiple gateway instances.

If Spritz supports more than one routing backend, such as classic Ingress and
Gateway API, both modes should implement the same route and auth contract
rather than inventing different browser-auth topologies.

## Why One Gateway Is The Default

One gateway per host gives:

- one callback owner
- one session cookie surface
- one redirect model
- simpler debugging
- fewer ways for background polling to disrupt foreground login

This is a better default than path-splitting multiple independent gateway
instances across `/`, `/api`, and `/i/*`.

## Advanced Topologies

Multiple auth gateway instances on one public host are not the default and
should be treated as an advanced deployment mode.

If an operator chooses that topology anyway, the deployment must isolate the
full auth boundary for each gateway instance, not only cookie names.

Required isolation:

- distinct cookie namespaces per gateway instance
- per-request CSRF and PKCE isolation
- distinct callback paths per gateway instance
- explicit route ownership so each callback path returns to the gateway that
  initiated the flow

Example advanced callback split:

- `/oauth2/ui/*` -> UI gateway
- `/oauth2/api/*` -> API gateway
- `/oauth2/instance/*` -> instance gateway

What is not sufficient on its own:

- different cookie names with one shared callback path

That still leaves callback ownership ambiguous.

## Deployment Guidance

For the shared-host browser deployment path, Spritz should model:

- one auth gateway instance per host
- one callback path namespace per host
- one trusted identity header contract from the gateway to the API
- one canonical route contract for `/`, `/api`, `/oauth2`, `/i`, and `/c`

If deployment overlays need additional internal proxies, those proxies should
not each become separate browser auth authorities for the same host unless the
operator explicitly opts into the advanced multi-gateway mode above.

What a deployment overlay should not do by default:

- split browser login across separate UI, API, and instance gateway instances,
- reuse one callback path across independent gateway instances,
- force product consumers to reassemble the host auth topology manually when a
  generic Spritz-owned version would work.

## Migration Target For Existing Custom Overlays

Some environments may already have custom shared-host routing that predates this
contract.

The target end state for those environments is:

1. collapse browser auth on the host behind one gateway instance,
2. keep one callback owner for the host,
3. keep downstream route fan-out behind that gateway,
4. reserve multi-gateway mode only for deployments that truly need it and can
   isolate callback ownership explicitly.

If a deployment cannot collapse to one gateway immediately, the minimum safe
intermediate state is:

- distinct callback paths per gateway instance,
- distinct cookie namespaces,
- explicit route ownership for each callback path,
- validation that background polling on one surface cannot break login on
  another.

## Validation

Operators should validate all of the following against the live deployment:

1. Start from an unauthenticated browser session.
2. Open `/`.
3. Confirm `/api/spritzes` or other background polling cannot break the login
   flow for `/`.
4. Complete the identity-provider login.
5. Confirm the callback is redeemed by the same gateway that initiated the
   flow.
6. Confirm the browser returns to a valid authenticated session without PKCE
   errors or redirect loops.

Useful negative test:

- leave unauthenticated background polling active on `/api/*` while logging in
  through `/`

If that scenario breaks login, the host still has competing browser auth flows.

## References

- [Portable Authentication and Account Architecture](2026-02-24-portable-authentication-and-account-architecture.md)
- [Simplest Spritz Deployment Specification](2026-02-24-simplest-spritz-deployment-spec.md)
