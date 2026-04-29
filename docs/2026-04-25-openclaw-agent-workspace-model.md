---
date: 2026-04-25
author: Onur Solmaz
title: OpenClaw Agent Workspace Model
tags: [openclaw, agents, identity, workspace]
---

# OpenClaw Agent Workspace Model

## Overview

OpenClaw agent behavior is defined by two things:

- OpenClaw runtime configuration
- files in the agent workspace

Spritz should treat this as an OpenClaw-owned model. Spritz may start an
OpenClaw instance and provide storage or configuration, but the agent's
personality belongs in OpenClaw workspace files.

The current Spritz implementation does not materialize these workspace files.
Spritz stores `spec.agentRef`, optional `spec.profileOverrides`, and
`status.profile`; it passes only normal runtime configuration into the OpenClaw
image. Deployment-owned systems may still prepare OpenClaw workspaces or
OpenClaw config outside Spritz core.

## Runtime Configuration

OpenClaw runtime configuration chooses the agent record, workspace, model, and
state directories.

The most relevant fields are:

- `agents.defaults.workspace`
- `agents.defaults.model`
- `agents.list[].id`
- `agents.list[].workspace`
- `agents.list[].agentDir`
- `agents.list[].identity`

`agentDir` stores agent state such as authentication, sessions, and runtime
state. It is not the primary home for the agent's personality.

`agents.list[].identity` stores display and routing identity:

- `name`
- `theme`
- `emoji`
- `avatar`

OpenClaw can populate this identity from workspace metadata with:

```bash
openclaw agents set-identity --workspace <workspace> --from-identity
```

## Workspace Files

OpenClaw loads a known set of files from the agent workspace and injects them
into the system prompt context.

The important files are:

| File | Purpose |
| --- | --- |
| `AGENTS.md` | Workspace operating instructions and boot rules |
| `SOUL.md` | Agent persona, tone, behavior, and durable instructions |
| `IDENTITY.md` | Name, theme, emoji, avatar, and display identity source |
| `USER.md` | User profile and preferences |
| `MEMORY.md` | Curated long-term memory for main sessions |
| `memory/YYYY-MM-DD.md` | Daily or recent memory notes |
| `TOOLS.md` | User guidance for tool usage, not tool availability |
| `HEARTBEAT.md` | Heartbeat behavior |
| `BOOTSTRAP.md` | First-run setup instructions for a fresh workspace |

`SOUL.md` is the closest OpenClaw-native equivalent of an agent personality or
system prompt. If `SOUL.md` is present, OpenClaw tells the model to embody that
persona and tone unless higher-priority instructions override it.

`IDENTITY.md` is metadata. It should not be treated as the full personality.
It exists so OpenClaw can derive identity values for UI display, mentions,
prefixes, and avatars.

## System Prompt Assembly

For an agent turn, OpenClaw builds a system prompt from:

1. OpenClaw's base runtime instructions
2. tool and runtime capability instructions
3. workspace and messaging instructions
4. loaded workspace files
5. optional extra system prompt supplied by the caller or runtime path

The workspace files are shown to the model as project context. That makes them
inspectable and editable by the agent, which is important for OpenClaw's memory
and personality model.

## Provisioning External Agents

If a deployment wants an OpenClaw instance to behave like an external agent from
a catalog, the recommended pattern is to materialize that external agent into
an OpenClaw workspace before the runtime starts.

This is not done by the Spritz operator today. The implemented Spritz side is
limited to resolving and storing `agentRef` and UI profile data.

The neutral mapping is:

| External agent data | OpenClaw target |
| --- | --- |
| display name | `IDENTITY.md` name and `agents.list[].identity.name` |
| avatar or image URL | `IDENTITY.md` avatar and `agents.list[].identity.avatar` |
| description | short context in `IDENTITY.md` or `SOUL.md` |
| behavior instructions | `SOUL.md` |
| model preference | `agents.list[].model` or `agents.defaults.model` |
| tools | OpenClaw config or runtime tool policy |
| durable memory | `MEMORY.md` only when explicitly migrated |

Do not inject a large external agent prompt only as an invisible per-turn
`extraSystemPrompt` unless this is a temporary compatibility bridge. That path
is harder to inspect, harder to edit, and duplicates the OpenClaw workspace
model.

## Spritz Boundary

Spritz should stay provider-agnostic:

- Spritz can store and display an instance profile.
- Spritz can start an OpenClaw image with OpenClaw config.
- Spritz can expose ACP and route users to the instance.
- Spritz should not interpret `SOUL.md`, `IDENTITY.md`, or external agent
  prompt semantics.

Deployment-owned systems may still generate workspace files for an OpenClaw
instance. That generation belongs outside Spritz core unless it is expressed as
a neutral file or mount mechanism.

## Current Repository Behavior

The repository currently implements:

- `spec.agentRef` and `spec.profileOverrides` on `Spritz`
- `status.profile` for UI-facing display metadata
- `agent.profile.sync` extension support during create flows
- rendered binding templates that preserve resolved agent identity
- OpenClaw image configuration through `OPENCLAW_CONFIG_JSON`,
  `OPENCLAW_CONFIG_B64`, or `OPENCLAW_CONFIG_FILE`

The repository does not currently implement:

- writing `IDENTITY.md` or `SOUL.md` into the OpenClaw workspace
- setting OpenClaw identity from Spritz `status.profile`
- a `SPRITZ_RUNTIME_CONTEXT_PATH` handoff file
- readiness gates based on OpenClaw workspace materialization

## Validation

A correctly provisioned OpenClaw-backed instance should satisfy:

- OpenClaw config points at the intended workspace.
- `IDENTITY.md` contains the intended display identity.
- `SOUL.md` contains the intended durable agent behavior.
- `openclaw agents list --json` shows the expected identity after identity sync.
- A first agent turn reflects `SOUL.md` without requiring hidden prompt
  injection.

## References

- [OpenClaw Integration](2026-03-13-openclaw-integration.md)
- [Agent Profile API](2026-03-30-agent-profile-api.md)
