---
date: 2026-03-30
author: Onur Solmaz <onur@textcortex.com>
title: Browser WebSocket Auth Model
tags: [spritz, auth, websocket, browser, tickets, architecture]
---

## Overview

This document defines the preferred browser authentication model for Spritz
WebSocket surfaces.

It covers two deployment shapes:

- the native Spritz browser surface on the canonical Spritz host
- external browser UIs that call Spritz from a different host

It also defines how Spritz should authenticate browser WebSocket upgrades
without relying on reusable bearer tokens in URL query parameters.

## TL;DR

- Native Spritz browser flows should keep using browser session cookies and
  trusted proxy identity on the canonical Spritz host.
- External browser UIs should keep using bearer tokens for normal HTTP API
  calls.
- External browser UIs should not send reusable bearer tokens in WebSocket
  URLs.
- Instead, they should request a one-time, short-lived connect ticket and use
  that ticket only for the WebSocket upgrade.
- Query-parameter bearer tokens may exist as a temporary compatibility path in
  some deployments, but they should not be treated as the long-term Spritz
  contract.

## Problem

HTTP and WebSocket requests have different browser constraints.

For normal HTTP requests, an external UI can send:

- `Authorization: Bearer <token>`

For browser WebSocket upgrades, the browser cannot reliably offer the same
custom header model that HTTP clients use.

This often pushes products toward a weak fallback:

- append a reusable bearer token to the WebSocket URL

That creates several problems:

- tokens appear in URLs and are easier to leak through logs, screenshots,
  copied links, browser history, and monitoring systems
- proxies and gateways may not treat query-parameter auth as a supported
  authentication path
- browser auth rules become coupled to URL construction instead of a stable
  product contract
- the same reusable token is often broader-lived and broader-scoped than a
  single WebSocket connect operation needs

Spritz should support a better browser contract.

## Goals

- Keep the native Spritz browser surface simple on the canonical host.
- Support external browser UIs on arbitrary hosts without forcing a shared
  cookie domain.
- Give browser WebSocket upgrades a first-class auth contract.
- Avoid reusable bearer tokens in WebSocket URLs.
- Keep the model portable across ingress controllers, proxies, and hosting
  setups.

## Non-goals

- Forcing every deployment to use one canonical public domain.
- Requiring external UIs to behave like first-party hosted Spritz pages.
- Making browser WebSocket auth depend on query parameters.
- Replacing bearer-token HTTP auth for external clients.

## Client Types

Spritz should treat these as different browser client shapes.

### Native browser UI

Example:

- `https://spritz.example.com`

This is the canonical Spritz host. It should use:

- browser session cookies
- one browser-facing auth gateway for the host
- trusted proxy or trusted identity headers behind that gateway
- same-host WebSocket upgrades where the browser session already authenticates
  the upgrade request

### External browser UI

Example:

- `https://console.example.com`

This is a browser client of Spritz, not a second native Spritz host. It should
use:

- absolute Spritz API URLs
- bearer tokens for normal HTTP calls
- one-time connect tickets for WebSocket upgrades

## Design Decision

Spritz should support two browser auth modes for WebSocket surfaces.

### Mode 1: Hosted browser session

When the browser is already on the canonical Spritz host:

- the browser session cookie authenticates the WebSocket upgrade
- the auth gateway and proxy chain remain the browser auth boundary
- the Spritz UI does not need to mint or attach a special client-managed
  WebSocket token

This is the preferred model for:

- `/`
- `/c/*`
- `/i/*` when the deployment uses trusted proxy instance access

### Mode 2: External bearer-auth browser client

When a different browser UI talks to Spritz by absolute URL:

- the client keeps using bearer tokens for HTTP requests
- the client requests a one-time connect ticket before opening a WebSocket
- the WebSocket handshake carries that ticket instead of a reusable bearer
  token in the URL

This is the preferred model for:

- ACP chat connections from external UIs
- terminal sessions from external UIs
- future browser WebSocket features exposed to non-native UIs

## Connect Ticket Model

The connect ticket is a dedicated authorization artifact for one WebSocket
upgrade.

It should be:

- opaque
- one-time use
- short-lived
- tightly scoped

The ticket should not be a broad browser session replacement. Its only job is
to authorize one WebSocket connection attempt.

### Ticket issuance

Before opening the socket, the external browser UI makes a normal authenticated
HTTP request to Spritz.

That request should:

- authenticate with the existing bearer-token HTTP model
- identify the intended WebSocket target
- return one ticket for one connection attempt

Spritz may expose this as:

- a generic connect-ticket endpoint with a target descriptor, or
- route-specific endpoints for ACP, terminal, or future WebSocket surfaces

The product contract matters more than the exact endpoint shape.

### Ticket scope

Each ticket should be bound to:

- one authenticated principal
- one target surface
- one target resource, such as one conversation or one terminal session
- one expiration time

Spritz should also support binding the ticket to:

