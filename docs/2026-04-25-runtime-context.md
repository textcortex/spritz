---
date: 2026-04-25
author: Onur Solmaz
title: Runtime Context
tags: [spritz, runtime, context, agents, identity]
---

# Runtime Context

## Overview

Spritz already stores runtime-facing agent identity in control-plane state:

- `spec.agentRef` identifies the deployment-owned agent record.
- `spec.profileOverrides` stores caller-provided display overrides.
- `status.profile` stores the resolved display name and image URL used by the
  UI.

The current implementation does not write a runtime context file into instance
pods. Runtimes receive only the normal `Spritz` spec fields, environment
variables, mounted files, and their own image configuration.

## Implemented Contract

The implemented contract is the `Spritz` resource shape:

```yaml
spec:
  agentRef:
    type: external
    provider: example-catalog
    id: agent-123
  profileOverrides:
    name: Example Agent
    imageUrl: https://example.com/agent.png

status:
  profile:
    name: Example Agent
    imageUrl: https://example.com/agent.png
    source: synced
```

`agentRef` is an opaque deployment-owned reference. Spritz validates the shape,
not the provider-specific meaning.

`profileOverrides` is desired display metadata supplied by the caller.

`status.profile` is the canonical UI read path. The API can resolve it through
the provider-agnostic `agent.profile.sync` extension operation.

Bindings preserve these fields in their rendered runtime template, so a channel
installation can keep pointing at the same resolved agent identity as the active
runtime changes.

## Proposed Runtime File

A future runtime handoff may add a small file that tells a newly created
instance which agent it represents and which initial behavior context should be
applied.

Possible schema:

```text
spritz.runtimeContext.v1
```

Possible path:

```text
/etc/spritz/runtime-context/context.json
```

Possible environment variable:

```text
SPRITZ_RUNTIME_CONTEXT_PATH=/etc/spritz/runtime-context/context.json
```

Possible payload:

```json
{
  "schemaVersion": "spritz.runtimeContext.v1",
  "agentRef": {
    "type": "external",
    "provider": "example-catalog",
    "id": "agent-123"
  },
  "profile": {
    "name": "Example Agent",
    "imageUrl": "https://example.com/agent.png"
  },
  "instructions": "Reply clearly and briefly."
}
```

This file is not currently created by the operator, API, or OpenClaw example
image.

## Proposed Fields

Required fields:

- `schemaVersion`
- `agentRef.type`
- `agentRef.provider`
- `agentRef.id`

Optional fields:

- `profile.name`
- `profile.imageUrl`
- `instructions`

`agentRef` would mirror the implemented `spec.agentRef`.

`profile` would mirror the implemented `status.profile`.

`instructions` would be startup behavior context for runtimes that support
durable instruction transfer. Runtimes may ignore it when they do not support
this capability.

## Boundaries

Runtime context is not the place for:

- model routing
- tool configuration
- MCP server configuration
- knowledge base data
- channel allowlists
- owner credentials
- provider-specific auth state
- runtime image or resource settings

Those remain separate Spritz control-plane, runtime-policy, installation, or
deployment-owned concerns.

## Runtime Consumption

If a runtime context file is added later, Spritz should provide the file and the
runtime should decide how to consume it.

Examples:

- OpenClaw can render `profile` and `instructions` into workspace files.
- A runtime with native agent lookup can use only `agentRef`.
- A runtime with no profile support can ignore `profile`.

Spritz should not contain runtime-specific branches for these choices.

## OpenClaw Mapping

For OpenClaw-backed runtimes, the natural mapping is:

| Runtime context field | OpenClaw target |
| --- | --- |
| `agentRef` | import manifest and consistency metadata |
| `profile.name` | `IDENTITY.md` name and OpenClaw identity name |
| `profile.imageUrl` | `IDENTITY.md` avatar and OpenClaw identity avatar |
| `instructions` | `SOUL.md` imported instructions section |

The generic OpenClaw workspace model is documented separately in
[OpenClaw Agent Workspace Model](2026-04-25-openclaw-agent-workspace-model.md).

## Readiness

Today, readiness is based on the runtime's configured probes and ACP discovery,
not on runtime context consumption.

If a runtime context file is implemented later, an instance should not be
considered ready for agent traffic until the runtime has consumed the context
or explicitly declared that it does not support runtime context.

For OpenClaw-backed instances, that means startup has rendered the workspace
files before starting the gateway or ACP adapter.

## References

- [Agent Profile API](2026-03-30-agent-profile-api.md)
- [OpenClaw Agent Workspace Model](2026-04-25-openclaw-agent-workspace-model.md)
