# OpenClaw in Spritz

This file is the single source of truth for OpenClaw-related behavior in this repository.

## Scope

Spritz supports running OpenClaw as a per-devbox application surface.
Each devbox runs its own OpenClaw process and is opened through its own `/w/{name}` route.

## Where OpenClaw Lives in This Repo

- Image and runtime wrapper:
  - `images/examples/openclaw/Dockerfile`
  - `images/examples/openclaw/entrypoint.sh`
  - `images/examples/openclaw/README.md`
- UI default preset label/image:
  - `ui/public/app.js`
- Helm surface for custom presets:
  - `helm/spritz/values.yaml` (`ui.presets`)
  - `helm/spritz/templates/ui-deployment.yaml`

## Runtime Contract (Example Image)

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
5. Auto-starts OpenClaw when command is default (`sleep infinity`), unless `OPENCLAW_AUTO_START=false`.

Key implication: direct `/w/{name}` access with `bind=lan` expects real gateway auth.

## Auth Modes and What to Use

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
- No bypass path to the workspace service.

### `none` (not for LAN exposure)

- `auth.mode=none` with `bind=lan` is rejected by OpenClaw runtime checks.
- Use `none` only for trusted local/loopback scenarios.

## Tokenless Implementation Pattern (Recommended)

If the target is "no token prompt in Control UI":

1. Put workspace routes behind an identity-aware proxy.
2. Switch OpenClaw auth to `trusted-proxy`.
3. Configure `trustedProxies` to only the proxy source IPs/CIDRs.
4. Configure `trustedProxy.userHeader` to the forwarded authenticated identity.
5. Enforce network policy so workspace pods are not reachable except through ingress/proxy.

Do not disable auth globally to get tokenless behavior.

## Example Preset Snippet

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

## Fast Troubleshooting

- `Refusing to bind gateway to lan without auth`:
  - invalid config (typically `auth.mode=none` with `bind=lan`).
- `gateway token missing` in Control UI:
  - gateway is in token mode but UI has no token.
  - either provide token or move to trusted-proxy model.
- `origin not allowed`:
  - add the dashboard origin to `gateway.controlUi.allowedOrigins`.

## Related Docs

- `images/examples/openclaw/README.md`
- `docs/2026-02-24-simplest-spritz-deployment-spec.md`
- `docs/2026-02-24-portable-authentication-and-account-architecture.md`
