# OpenClaw Example Image

This image provides a generic Spritz devbox with `openclaw` preinstalled.

For the repository-level OpenClaw integration model (auth modes, routing expectations,
and production tokenless pattern), see [OpenClaw Integration](../../../docs/2026-03-13-openclaw-integration.md).

It intentionally stays environment-agnostic:

- no organization-specific domains
- no environment-specific IDs
- no bundled secrets

## Build

Run from the `images/` directory so shared scripts are available in build context:

```bash
cd images
docker build -f examples/openclaw/Dockerfile -t spritz-openclaw:latest .
```

Optional build args:

- `OPENCLAW_VERSION` (default: `2026.4.2`)

Example:

```bash
docker build \
  -f examples/openclaw/Dockerfile \
  -t spritz-openclaw:latest \
  --build-arg OPENCLAW_VERSION=2026.4.2 \
  .
```

## Runtime

The image uses a small OpenClaw wrapper entrypoint and then calls
`examples/base/entrypoint.sh`.

By default, when the container command is the image default (`sleep infinity`),
it auto-starts the OpenClaw gateway on port `8080` with a LAN bind so Spritz `Open` can render
OpenClaw UI immediately.

The image also starts an internal ACP adapter by default:

- listen address: `0.0.0.0:2529`
- WebSocket path: `/`
- backend: one long-lived ACP server process that talks to the local gateway over loopback
- session mapping: ACP session IDs are deterministically mapped onto normal OpenClaw
  agent-scoped gateway session keys so reconnects work without ACP clients knowing OpenClaw internals

This keeps the Spritz ACP contract stable even though OpenClaw's native ACP support is currently
stdio-only.

To disable auto-start and keep shell-only behavior, set:

- `OPENCLAW_AUTO_START=false`

Auto-start related runtime overrides:

- `OPENCLAW_GATEWAY_PORT` (default: `8080`)
- `OPENCLAW_GATEWAY_MODE` (default: `local`)
- `OPENCLAW_GATEWAY_BIND` (default: `lan`; set `loopback` for local-only)
- `OPENCLAW_GATEWAY_TOKEN` (optional; auto-generated if omitted)
- `OPENCLAW_ACP_ENABLED` (default: `true`)
- `OPENCLAW_ACP_BIND` (default: `0.0.0.0`)
- `OPENCLAW_ACP_PORT` (default: `2529`)
- `OPENCLAW_ACP_PATH` (default: `/`)
- `SPRITZ_OPENCLAW_ACP_GATEWAY_HOST` (optional; defaults to `127.0.0.1`)
- `SPRITZ_OPENCLAW_ACP_GATEWAY_URL` (optional; overrides the computed adapter upstream target)
- `SPRITZ_OPENCLAW_ACP_GATEWAY_HEADERS_JSON` (optional; JSON object of headers injected into the adapter's upstream gateway connection)
- `SPRITZ_OPENCLAW_ACP_TRUSTED_PROXY_USER` (optional; default internal trusted-proxy user identity)
- `SPRITZ_OPENCLAW_ACP_TRUSTED_PROXY_EMAIL` (optional; default internal trusted-proxy email identity)
- `SPRITZ_OPENCLAW_ACP_FALLBACK_AGENT_ID` (default: `main`; agent id used when the adapter maps ACP UUID session IDs onto OpenClaw gateway session keys)
- `SPRITZ_OPENCLAW_ACP_FALLBACK_SESSION_PREFIX` (default: `spritz-acp`; session-key namespace used for ACP-managed gateway transcripts)
- `SPRITZ_OPENCLAW_ACP_ALLOW_INSECURE_PRIVATE_WS` (default: `0`; only needed when overriding the adapter target away from loopback onto a trusted private-network `ws://` endpoint)

When the OpenClaw gateway itself is configured with `gateway.auth.mode="trusted-proxy"`, the
entrypoint automatically:

- appends `127.0.0.1` and `::1` to `gateway.trustedProxies`
- derives a header set for the internal ACP adapter
- routes the adapter through a loopback-only header-injecting WebSocket proxy
- rewrites the upstream gateway `connect` handshake to the Control UI operator profile without a
  device identity, so the adapter does not trigger device pairing

This keeps `/i/{name}` tokenless for browser users while allowing the internal ACP adapter to
authenticate cleanly without using pod-IP workarounds.

## Generic Config Support

OpenClaw config can be supplied at runtime without rebuilding the image:

- `OPENCLAW_CONFIG_JSON`: raw JSON content for `openclaw.json`
- `OPENCLAW_CONFIG_B64`: base64-encoded JSON content
- `OPENCLAW_CONFIG_FILE`: path to an existing config file in the container
- `OPENCLAW_CONFIG_DIR` (optional): config directory (default: `${HOME}/.openclaw`)
- `OPENCLAW_CONFIG_PATH` (optional): config file path (default: `${OPENCLAW_CONFIG_DIR}/openclaw.json`)

If none are provided and no config exists, the image writes a portable default
config that enables the bundled browser and OpenClaw's generic message
acknowledgement UX:

- `messages.ackReaction`: eye reaction
- `messages.ackReactionScope`: `group-all`
- `messages.removeAckAfterReply`: `true`
- `messages.statusReactions.enabled`: `true`
- all default status-reaction states use the same eye reaction

These defaults are provider-neutral OpenClaw settings. Provider credentials and
provider-specific channel IDs are still supplied by deployment or installation
configuration.

When this image is used behind a Spritz shared channel gateway, do not enable
OpenClaw's direct Slack, Discord, Teams, or similar provider-channel tools. The
runtime should return text over ACP, and the Spritz gateway should own provider
delivery and automatic acknowledgement reactions.

The default image config does not include provider-action MCP tools for reacting
to Slack messages. Those tools require a separate action policy and should be
added only when the runtime is intentionally allowed to perform explicit
provider actions.

## Spritz Open Integration

Use this image as a per-devbox application surface in Spritz:

- point the Spritz `Open` action to the OpenClaw UI running inside that specific devbox
- keep access checks tied to that devbox owner/session

Do not expose a single global/shared OpenClaw dashboard for all devboxes.
The intended model is one UI endpoint per running devbox instance.

## ACP Integration

When used as a Spritz ACP backend, this image exposes ACP on the reserved internal port `2529`
automatically. Spritz can then:

- fetch health and metadata from the instance ACP adapter
- mark the instance ACP-ready in `status.acp`
- proxy browser ACP traffic through `spritz-api`

The ACP adapter is image-owned compatibility glue. Once OpenClaw grows a native socket transport,
this adapter should be removed and the image should hand traffic to OpenClaw directly.

## Quick Check

```bash
openclaw --help
```
