# Codex Example Image

Example Spritz instance image that starts Codex plus an instance-local ACP websocket server on
port `2529`.

It stays generic:

- no bundled secrets
- no environment-specific domains or IDs
- no TextCortex-specific wiring

Build from `images/`:

```bash
docker build -f examples/codex/Dockerfile -t spritz-codex:latest .
```

Runtime contract:

- websocket ACP on `0.0.0.0:2529`
- health on `/healthz`
- metadata on `/.well-known/spritz-acp`
- Codex ACP bridge defaults to `codex`
- required auth env defaults to `OPENAI_API_KEY`

By default the entrypoint seeds Codex auth from `OPENAI_API_KEY` with:

```bash
printf '%s' "$OPENAI_API_KEY" | codex login --with-api-key
```

Main overrides:

- `SPRITZ_CODEX_ACP_ENABLED`
- `SPRITZ_CODEX_BIN`
- `SPRITZ_CODEX_ARGS_JSON`
- `SPRITZ_CODEX_MODEL`
- `SPRITZ_CODEX_REQUIRED_ENV`
