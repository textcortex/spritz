---
date: 2026-02-24
author: Spritz Maintainers <user@example.com>
title: Portable Authentication and Account Architecture
tags: [spritz, auth, oidc, deployment, architecture]
---

## Overview

This document defines the default, portable authentication and account model for Spritz.

The objective is a deployment model that works across arbitrary environments without requiring a
specific edge provider, SaaS login product, or organization-specific identity stack.

## Goals

- Keep Spritz portable across cloud providers and ingress stacks.
- Support standard OIDC login for browser users.
- Avoid hard dependency on any single external auth vendor.
- Keep the UI and API auth contract explicit and predictable.
- Keep account identity stable across sessions and devices.

## Non-goals

- Building a password database inside Spritz.
- Replacing enterprise IdP features (MFA, SSO policy, SCIM).
- Supporting provider-specific behavior in core Spritz code.

## Design Principles

- Use open standards first (OIDC, OAuth2, JWT).
- Keep authentication at the gateway boundary.
- Keep authorization checks in the API.
- Avoid browser token plumbing when a gateway session can be used.
- Make provider-specific wiring an overlay concern, not a Spritz core concern.

## Reference Architecture (Default)

Default architecture:

1. User accesses Spritz host (`https://spritz.example.com`).
2. In-cluster auth gateway (for example `oauth2-proxy`) handles OIDC login with an IdP.
3. Gateway establishes a secure session cookie for the browser.
4. Gateway injects authenticated identity headers to API-bound requests.
5. Spritz API trusts those headers (`api.auth.mode=header`) and derives principal identity.
6. UI uses same-origin requests to `/api` and does not manage login tokens.

This model avoids custom `/login` application routes in Spritz and avoids client-side refresh-token
logic as a required dependency for normal UI operation.

## Why This Is the Default

Compared to a browser-token-centric model, gateway-managed auth is simpler and safer for generic
self-hosting:

- fewer moving parts in UI,
- fewer token storage risks,
- cleaner portability across environments,
- easier production hardening with existing ingress/auth patterns.

## Components

Required components:

- OIDC provider (any standards-compliant provider).
- Auth gateway in-cluster (for example `oauth2-proxy`).
- Ingress/Gateway route layer that can enforce auth and forward identity headers.
- Spritz UI and Spritz API.

Optional components:

- Group sync or SCIM in external IdP.
- External policy engine for advanced authorization.

## Identity and Account Model

### Identity Source of Truth

Identity is external (IdP). Spritz does not own password credentials.

### Principal Identity

Spritz principal identity is derived from gateway-provided claims:

- stable user ID (subject claim, typically `sub`) -> required,
- email -> optional but recommended,
- teams/groups -> optional.

### Local Account Semantics

Spritz account behavior is "just-in-time":

- a user is considered known when first authenticated request arrives,
- ownership labels and access checks use stable principal ID,
- no mandatory separate account provisioning database is required.

## Authentication Modes in Spritz

Spritz API supports these modes:

- `none`: no auth (local/dev only).
- `header`: trust identity headers from gateway.
- `bearer`: validate JWT/introspect bearer token.
- `auto`: try header first, then bearer fallback.

### Default Mode for Arbitrary Deployments

Use:

- `api.auth.mode=header`
- `ui.auth.mode=none`

This keeps login/session management in the gateway and keeps UI behavior deterministic.

## UI/Auth Contract (Default)

For gateway-managed auth, set UI auth to non-token mode:

```yaml
ui:
  auth:
    mode: none
    tokenStorageKeys: []
    loginUrl: ""
    redirectOnUnauthorized: false
    refresh:
      enabled: false
      url: ""
      tokenStorageKeys: []
```

Expected behavior:

- browser is redirected to IdP by gateway when unauthenticated,
- Spritz UI does not build `/login?next=...` redirects,
- API calls succeed when gateway session is valid.

## API/Auth Contract (Default)

For gateway-injected identity:

```yaml
api:
  auth:
    mode: header
    headerId: X-Spritz-User-Id
    headerEmail: X-Spritz-User-Email
    headerTeams: X-Spritz-User-Teams
```

Header requirements:

- `headerId` must map to a stable principal identifier.
- Header trust boundary must be enforced at ingress (clients must not be able to spoof headers).

## Gateway Responsibilities

The gateway layer must:

- enforce authentication before forwarding to protected routes,
- set identity headers for authenticated requests,
- strip inbound identity headers from clients,
- set secure, HTTP-only, same-site cookies,
- preserve host/proto headers required by upstream routing.

## Route Design

Recommended route structure:

- `/` -> Spritz UI
- `/api` -> Spritz API
- `/auth/*` or `/oauth2/*` -> auth gateway endpoints

All API routes should remain under `/api/*`.

## Authorization Model

Minimum authorization behavior:

- authenticated principal can operate only on resources they own unless admin policy grants broader access,
- admin access can be granted via configured IDs/teams,
- ownership labels must use stable principal ID, not mutable display attributes.

## Security Requirements

- HTTPS only in all environments beyond local dev.
- Secure cookies (`Secure`, `HttpOnly`, `SameSite=Lax` or stricter).
- Header spoofing prevention at ingress.
- No secrets or tokens in URL query for normal UI flows.
- No persistence of access tokens in local storage for default gateway mode.
- Audit logging should include principal ID and request outcome.

## Operational Requirements

- Auth gateway health checks included in deployment.
- IdP client credentials stored in cluster secret manager, not in static values files.
- Explicit rollout checklists for certificate, DNS, and callback URL changes.
- Documented break-glass procedure for auth outage (for example temporary allowlist for admins).

## Provider-Agnostic OIDC Checklist

For any OIDC provider, configure:

- issuer URL,
- client ID,
- client secret,
- redirect URI on Spritz host,
- allowed callback paths in gateway config,
- scopes (at minimum `openid`, plus `email`/`profile` if needed),
- claim mapping for subject/email/groups.

## Optional Bearer/JWT Mode

Bearer mode is supported for non-browser clients or environments where gateway header auth is not
preferred.

Use JWT validation when possible (JWKS) and introspection only when required.

If bearer mode is enabled for UI traffic, token lifecycle (acquisition, refresh, storage) must be
explicitly designed and documented for that deployment.

## Migration Guidance (From Path-Mounted Login Flows)

If moving from legacy UI-driven `/login` behavior:

1. move to host-based routing (`/` and `/api`),
2. enable gateway-managed OIDC,
3. switch API auth to `header`,
4. set UI auth to `none` and disable refresh/login URL fields,
5. validate redirects and identity headers end-to-end,
6. remove legacy path-specific auth rewrites.

## Validation Checklist

Validation for a fresh deployment:

- unauthenticated browser request to `/` triggers gateway login,
- post-login browser lands on Spritz UI root,
- authenticated `GET /api/healthz` returns `200`,
- authenticated `GET /api/spritzes` returns data for principal,
- principal ownership labels match stable subject ID,
- spoofed identity headers from client are ignored/stripped,
- logout invalidates gateway session and blocks API access.

## Configuration Surfaces

In Spritz core:

- Helm values define generic auth knobs (`api.auth.*`, `ui.auth.*`).
- No environment-specific domains, tenants, or issuer values in repository defaults.

In environment overlays (outside Spritz core):

- real hostnames,
- real OIDC issuer/client settings,
- secrets and policy bindings,
- ingress-specific auth wiring.

## Future Work

- provide a first-class "OIDC gateway profile" example manifest set in Spritz,
- add an integration test profile for header-auth mode,
- add explicit docs for non-browser service account auth patterns.

