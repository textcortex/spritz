---
date: 2026-03-13
author: Onur Solmaz <onur@textcortex.com>
title: Spawn Language for Agent Workspaces
tags: [spritz, wording, cli, agents]
---

## Overview

This document defines `spawn` as the preferred user-facing verb for starting an
agent in a Spritz-backed system.

In plain language:

- `spawn an agent` means `create a Spritz workspace for that agent`
- the technical system action is still `create` / `provision`
- the stored resource is still a `workspace`

This is a vocabulary rule, not a data model change.

## Definition

Use `spawn` when the human intent is:

- start a fresh agent workspace
- make that agent reachable through its returned URLs or terminal access
- create the workspace from a preset such as `openclaw` or `claude-code`

Do not use `spawn` to mean:

- start a local child process
- fork an existing workspace
- duplicate a conversation
- create a new owner identity

## Rules

When an agent or operator says `spawn`, interpret it as:

1. resolve who the workspace is for
2. resolve what preset or workspace spec should be used
3. call Spritz create
4. report back with what was spawned and how to open it

Keep the canonical technical language unchanged:

- API actions stay `create`
- provisioning remains `provisioning`
- the resource remains a `workspace`
- ownership remains the internal Spritz owner model

## CLI and Skill Guidance

The bundled `spz` skill should understand `spawn` as shorthand for creating a
Spritz workspace.

Expected behavior:

- if a user asks to `spawn` an agent, use `spz create`
- if the owner is an external platform user, use external owner flags rather
  than treating the platform ID as `--owner-id`
- if preset, provider, subject, or tenant is unclear, ask for clarification
- after success, report the spawned workspace name and returned open URLs

## Examples

User-facing request:

```text
Spawn a claude-code agent for me.
```

Technical action:

```bash
spz create --preset claude-code ...
```

User-facing request:

```text
Spawn an openclaw agent for this Discord user.
```

Technical action:

```bash
spz create --preset openclaw --owner-provider discord --owner-subject 123456789012345678 ...
```

## Validation

The bundled `spz` skill description and examples should include the word
`spawn` so agents learn the intended vocabulary at the skill boundary.

