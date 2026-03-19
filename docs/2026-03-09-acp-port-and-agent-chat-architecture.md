---
date: 2026-03-09
author: Onur <onur@textcortex.com>
title: ACP Port and Agent Chat Architecture
tags: [spritz, acp, agent-chat, architecture, helm]
---

## Overview

This document defines the production ACP model in Spritz.

The goal is a default setup that works out of the box with the Helm chart:

- every instance reserves ACP on port `2529`
- any backend listening on that port is treated as ACP-capable
- Spritz discovers ACP agents automatically
- the browser talks to agents through the Spritz API gateway
- ACP remains backend-agnostic so instances can run OpenClaw or any other ACP implementation

## Architecture

### ACP contract inside each instance

Spritz reserves one internal service/container port for ACP:

- port: `2529`
- transport: WebSocket
- path: `/`
- protocol: ACP JSON-RPC

If an instance process listens there and answers ACP `initialize`, Spritz treats it as an ACP agent.

For the current Spritz ACP runtime contract, the same service should also expose:

- `GET /healthz`
- `GET /.well-known/spritz-acp`

Those HTTP endpoints exist for control-plane health and metadata discovery. Browser and API ACP
traffic still uses the WebSocket endpoint.

### Discovery ownership

The operator owns ACP discovery.

When an instance deployment is ready, the operator:

1. checks `http://<spritz>.<namespace>.svc.cluster.local:2529/healthz`
2. fetches `http://<spritz>.<namespace>.svc.cluster.local:2529/.well-known/spritz-acp` when ACP
   metadata is missing or stale
3. normalizes that response into `status.acp`
4. sets the `ACPReady` condition on the `Spritz` resource

The API does not probe ACP during user requests.

### API ownership

The API owns only:

- listing ACP-ready instances
- managing conversation metadata
- proxying authenticated ACP WebSocket traffic from browser to instance

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

Each conversation has its own generated ID, so one instance can have many independent ACP threads.

### Browser path

The browser never connects directly to instance ACP ports.

The path is always:

`browser -> spritz-api -> instance:2529`

That keeps auth, origin checks, and future policy enforcement in one place.

## Helm surface

The chart exposes ACP through these values:

```yaml
acp:
  enabled: true
  port: 2529
  path: /
  healthPath: /healthz
  metadataPath: /.well-known/spritz-acp
  probeTimeout: 3s
  refreshInterval: 30s
  metadataRefreshInterval: 5m
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

Any instance backend may be used as long as it:

- listens on port `2529`
- speaks ACP over WebSocket
- returns a valid `initialize` response
- supports the ACP session methods needed by the client flow

OpenClaw is one example backend, not the protocol owner.

For the current OpenClaw preset, Spritz ships an image-owned ACP adapter that exposes:

- WebSocket ACP on `2529`
- `GET /healthz`
- `GET /.well-known/spritz-acp`

That adapter owns OpenClaw-specific session mapping and transcript replay, so the Spritz ACP
control plane stays backend-agnostic.

The preferred long-term shape is to converge backend examples on one shared
Spritz ACP server harness on port `2529`, with a backend ACP stdio command
behind it.

That means the common server shell should stay the same across examples, while
the backend-specific command can differ:

- `openclaw acp`
- `claude-agent-acp`
- any other ACP-capable stdio command

This is also the part of the design that `acpx` validates well: one stable ACP
interface can front many different backend commands. Spritz should keep owning
conversation binding and gatewaying itself rather than depending on `acpx`
session management directly.

## UI behavior

The default Spritz UI provides a test and operator surface for ACP:

- left column: ACP-ready agents
- middle column: conversations for the selected instance
- right column: active ACP thread

Each thread maps to one `SpritzConversation` and one ACP session lifecycle.

On reconnect:

- if the conversation is new or the binding is missing or broken, the UI asks
  Spritz API to bootstrap the selected conversation first
- if the conversation already has an active binding, the UI reconnects directly
  through the ACP gateway path without bootstrapping again first
- Spritz API repairs the binding only when the backend confirms that the
  stored ACP session is missing or invalid

Socket ownership rules matter here:

- initialize-only sockets must not steal an active runtime session
- short-lived bootstrap or repair sockets must not interrupt a live chat socket
- only real session traffic for the selected conversation should be allowed to
  claim runtime ownership

## Security model

The production defaults are:

- ACP is internal-only Kubernetes traffic
- browser access goes through the authenticated Spritz API
- ACP origin checks are enforced on the API WebSocket bridge
- optional ACP NetworkPolicy can block direct pod-to-pod access except from API and operator
- transcripts are not stored in Kubernetes resources

## Validation

A deployment is considered correct when all of these are true:

- new instances expose service/container port `2529`
- `Spritz.status.acp.state` becomes `ready` for ACP-capable instances
- `ACPReady=True` appears on ready agents
- `GET /api/acp/agents` lists only ACP-ready instances
- `POST /api/acp/conversations` creates a generated conversation ID
- the UI can create a conversation, connect, and exchange ACP messages through the API bridge

## References

- `README.md`
- [OpenClaw Integration](2026-03-13-openclaw-integration.md)
- `docs/2026-03-10-acp-adapter-and-runtime-target-architecture.md`
- `docs/2026-02-24-simplest-spritz-deployment-spec.md`
