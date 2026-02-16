# OpenClaw Example Image

This image provides a generic Spritz devbox with `openclaw` preinstalled.

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

- `OPENCLAW_VERSION` (default: `latest`)

Example:

```bash
docker build \
  -f examples/openclaw/Dockerfile \
  -t spritz-openclaw:latest \
  --build-arg OPENCLAW_VERSION=latest \
  .
```

## Runtime

The image uses a small OpenClaw wrapper entrypoint and then calls
`examples/base/entrypoint.sh`.

By default, when the container command is the image default (`sleep infinity`),
it auto-starts the OpenClaw gateway on port `8080` with a LAN bind so Spritz `Open` can render
OpenClaw UI immediately.

To disable auto-start and keep shell-only behavior, set:

- `OPENCLAW_AUTO_START=false`

Auto-start related runtime overrides:

- `OPENCLAW_GATEWAY_PORT` (default: `8080`)
- `OPENCLAW_GATEWAY_MODE` (default: `local`)
- `OPENCLAW_GATEWAY_BIND` (default: `lan`; set `loopback` for local-only)
- `OPENCLAW_GATEWAY_TOKEN` (optional; auto-generated if omitted)

## Generic Config Support

OpenClaw config can be supplied at runtime without rebuilding the image:

- `OPENCLAW_CONFIG_JSON`: raw JSON content for `openclaw.json`
- `OPENCLAW_CONFIG_B64`: base64-encoded JSON content
- `OPENCLAW_CONFIG_FILE`: path to an existing config file in the container
- `OPENCLAW_CONFIG_DIR` (optional): config directory (default: `${HOME}/.openclaw`)
- `OPENCLAW_CONFIG_PATH` (optional): config file path (default: `${OPENCLAW_CONFIG_DIR}/openclaw.json`)

If none are provided and no config exists, a minimal browser config is written.

## Spritz Open Integration

Use this image as a per-devbox application surface in Spritz:

- point the Spritz `Open` action to the OpenClaw UI running inside that specific devbox
- keep access checks tied to that devbox owner/session

Do not expose a single global/shared OpenClaw dashboard for all devboxes.
The intended model is one UI endpoint per running devbox instance.

## Quick Check

```bash
openclaw --help
```
