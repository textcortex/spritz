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
- active profile field: `bearerToken`

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

## Zenobot and other preconfigured bot images

Some bot images ship with `spz` already configured through an active profile.

When you are inside one of those images:

- check `spz profile current`
- inspect `spz profile show <name>`
- prefer the active profile over asking for raw `SPRITZ_*` env vars

Expected shape:

- profile name like `zenobot`
- preconfigured API URL
- preconfigured bearer token
- preconfigured namespace when needed

So if `spz profile current` returns a profile, assume Spritz is already configured unless the command itself fails.

## Service-principal create flow

For bots and other external provisioners, prefer external owner resolution when
you only know the user's platform identity.

Example for a Discord-triggered create:

```bash
spz create \
  --owner-provider discord \
  --owner-subject 123456789012345678 \
  --preset openclaw \
  --idle-ttl 24h \
  --ttl 168h \
  --idempotency-key discord-interaction-123 \
  --source discord \
  --request-id discord-interaction-123 \
  --json
```

Rules:

- for Discord, Slack, Teams, and similar platform-triggered creates, pass the
  external platform user through `--owner-provider` and `--owner-subject`
- never pass a Discord, Slack, or Teams user ID through `--owner-id`
- do not ask for or depend on an internal owner ID unless it is already known
  from a trusted internal context
- use `--owner-id` only when you already have the canonical internal Spritz
  owner ID and intend a direct internal/admin create
- if provider, subject, preset, or tenant context is unclear, ask for
  clarification instead of guessing
- the service principal is only the actor
- the same `idempotency-key` and same request should replay the same workspace
- the same `idempotency-key` with a different request should fail with conflict
- the response should include the canonical access URL

## Common commands

Create from a preset for an external platform user:

```bash
spz create --preset openclaw --owner-provider discord --owner-subject 123456789012345678 --idle-ttl 24h --ttl 168h --idempotency-key req-123 --json
```

Create from a preset for a known internal owner:

```bash
spz create --preset openclaw --owner-id user-123 --idle-ttl 24h --ttl 168h --idempotency-key req-123 --json
```

If external owner resolution fails, explain it like this:

```text
The external account could not be resolved to a Spritz owner.
Ask the user to connect their account in the product or integration that owns
this identity mapping, then retry the create request.
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
spz profile set staging --api-url https://console.example.com/api --token service-token --namespace spritz
spz profile use staging
```

## Operational expectations

- prefer `--preset` over `--image` when a preset exists
- prefer bearer-token auth for bots
- for chat-platform-triggered creates, prefer external owner flags over direct
  `--owner-id`
- do not assume the caller already knows an internal owner ID
- if the required provider, subject, tenant, or preset is unclear, ask for the
  missing detail instead of guessing
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
