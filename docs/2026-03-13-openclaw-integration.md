---
date: 2026-03-13
author: Onur Solmaz <onur@textcortex.com>
title: OpenClaw Integration
tags: [openclaw, acp, integration]
---

# OpenClaw in Spritz

This document is the single source of truth for OpenClaw-related behavior in this repository.

## Scope

Spritz supports running OpenClaw as a per-devbox application surface.
Each devbox runs its own OpenClaw process and is opened through its own `/w/{name}` route.
When OpenClaw is used as an ACP backend inside Spritz, it should also expose ACP on
the reserved internal port `2529`.

## Where OpenClaw lives in this repo

- Image and runtime wrapper:
  - `images/examples/openclaw/Dockerfile`
  - `images/examples/openclaw/entrypoint.sh`
  - `images/examples/openclaw/README.md`
- UI default preset label/image:
  - `ui/public/app.js`
- Helm surface for custom presets:
  - `helm/spritz/values.yaml` (`ui.presets`)
  - `helm/spritz/templates/ui-deployment.yaml`

## Runtime contract (example image)

The OpenClaw example entrypoint does the following:

1. Resolves config path (`OPENCLAW_CONFIG_PATH` / `OPENCLAW_CONFIG_DIR`).
2. Writes config from one of:
   - `OPENCLAW_CONFIG_JSON`
   - `OPENCLAW_CONFIG_B64`
   - `OPENCLAW_CONFIG_FILE`
3. Sets gateway fields:
   - `gateway.mode` from `OPENCLAW_GATEWAY_MODE` (default `local`)
   - `gateway.port` from `OPENCLAW_GATEWAY_PORT` (default `8080`)
   - `gateway.bind` from `OPENCLAW_GATEWAY_BIND` (default `lan`)
4. Ensures `OPENCLAW_GATEWAY_TOKEN` exists (uses provided token or generates one).
5. Writes the gateway token to a local token file for ACP adapter use.
6. Starts an image-owned ACP adapter on `0.0.0.0:2529` unless `OPENCLAW_ACP_ENABLED=false`.
7. Exposes:
   - WebSocket ACP on `/`
   - `GET /healthz`
   - `GET /.well-known/spritz-acp`
8. When gateway auth mode is `trusted-proxy`, automatically trusts loopback for the internal adapter,
   injects the required trusted-proxy headers on the adapter's upstream gateway hop, and rewrites the
   upstream gateway `connect` handshake into a Control UI operator session without device identity.
9. Auto-starts OpenClaw when command is default (`sleep infinity`), unless `OPENCLAW_AUTO_START=false`.

Key implication: direct `/w/{name}` access with `bind=lan` expects real gateway auth.

## ACP contract in Spritz

Spritz treats ACP as backend-agnostic.

For OpenClaw to appear in the Spritz ACP chat UI, the instance must answer ACP on:

- port `2529`
- WebSocket path `/`

Spritz will then:

- discover the agent from the operator by sending ACP `initialize`
- surface it in `status.acp`
- proxy browser ACP traffic through `spritz-api`

This ACP path is separate from OpenClaw's dashboard and gateway UI.

Today the example image satisfies that contract with a long-lived ACP adapter:

- one long-lived Node ACP server inside the image listens on `2529`
- the adapter talks to the local OpenClaw gateway over loopback WebSocket
- ACP WebSocket clients connect to that long-lived adapter instead of spawning a fresh runtime
- the adapter also exposes cheap HTTP health and metadata endpoints for Spritz operator discovery
- if gateway auth mode is `trusted-proxy`, the adapter uses a loopback-only header injector so the
  internal ACP hop satisfies the same trusted-proxy contract as the browser route
- in trusted-proxy mode, the adapter impersonates a Control UI-style operator profile without
  device identity so OpenClaw does not force pairing for the internal ACP client

This keeps the Spritz side backend-agnostic while OpenClaw remains free to add native socket ACP
later.

## Auth modes and what to use

### `token` (default-safe for current direct routing)

- Works with `bind=lan`.
- No startup crash.
- Required when route is direct to the devbox without trusted proxy auth headers.

### `trusted-proxy` (tokenless UX, production-ready if wired correctly)

Use this when you want users not to paste gateway tokens manually.

Required OpenClaw config:

- `gateway.auth.mode: "trusted-proxy"`
- `gateway.auth.trustedProxy.userHeader: "<header-name>"`
- `gateway.trustedProxies: ["<proxy-ip-or-cidr>", ...]`

Required platform behavior:

- All `/w/{name}` traffic must pass through an auth proxy.
- Proxy must authenticate users and overwrite identity headers.
- No bypass path to the instance service.

### `none` (not for LAN exposure)

- `auth.mode=none` with `bind=lan` is rejected by OpenClaw runtime checks.
- Use `none` only for trusted local/loopback scenarios.

## Tokenless implementation pattern (recommended)

If the target is "no token prompt in Control UI":

1. Put instance routes behind an identity-aware proxy.
2. Switch OpenClaw auth to `trusted-proxy`.
3. Configure `trustedProxies` to only the proxy source IPs/CIDRs.
4. Configure `trustedProxy.userHeader` to the forwarded authenticated identity.
5. Enforce network policy so instance pods are not reachable except through ingress/proxy.

Do not disable auth globally to get tokenless behavior.

## Example preset snippet

Use `ui.presets` to ship an OpenClaw preset with config injected through env:

```yaml
ui:
  presets:
    - name: OpenClaw Devbox
      image: spritz-openclaw:latest
      description: OpenClaw preinstalled
      env:
        - name: OPENCLAW_CONFIG_JSON
          value: >-
            {"gateway":{"mode":"local","bind":"lan","auth":{"mode":"token"}}}
```

For trusted-proxy deployments, replace the `auth` block accordingly.

## Fast troubleshooting

- `Refusing to bind gateway to lan without auth`:
  - invalid config (typically `auth.mode=none` with `bind=lan`).
- `gateway token missing` in Control UI:
  - gateway is in token mode but UI has no token.
  - either provide token or move to trusted-proxy model.
- `origin not allowed`:
  - add the dashboard origin to `gateway.controlUi.allowedOrigins`.

## Related docs

- [OpenClaw example runtime README](../images/examples/openclaw/README.md)
- [Simplest Spritz Deployment Spec](2026-02-24-simplest-spritz-deployment-spec.md)
- [Portable Authentication and Account Architecture](2026-02-24-portable-authentication-and-account-architecture.md)