- one allowed origin
- one instance or spritz name
- one protocol version

### Ticket lifetime

The ticket should expire quickly.

Recommended window:

- 15 to 60 seconds

If a reconnect is needed later, the client should mint a new ticket.

### Ticket format

The preferred format is:

- an opaque random value

Spritz should store or validate it server-side in a way that supports:

- one-time consumption
- atomic invalidation on successful use
- fast expiration

This is preferable to a longer-lived reusable bearer token, even if the ticket
is internally represented as a signed token.

## WebSocket Handshake Contract

The ticket should be sent during the WebSocket handshake through a handshake
field that browsers can set without using the URL query string.

The preferred contract is:

- WebSocket subprotocol-based ticket transport

Example shape:

- application protocol: `acp.v1`
- auth transport: `spritz-ticket.<opaque-value>`

The server should:

1. parse the offered protocols
2. validate the ticket
3. consume the ticket atomically
4. accept the WebSocket
5. reply with the actual application protocol

Spritz should not require the server to echo the ticket itself back to the
client.

## Security Requirements

The connect ticket path should meet all of the following requirements.

### Required

- one-time use
- short expiration
- authorization check before issuance
- authorization check again during connect if resource state changed
- origin validation during the WebSocket upgrade
- rate limiting for ticket issuance and failed connect attempts
- no ticket in the URL
- no logging of raw ticket values

### Strongly recommended

- bind tickets to one origin when the deployment can determine an expected
  origin
- store only a hash of the ticket value at rest
- include explicit target type in the ticket scope
- invalidate outstanding tickets when the parent session or permission is
  revoked

## Current Compatibility Behavior

Some Spritz deployments may currently allow cross-host browser WebSocket
connections by appending a bearer token to the URL query string.

That behavior may remain temporarily for compatibility, but Spritz should not
extend it as the main browser contract.

Spritz should instead move toward:

- cookie-authenticated hosted browser upgrades for the native Spritz host
- connect-ticket upgrades for external bearer-auth browser clients

## Routing and Proxy Implications

This model keeps the routing story clear.

### Hosted browser session path

For the canonical Spritz host:

- the browser-facing auth gateway remains the entry point
- cookies authenticate the WebSocket upgrade
- hosted UI routes keep the same browser auth boundary as the rest of the host

### External bearer-auth client path

For external browser UIs:

- the WebSocket upgrade path must reach a Spritz-owned validator that can read
  the handshake ticket
- intermediate proxies must preserve the chosen handshake field
- deployments must not assume that a cookie-only gateway can validate this
  path on behalf of the external client

This means the deployment contract should make the ticket-authenticated
WebSocket path explicit rather than relying on an incidental proxy behavior.

## Rollout Plan

### Phase 1: Establish the product contract

- document hosted-browser and external-browser WebSocket auth as separate
  supported modes
- document query-parameter bearer tokens as compatibility behavior only
- define the target security requirements for connect tickets

### Phase 2: Add connect-ticket support

- add a ticket issuance API
- add server-side ticket validation and one-time consumption
- add origin checks and rate limits
- support handshake-based ticket transport for ACP and terminal WebSockets

### Phase 3: Move external browser UIs

- update external UIs to mint connect tickets before socket creation
- remove reusable bearer tokens from WebSocket URLs in UI code and tests
- validate reconnect flows that mint fresh tickets automatically

### Phase 4: Retire URL-token WebSocket auth

- disable query-parameter bearer token auth for browser WebSocket upgrades
- keep explicit deployment guidance for hosted cookie mode and external ticket
  mode

## Validation

Validate all of the following against a live deployment.

### Hosted browser surface

1. Open the canonical Spritz host in a fresh browser session.
2. Complete login through the browser auth gateway.
3. Confirm chat and terminal WebSockets connect without client-managed URL
   tokens.
4. Confirm instance routes behind trusted proxy continue to work.

### External browser UI

1. Authenticate the external UI through its normal product flow.
2. Call Spritz HTTP APIs with bearer auth.
3. Mint a connect ticket for the target WebSocket surface.
4. Open the WebSocket without placing a reusable bearer token in the URL.
5. Confirm the connection succeeds.
6. Confirm reconnect mints a fresh ticket.
7. Confirm replaying a used or expired ticket fails.

### Negative checks

1. Invalid or expired ticket fails the WebSocket upgrade.
2. Ticket for one conversation does not open another conversation.
3. Ticket from one origin is rejected when replayed from another origin, when
   origin binding is enabled.
4. Query-parameter token auth is not required for the supported client path.

## References

- [Portable Authentication and Account Architecture](2026-02-24-portable-authentication-and-account-architecture.md)
- [Shared-Host Auth Gateway Architecture](2026-03-20-shared-host-auth-gateway-architecture.md)
- [Native Browser and External UI Auth Model](2026-03-26-native-browser-and-external-ui-auth-model.md)
