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

If deployment overlays need additional internal proxies, those proxies should
not each become separate browser auth authorities for the same host unless the
operator explicitly opts into the advanced multi-gateway mode above.

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
