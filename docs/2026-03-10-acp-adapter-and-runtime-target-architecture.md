---
date: 2026-03-10
author: Onur <onur@textcortex.com>
title: ACP Adapter and Runtime Target Architecture
tags: [spritz, acp, adapter, runtime, architecture]
---

## Overview

This document defines the target runtime architecture for ACP-backed
workspaces in Spritz.

The goal is a control plane that stays simple:

- Spritz creates and manages workspaces
- Spritz routes authenticated ACP traffic to those workspaces
- the workspace exposes one stable ACP endpoint on port `2529`
- the backend behind that endpoint remains replaceable

Spritz must stay backend-agnostic. OpenClaw is one backend, not a special case
that shapes the control-plane architecture.

## Problem Statement

The current OpenClaw integration still couples ACP connection lifecycle to
backend process lifecycle too tightly.

That creates avoidable failure modes:

- short-lived ACP probes can trigger real runtime work
- disconnecting one ACP websocket can tear down backend state abruptly
- backend-specific behavior leaks into the control plane
- reconnect and replay behavior becomes harder to reason about

The target architecture is to separate those responsibilities cleanly.

## Desired Role Split

### Spritz

Spritz owns:

- workspace provisioning
- user authentication and authorization
- workspace discovery and metadata
- conversation records
- browser-facing ACP gatewaying

Spritz does not own:

- backend runtime internals
- backend transcript storage
- backend-specific session semantics

### ACP adapter

Each ACP-capable workspace should expose one long-lived ACP service on
port `2529`.

If the backend is not natively ACP, the workspace should run an ACP adapter.

The ACP adapter owns:

- ACP transport termination on `2529`
- ACP session lifecycle
- deterministic mapping from ACP session id to backend runtime session
- transcript replay for `session/load`
- translation between backend-native events and ACP events
- graceful handling of upstream disconnects and shutdown

The ACP adapter must be the only backend-specific integration layer.

### Backend runtime

The backend runtime owns:

- actual execution
- actual transcript and session state
- tool execution
- backend-native storage and runtime semantics

The backend runtime should not be directly coupled to Spritz.

## Target Workspace Contract

Every ACP-capable workspace should provide exactly one stable internal ACP
contract:

- port: `2529`
- transport: WebSocket
- protocol: ACP JSON-RPC

That endpoint should be long-lived and safe for multiple clients over time.

Spritz should be able to assume:

- ACP `initialize` is safe and cheap
- `session/new` creates a backend session for a Spritz conversation
- `session/load` replays transcript from backend storage
- disconnecting one client does not corrupt or abruptly terminate the backend

## Control-Plane Changes

### Operator

The operator should stop using real ACP runtime websocket sessions as the
normal liveness path.

The operator should use:

- Kubernetes readiness for basic health
- a lightweight metadata refresh path for ACP capability discovery

ACP metadata refresh should be slow and side-effect free. It must not create
or tear down backend runtime sessions as part of routine health checks.

### API

The Spritz API should remain the only browser-facing ACP gateway.

The path should stay:

`browser -> spritz-api -> workspace ACP endpoint`

The API should own:

- conversation bootstrap
- conversation to ACP session binding
- authenticated ACP proxying

The browser should not invent, repair, or replace ACP session bindings on its
own.

### UI

The UI should stay thin.

It should:

- select conversations by Spritz conversation id
- ask the API to bootstrap the conversation
- connect through the Spritz ACP gateway
- render replayed and live ACP updates

It should not:

- infer backend session ownership
- route by backend runtime ids
- maintain correctness-critical transcript state locally

## Adapter Requirements

The ACP adapter is the key runtime boundary.

It should be implemented as one long-lived process per workspace, not as a new
process spawned for every websocket connection.

Required behavior:

- accept many ACP client connections over time
- keep backend session mapping deterministic
- perform graceful shutdown of upstream resources
- isolate transport disconnects from transcript correctness
- support replay from backend transcript storage
- normalize backend errors into ACP errors instead of leaking raw HTML or
  transport pages into transcripts

For a backend like OpenClaw today, the adapter should talk to OpenClaw locally
over private pod-internal addresses only.

It should not route internal requests through public ingress, external CDN
hosts, or other edge paths.

## Transcript and Session Model

The stable model remains:

- `SpritzConversation.metadata.name` is the route and thread id
- `SpritzConversation.spec.sessionId` is the ACP session id
- the backend runtime session key stays internal to the adapter/backend

The source of truth for transcript history should be the backend session store.

The adapter must make `session/load` replay that history correctly without
depending on browser cache.

Browser-local transcript cache is allowed only as a rendering optimization.

## Cutover Rules

This architecture should be treated as a cutover, not a long-term dual-stack
migration.

Rules:

- keep one ACP runtime path
- do not keep parallel legacy and new runtime flows alive indefinitely
- remove per-connection child-process ACP runtime behavior once the adapter is
  in place
- remove browser-side session repair logic once API bootstrap is authoritative
- keep one cache format for ACP thread rendering

The target is one clear path from browser to workspace ACP runtime.

## Implementation Direction

The next implementation work should focus on these changes:

1. Introduce or harden a single long-lived ACP adapter runtime per workspace.
2. Remove operator dependence on short-lived ACP websocket probes for health.
3. Ensure all adapter-to-backend traffic stays pod-local or cluster-local.
4. Keep conversation bootstrap and binding ownership in the API.
5. Make transcript replay fully backend-driven through `session/load`.
6. Add soak tests that keep chat sessions open across repeated metadata refresh
   intervals and verify no websocket reset churn or transcript corruption.

## Validation

The target architecture is considered correct when all of the following are
true:

- a workspace exposes one stable ACP endpoint on `2529`
- repeated readiness and metadata refresh cycles do not create runtime churn
- disconnecting one ACP client does not produce abnormal backend websocket
  reset loops
- reopening a conversation restores transcript from backend replay
- Spritz UI, API, and operator remain backend-agnostic
- replacing OpenClaw with another ACP-compatible backend does not require
  control-plane changes

## References

- `docs/2026-03-09-acp-port-and-agent-chat-architecture.md`
- `docs/2026-03-10-acp-conversation-storage-and-replay-model.md`
- `docs/2026-02-24-simplest-spritz-deployment-spec.md`
