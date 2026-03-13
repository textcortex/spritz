---
date: 2026-03-13
author: Onur Solmaz <onur@textcortex.com>
title: spz Audience and External Owner Guidance
tags: [spritz, spz, cli, audience, external-identity]
---

## Overview

This document defines the `AUDIENCE` environment variable contract for `spz`
and the default guidance for direct-owner versus external-owner create flows.

The goal is to keep the CLI behavior stable while making the human-readable
guidance safer for bots, messaging agents, and service-principal automation.

## `AUDIENCE` Contract

- `AUDIENCE` is a human-readable guidance switch for `spz`.
- Allowed values:
  - `human`
  - `agent`
- Default: `human`

`AUDIENCE` affects:

- `spz --help`
- subcommand help such as `spz create --help`
- human-readable examples and remediation hints

`AUDIENCE` must not change:

- API requests
- flag semantics
- exit codes
- JSON response shape

## Ownership Guidance Defaults

The default guidance for `spz create` must not assume the caller already knows
an internal owner ID.

Rules:

- direct `--owner-id` is a direct-owner path for internal, admin, or manual
  callers that already know the canonical internal owner ID
- external-owner flags are the default guidance whenever the caller only knows
  a platform-native user identity
- documentation and skill examples must not present `--owner-id` as the normal
  baseline for messaging-platform-driven creates

## Agent Audience Rules

When `AUDIENCE=agent`, the CLI help and printed remediation must guide the
caller to use external-owner resolution by default.

Agent guidance must state:

- if the request comes from a messaging platform such as Discord, Slack,
  Microsoft Teams, Mattermost, Google Chat, or similar, always use the stable
  user ID from that platform as the external subject
- for those flows, use:
  - `--owner-provider <platform>`
  - `--owner-subject <platform-user-id>`
- never pass a messaging-platform user ID through `--owner-id`
- do not ask the end user for an internal owner ID by default

Examples:

```bash
spz create \
  --preset claude-code \
  --owner-provider discord \
  --owner-subject 123456789012345678 \
  --source discord \
  --request-id discord-123 \
  --idempotency-key discord-123 \
  --json
```

```bash
spz create \
  --preset openclaw \
  --owner-provider msteams \
  --owner-subject 6f0f9d4f-9b0e-4d52-8c3a-ef0fd64b9b9f \
  --owner-tenant 11111111-2222-3333-4444-555555555555 \
  --source msteams \
  --request-id teams-123 \
  --idempotency-key teams-123 \
  --json
```

## Human Audience Rules

When `AUDIENCE=human`, the CLI may present both direct-owner and external-owner
paths, but it must still avoid implying that internal owner IDs are generally
required.

Human guidance should:

- show external-owner examples for messaging-platform integrations
- keep `--owner-id` documented as an explicit direct-owner mode
- explain the tradeoff in plain language when both paths are shown

## Unresolved External Identity Behavior

When the create flow is using an external owner and resolution fails because the
external identity is not linked to a Spritz owner, the human-readable error
guidance should tell the caller that the user needs to connect their account.

This guidance should stay generic and deployment-agnostic.

Preferred wording shape:

```text
The external account could not be resolved to a Spritz owner.
Ask the user to connect their account in the product or integration that owns
this identity mapping, then retry the create request.
```

This must not tell the caller to ask the user for an internal owner ID as the
default fallback.

## Clarification Behavior

When the required create inputs are not clear, the CLI guidance should tell the
caller to ask for clarification instead of guessing.

Examples of unclear input:

- the requested preset is ambiguous
- the platform is unclear
- the external subject is unavailable from message metadata
- tenant-scoped providers are missing tenant context

Preferred behavior:

- ask for the missing detail explicitly
- do not guess an internal owner ID
- do not silently switch from external-owner mode to direct-owner mode

## Skill and Help Requirements

The bundled `spz` skill must stay generic and correct:

- present external-owner create flows first for platform integrations
- explain unresolved-owner remediation as “connect the account”
- tell callers to ask for clarification when provider, subject, or preset is
  unclear

The CLI help and printed remediation must be audience-aware:

- `AUDIENCE=human` prints balanced direct-owner and external-owner guidance
- `AUDIENCE=agent` prints messaging-platform-first ownership guidance
- the modality switch must live in code, not in duplicated static skill text

## Validation

Validation for this contract should include:

- a bundled-skill test that asserts messaging-platform flows use
  `--owner-provider` and `--owner-subject`
- a bundled-skill test that asserts unresolved external owners are explained as
  “connect the account”
- CLI help tests for `AUDIENCE=human` and `AUDIENCE=agent`

## References

- `docs/2026-03-11-external-provisioner-and-service-principal-architecture.md`
- `docs/2026-03-12-external-identity-resolution-api-architecture.md`
- `cli/skills/spz/SKILL.md`
