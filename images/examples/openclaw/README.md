# OpenClaw Example Image

This image provides a generic Spritz devbox with `openclaw` preinstalled.

It intentionally stays environment-agnostic:

- no TextCortex domains
- no project-specific IDs
- no bundled secrets

## Build

Run from the `images/` directory so shared scripts are available in build context:

```bash
cd images
docker build -f examples/openclaw/Dockerfile -t spritz-openclaw:latest .
```

Optional build args:

- `OPENCLAW_VERSION` (default: `latest`)
- `ZMX_VERSION` (default: `0.1.1`)

Example:

```bash
docker build \
  -f examples/openclaw/Dockerfile \
  -t spritz-openclaw:latest \
  --build-arg OPENCLAW_VERSION=latest \
  .
```

## Runtime

The image reuses `examples/base/entrypoint.sh`.

Default command is `sleep infinity` so Spritz terminal/SSH sessions can attach cleanly.

To run OpenClaw manually inside a devbox:

```bash
openclaw --help
```
