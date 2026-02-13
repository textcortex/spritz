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

Default command is `sleep infinity` so Spritz terminal/SSH sessions can attach cleanly.

## Generic Config Support

OpenClaw config can be supplied at runtime without rebuilding the image:

- `OPENCLAW_CONFIG_JSON`: raw JSON content for `openclaw.json`
- `OPENCLAW_CONFIG_B64`: base64-encoded JSON content
- `OPENCLAW_CONFIG_FILE`: path to an existing config file in the container
- `OPENCLAW_CONFIG_DIR` (optional): config directory (default: `${HOME}/.openclaw`)
- `OPENCLAW_CONFIG_PATH` (optional): config file path (default: `${OPENCLAW_CONFIG_DIR}/openclaw.json`)

If none are provided and no config exists, a minimal browser config is written.

## Quick Check

```bash
openclaw --help
```
