# Claude Code Example Image

Example Spritz workspace image that starts Claude Code plus a workspace-local ACP websocket server on
port `2529`.

It stays generic:

- no bundled secrets
- no environment-specific domains or IDs
- no TextCortex-specific wiring

Build from `images/`:

```bash
docker build -f examples/claude-code/Dockerfile -t spritz-claude-code:latest .
```

Runtime contract:

- websocket ACP on `0.0.0.0:2529`
- health on `/healthz`
- metadata on `/.well-known/spritz-acp`
- adapter bin defaults to `claude-agent-acp`
- required auth env defaults to `ANTHROPIC_API_KEY`

Main overrides:

- `SPRITZ_CLAUDE_CODE_ACP_ENABLED`
- `SPRITZ_CLAUDE_CODE_ACP_BIN`
- `SPRITZ_CLAUDE_CODE_ACP_ARGS_JSON`
- `SPRITZ_CLAUDE_CODE_REQUIRED_ENV`
