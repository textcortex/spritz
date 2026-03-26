---
date: 2026-03-26
author: Onur Solmaz <onur@textcortex.com>
title: Native Browser and External UI Auth Model
tags: [spritz, auth, cookies, bearer, ui, architecture]
---

## Overview

This document defines the preferred authentication model when Spritz serves:

- its own native browser UI, and
- one or more external UI clients on different hosts

The key rule is simple:

- native Spritz browser flows should use gateway-managed cookies
- external UI clients should use bearer tokens

This keeps the browser model simple for the canonical Spritz host and avoids
cross-host cookie coupling for external clients.

## Goals

- Keep the native Spritz UI simple on its own host.
- Support external UI clients without requiring shared browser cookies.
- Avoid making multiple domains behave like one browser session surface.
- Keep Spritz usable as both a product UI and a backend platform.

## Non-goals

- Sharing session cookies across unrelated hosts.
- Making every UI host a first-class Spritz browser host.
- Requiring external clients to reuse the native browser login flow.

## Client Types

Spritz should support two primary client types.

### Native browser UI

The canonical Spritz host, for example `https://spritz.example.com`, uses:

- gateway-managed login,
- gateway-managed session cookies,
- same-origin browser requests to `/api`,
- header auth or auto auth in the API.

This is the default browser model described in
[Portable Authentication and Account Architecture](2026-02-24-portable-authentication-and-account-architecture.md).

### External UI client

An external UI, for example `https://console.example.com`, uses:

- full Spritz API URLs,
- bearer tokens on API and ACP requests,
- no dependency on Spritz host cookies.

The external UI is a client of Spritz, not a second native Spritz host.

## Preferred Architecture

Recommended topology:

1. Keep one canonical Spritz browser host.
2. Keep the native browser login flow on that host.
3. Let external UIs call Spritz by absolute URL.
4. Authenticate those external UI requests with bearer tokens.

This produces a clear split:

- cookies are for the native Spritz browser surface
- tokens are for non-native clients

## Native Browser Flow

Recommended native browser flow:

1. Browser opens the canonical Spritz host.
2. Auth gateway handles login and callback.
3. Auth gateway establishes a secure browser session cookie.
4. Browser calls `/api` on the same host.
5. Gateway forwards identity headers to `spritz-api`, or the API falls back to
   bearer validation when configured for `auto`.

This is the clean default for the canonical host because it avoids browser
token storage as a required dependency.

## External UI Flow

Recommended external UI flow:

1. User authenticates to the external product UI.
2. The external product obtains or exchanges a token for Spritz.
3. The external UI calls Spritz using absolute URLs such as:
   - `https://spritz.example.com/api/...`
4. The external UI sends `Authorization: Bearer <token>`.
5. Spritz validates that token in bearer or auto auth mode.

The external UI should not depend on:

- Spritz session cookies,
- Spritz callback paths,
- same-origin `/api` assumptions,
- cross-host browser session sharing.

## Preferred Token Shape

The preferred long-term model for external UI access is:

- an upstream product session,
- exchanged for a short-lived Spritz-scoped bearer token,
- with an audience that is specific to Spritz,
- and only the claims Spritz needs.

This is better than reusing a broad product token directly because it gives:

- tighter audience control,
- shorter token lifetime,
- cleaner separation between the upstream product and Spritz,
- easier future support for multiple external clients.

## API Expectations

To support this model, Spritz should treat bearer auth as a first-class client
path, not only as a fallback for special cases.

That means:

- API routes should accept valid bearer tokens when configured for bearer or
  auto auth.
- ACP routes should also accept the same authenticated client model.
- UI code should allow an explicit Spritz API base URL instead of assuming the
  UI and API share one origin.

## What To Avoid

Avoid these patterns for external UI integration:

- sharing native Spritz cookies across different hosts,
- making multiple hosts behave like one browser auth boundary,
- forcing external clients through the native browser callback flow,
- coupling Spritz API access to one exact UI origin.

These patterns are fragile and usually turn cross-host behavior into a browser
cookie problem instead of a normal API client problem.

## Validation

For a deployment that supports both client types, validate all of the
following:

1. Native browser login works on the canonical Spritz host with cookies only.
2. Native browser API calls succeed without client-managed bearer tokens.
3. External UI requests succeed with bearer tokens and absolute Spritz URLs.
4. External UI requests do not require Spritz cookies to be present.
5. Spritz API and ACP reject invalid or expired bearer tokens consistently.

## Summary

The preferred mixed-client model is:

- one canonical Spritz browser host with cookie-based login,
- many possible external UI clients using bearer tokens,
- no attempt to make unrelated hosts share one browser session boundary.

This keeps Spritz simple as a browser product and clean as a platform backend.
