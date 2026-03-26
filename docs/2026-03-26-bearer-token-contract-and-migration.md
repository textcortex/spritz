---
date: 2026-03-26
author: Onur Solmaz <onur@textcortex.com>
title: Bearer Token Contract and Migration
tags: [spritz, auth, bearer, jwks, migration]
---

## Overview

This document defines the preferred bearer-token contract for Spritz and the
migration path from legacy upstream tokens.

The core decision is simple:

- Spritz should define one preferred bearer-token contract.
- Upstream systems should exchange their own sessions or tokens into a
  Spritz-scoped token.
- Spritz should validate that token directly with JWKS when possible.
- Introspection should remain available only as a bridge for legacy callers.

## Problem

Some clients can already present standard JWTs with the claims Spritz expects
for direct verification. Other clients still present legacy backend-issued JWTs
that do not match the preferred Spritz bearer contract.

If Spritz keeps learning new upstream token shapes, bearer auth becomes harder
to reason about, harder to test, and more deployment-specific than it should
be.

## Decision

The preferred long-term model is:

1. an upstream product or service authenticates the actor,
2. that upstream system exchanges its local session or token for a
   Spritz-scoped JWT,
3. the caller sends that Spritz token to Spritz,
4. Spritz validates it locally with JWKS.

Spritz should not depend on broad upstream product tokens as its normal bearer
contract.

## Preferred Token Contract

A Spritz-scoped bearer token should:

- be a JWT,
- be signed by a key that is published through JWKS,
- include a stable principal identifier in `sub`,
- include `iss`,
- include an audience that is specific to Spritz in `aud`,
- include normal time bounds such as `exp` and `iat`,
- include only the claims Spritz actually needs for authorization.

Optional claims such as principal `type`, `email`, `teams`, or `scopes` are
acceptable when the deployment needs them, but they should remain explicit and
minimal.

## Why This Model

This model is preferred because it gives Spritz:

- one clear bearer-token contract,
- local token validation for the normal path,
- tighter audience control,
- short-lived downstream tokens,
- less coupling to upstream product internals,
- easier support for multiple external clients.

## What Introspection Is For

Introspection is still useful, but it should be treated as a migration tool or
an exception path.

Good uses for introspection:

- legacy callers that still send an upstream token shape,
- deployments where direct JWT validation is not possible yet,
- temporary compatibility while clients migrate to the Spritz token contract.

Introspection should not be the preferred steady-state path for external UI or
service clients once a Spritz-scoped JWT can be issued.

## Migration Plan

### Phase 1: Compatibility Bridge

- Keep bearer introspection available.
- Allow JWKS validation to fall back to introspection when a legacy token does
  not satisfy the preferred Spritz JWT contract.
- Add regression coverage for:
  - a valid Spritz JWT,
  - a legacy token that succeeds only through introspection,
  - an invalid token that fails both paths.

### Phase 2: Token Exchange

- Add or use an upstream exchange path that returns a Spritz-scoped JWT.
- External UIs should call that exchange before talking to Spritz.
- Shared service flows should mint the same Spritz token shape instead of
  reusing a product-local token format.

### Phase 3: Convergence

- Migrate callers away from legacy upstream bearer tokens.
- Keep introspection only for the callers that still truly require it.
- Disable fallback in deployments that no longer need legacy compatibility.

## Security Expectations

The secure version of this model should follow these rules:

- exchanged Spritz tokens should be short-lived,
- the `aud` claim should be specific to Spritz,
- claims should be minimal and purpose-built for Spritz,
- signing keys should be rotated through normal JWKS practices,
- upstream products should validate the actor before issuing a Spritz token,
- Spritz should not accept a broad upstream token when a narrower Spritz token
  can be used instead.

## Validation

Validation for a deployment following this plan should include:

1. a valid Spritz-scoped JWT succeeds through JWKS alone,
2. a wrong issuer or audience is rejected,
3. a legacy token succeeds only when introspection fallback is enabled,
4. ACP and other websocket-backed flows accept the same Spritz-scoped token
   model,
5. disabling fallback after migration does not break supported clients.

## Summary

The preferred bearer-auth design is:

- one Spritz-defined token contract,
- upstream systems exchange into that contract,
- JWKS for the normal validation path,
- introspection only as a bridge for legacy token shapes.
