---
name: spz
description: Use the spz CLI to provision, inspect, and access Spritz workspaces, including service-principal create flows, preset-based provisioning, canonical access URLs, and ACP-ready workspace operations.
---

# spz

## When to use this skill

Use this skill when you need to interact with a Spritz control plane through the `spz` CLI.

Typical cases:

- create a new workspace from a preset such as `openclaw` or `claude-code`
- create a workspace on behalf of a user with a service-principal bearer token
- suggest a DNS-safe random name for a workspace
- inspect a workspace URL
- attach to terminal access
- manage local `spz` profiles for different Spritz environments

## What Spritz is

Spritz is a control plane for agent workspaces.

Core model:

- a workspace is a `Spritz` resource
- the workspace may expose ACP on port `2529`
- Spritz owns provisioning, routing, auth, canonical URLs, and lifecycle
- the backend image owns the runtime itself

For external provisioners:

- the human remains the owner
- the service principal is only the actor that created the workspace
- create-only service principals should not be able to edit, delete, terminal into, or list user workspaces unless explicitly granted

## Authentication modes

`spz` supports two auth models.

### 1. Bearer token

Use this for services, bots, and automation.

- env: `SPRITZ_BEARER_TOKEN`
- flag: `--token`

This is the preferred mode for external provisioners such as bots.

### 2. Header-based user identity

Use this for local or trusted internal environments.

- `SPRITZ_USER_ID`
- `SPRITZ_USER_EMAIL`
- `SPRITZ_USER_TEAMS`

This mode is not the right fit for external automation.

## Important environment variables

- `SPRITZ_API_URL`: Spritz API base URL, for example `https://console.example.com/api`
- `SPRITZ_BEARER_TOKEN`: service-principal bearer token
- `SPRITZ_CONFIG_DIR`: config directory for profiles
- `SPRITZ_PROFILE`: active profile name

## Service-principal create flow

For external provisioners, the normal command is:

```bash
spz create \
  --owner-id user-123 \
  --preset openclaw \
  --idle-ttl 24h \
  --ttl 168h \
  --idempotency-key discord-interaction-123 \
  --source discord \
  --request-id discord-interaction-123 \
  --json
```

Rules:

- `owner-id` is the human who should own the workspace
- the service principal is only the actor
- the same `idempotency-key` and same request should replay the same workspace
- the same `idempotency-key` with a different request should fail with conflict
- the response should include the canonical access URL

## Common commands

Create from a preset:

```bash
spz create --preset openclaw --owner-id user-123 --idle-ttl 24h --ttl 168h --idempotency-key req-123 --json
```

Create from an explicit image:

```bash
spz create --image example.com/spritz-devbox:latest --owner-id user-123 --idempotency-key req-123 --json
```

Suggest a name:

```bash
spz suggest-name --preset claude-code
```

Open a workspace URL:

```bash
spz open openclaw-tide-wind
```

List workspaces:

```bash
spz list
```

Open a terminal:

```bash
spz terminal openclaw-tide-wind
```

Use profiles:

```bash
spz profile set staging --api-url https://console.example.com/api --namespace spritz
spz profile use staging
```

## Operational expectations

- prefer `--preset` over `--image` when a preset exists
- prefer bearer-token auth for bots
- treat the create response as the source of truth for the access URL
- do not construct workspace URLs yourself
- use idempotency keys for any retried or externally triggered create operation
- for service principals, expect create to succeed and list/delete to be denied unless extra scopes were granted

## Bundled skill usage

This package includes a bundled `spz` skill for Codex-compatible environments.

Install it into the current user's Codex skill directory with:

```bash
spz --skill install spz --agent codex --scope user --force
```

Inspect it with:

```bash
spz --skill show spz
spz --skill list
```
