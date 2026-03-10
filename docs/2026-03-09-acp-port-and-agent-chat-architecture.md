---
date: 2026-03-09
author: Onur <onur@textcortex.com>
title: ACP Port and Agent Chat Architecture
tags: [spritz, acp, agent-chat, architecture, helm]
---

## Overview

This document defines the production ACP model in Spritz.

The goal is a default setup that works out of the box with the Helm chart:

- every workspace reserves ACP on port `2529`
- any backend listening on that port is treated as ACP-capable
- Spritz discovers ACP agents automatically
- the browser talks to agents through the Spritz API gateway
- ACP remains backend-agnostic so workspaces can run OpenClaw or any other ACP implementation

## Architecture

### ACP contract inside each workspace

Spritz reserves one internal service/container port for ACP:

- port: `2529`
- transport: WebSocket
- path: `/`
- protocol: ACP JSON-RPC

If a workspace process listens there and answers ACP `initialize`, Spritz treats it as an ACP agent.

### Discovery ownership

The operator owns ACP discovery.

When a workspace deployment is ready, the operator:

1. connects to `ws://<spritz>.<namespace>.svc.cluster.local:2529/`
2. sends ACP `initialize`
3. normalizes the response into `status.acp`
4. sets the `ACPReady` condition on the `Spritz` resource

The API does not probe ACP during user requests.

### API ownership

The API owns only:

- listing ACP-ready workspaces
- managing conversation metadata
- proxying authenticated ACP WebSocket traffic from browser to workspace

This keeps discovery and status mutation in the control plane, not in request handlers.

### Conversation persistence model

`SpritzConversation` stores metadata only:

- `spec.spritzName`
- `spec.owner`
- `spec.title`
- `spec.sessionId`
- `spec.cwd`
- normalized ACP `agentInfo`
- normalized ACP `capabilities`

It does not store the transcript.

Each conversation has its own generated ID, so one workspace can have many independent ACP threads.

### Browser path

The browser never connects directly to workspace ACP ports.

The path is always:

`browser -> spritz-api -> workspace:2529`

That keeps auth, origin checks, and future policy enforcement in one place.

## Helm surface

The chart exposes ACP through these values:

```yaml
acp:
  enabled: true
  port: 2529
  path: /
  probeTimeout: 3s
  refreshInterval: 30s
  networkPolicy:
    enabled: false

api:
  acp:
    origins: []
```

Meaning:

- `acp.*` is the shared runtime contract used by the operator and API
- `api.acp.origins` controls which browser origins may open the ACP WebSocket gateway
- `acp.networkPolicy.enabled=true` restricts inbound ACP traffic to Spritz API and operator pods

## Backend-agnostic requirement

Spritz must not depend on OpenClaw-specific ACP behavior.

Any workspace backend may be used as long as it:

- listens on port `2529`
- speaks ACP over WebSocket
- returns a valid `initialize` response
- supports the ACP session methods needed by the client flow

OpenClaw is one example backend, not the protocol owner.

For the current OpenClaw preset, Spritz ships an image-owned compatibility bridge that exposes
WebSocket ACP on `2529` and forwards each connection into the image-owned
`spritz-openclaw-acp-wrapper` over stdio. This bridge is intentionally confined to the image so
the Spritz ACP control plane does not become OpenClaw-specific.

## UI behavior

The default Spritz UI provides a test and operator surface for ACP:

- left column: ACP-ready agents
- middle column: conversations for the selected workspace
- right column: active ACP thread

Each thread maps to one `SpritzConversation` and one ACP session lifecycle.

On reconnect:

- the UI first asks Spritz API to bootstrap the selected conversation
- Spritz API loads the stored ACP session or explicitly repairs it if the
  backend confirms that the session is missing
- the UI then connects through the ACP bridge using the confirmed conversation
  binding

## Security model

The production defaults are:

- ACP is internal-only Kubernetes traffic
- browser access goes through the authenticated Spritz API
- ACP origin checks are enforced on the API WebSocket bridge
- optional ACP NetworkPolicy can block direct pod-to-pod access except from API and operator
- transcripts are not stored in Kubernetes resources

## Validation

A deployment is considered correct when all of these are true:

- new workspaces expose service/container port `2529`
- `Spritz.status.acp.state` becomes `ready` for ACP-capable workspaces
- `ACPReady=True` appears on ready agents
- `GET /api/acp/agents` lists only ACP-ready workspaces
- `POST /api/acp/conversations` creates a generated conversation ID
- the UI can create a conversation, connect, and exchange ACP messages through the API bridge

## References

- `README.md`
- `OPENCLAW.md`
- `docs/2026-02-24-simplest-spritz-deployment-spec.md`
