---
date: 2026-03-10
author: Onur <onur@textcortex.com>
title: ACP Adapter and Runtime Target Architecture
tags: [spritz, acp, adapter, runtime, architecture]
---

## Overview

This document defines the target runtime architecture for ACP-backed
instances in Spritz.

The goal is a control plane that stays simple:

- Spritz creates and manages instances
- Spritz routes authenticated ACP traffic to those instances
- the instance exposes one stable ACP endpoint on port `2529`
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

- instance provisioning
- user authentication and authorization
- instance discovery and metadata
- conversation records
- browser-facing ACP gatewaying

Spritz does not own:

- backend runtime internals
- backend transcript storage
- backend-specific session semantics

### ACP adapter

Each ACP-capable instance should expose one long-lived ACP service on
port `2529`.

If the backend is not natively ACP, the instance should run an ACP adapter.

The ACP adapter owns:

- ACP transport termination on `2529`
- HTTP health and metadata on the same port for control-plane use
- ACP session lifecycle
- deterministic mapping from ACP session id to backend runtime session
- transcript replay for `session/load`
- translation between backend-native events and ACP events
- graceful handling of upstream disconnects and shutdown

The ACP adapter must be the only backend-specific integration layer.

## Preferred Adapter Shape

The preferred shape is one generic Spritz ACP server harness on port `2529`
with a backend ACP command behind it.

That means:

- Spritz owns one stable WebSocket and HTTP surface on `2529`
- the backend integration behind that surface should preferably be a command
  that already speaks ACP over stdio
- backend-specific logic should be limited to a small adapter shim when the
  backend needs custom session or replay behavior

Examples of the desired command-side interface:

- `openclaw acp`
- `claude-agent-acp`
- any other command that speaks ACP over stdio

This keeps the instance contract stable even when the backend changes.

The shared harness should own:

- HTTP `GET /healthz`
- HTTP `GET /.well-known/spritz-acp`
- WebSocket upgrade and ACP transport handling
- connection lifecycle and graceful shutdown
- common error normalization

The backend shim should own only:

- backend command startup
- backend-specific session mapping
- backend-specific transcript replay
- backend-specific event translation where ACP stdio is not sufficient

To actually minimize duplication, this repository should treat the shared
server harness as the single owner of the generic ACP server shell.

That means new backend examples should not each implement their own copies of:

- HTTP health and metadata endpoints
- WebSocket upgrade and ACP transport shell
- common shutdown logic
- common error normalization
- common socket ownership guardrails

Instead, backend examples should plug backend-specific behavior into the shared
harness through a narrow adapter interface.

That adapter interface should cover only:

- how to start or attach to the backend ACP command
- how backend session ids map to ACP session ids when custom mapping is needed
- how transcript replay works when the backend cannot rely on pure ACP stdio
- any backend-specific metadata additions beyond the shared contract

### Backend runtime

The backend runtime owns:

- actual execution
- actual transcript and session state
- tool execution
- backend-native storage and runtime semantics

The backend runtime should not be directly coupled to Spritz.

## Relation To ACPX

`acpx` is a useful reference because it already demonstrates one interface for
multiple ACP-capable backends through stdio commands.

That validates the direction above:

- one stable ACP-facing surface
- many interchangeable backend commands behind it

Spritz should not depend on `acpx` as its runtime session layer, though.

Spritz already owns:

- conversation records
- conversation to ACP session binding
- authenticated gatewaying

Adding `acpx` session management as a second runtime control layer would make
session ownership harder to reason about.

The preferred use of `acpx` here is as a design reference for backend command
registration and stdio ACP integration, not as the deployed instance session
manager.

## Target Instance Contract

Every ACP-capable instance should provide exactly one stable internal ACP
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
- metadata-only or initialize-only sockets do not steal active runtime session
  ownership from a real conversation connection

## Control-Plane Changes

### Operator

The operator should stop using real ACP runtime websocket sessions as the
normal liveness path.

The operator should use:

- Kubernetes readiness for basic health
- the adapter's lightweight HTTP health and metadata paths for ACP capability discovery

ACP metadata refresh should be slow and side-effect free. It must not create
or tear down backend runtime sessions as part of routine health checks.

### API

The Spritz API should remain the only browser-facing ACP gateway.

The path should stay:

`browser -> spritz-api -> instance ACP endpoint`

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
- ask the API to bootstrap the conversation when the conversation is new or the
  binding is missing or broken
- connect through the Spritz ACP gateway
- render replayed and live ACP updates

For an already active conversation binding, reconnect should go straight to the
ACP gateway without running bootstrap again first.

It should not:

- infer backend session ownership
- route by backend runtime ids
- maintain correctness-critical transcript state locally

## Adapter Requirements

The ACP adapter is the key runtime boundary.

It should be implemented as one long-lived process per instance, not as a new
process spawned for every websocket connection.

Required behavior:

- accept many ACP client connections over time
- expose cheap HTTP health and metadata endpoints without starting real runtime sessions
- keep backend session mapping deterministic
- keep runtime ownership rules explicit so short-lived probe or bootstrap
  sockets cannot steal an active session from the main chat connection
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

The target is one clear path from browser to instance ACP runtime.

## Implementation Direction

The current cutover in Spritz implements the first step of this architecture:

- the OpenClaw example image now runs one long-lived ACP server process on `2529`
- the operator now uses HTTP health and metadata instead of periodic ACP websocket probes
- instance pod readiness and liveness use the ACP health endpoint

Remaining implementation work should focus on these changes:

1. Extract and standardize the shared `2529` ACP server harness so backend
   examples reuse one outer server shape.
2. Ensure all adapter-to-backend traffic stays pod-local or cluster-local.
3. Keep conversation bootstrap and binding ownership in the API.
4. Make transcript replay fully backend-driven through `session/load`.
5. Define and enforce socket ownership rules so initialize-only and bootstrap
   connections cannot disrupt active conversation sockets.
6. Add soak tests that keep chat sessions open across repeated metadata refresh
   intervals and verify no websocket reset churn or transcript corruption.

The shared harness extraction is the required duplication-reduction step.

That refactor should end with these repository rules:

- new ACP example images must reuse the shared harness
- generic ACP server behavior must not be reimplemented inside backend example
  directories
- backend example directories may only own backend-specific adapter code

## Validation

The target architecture is considered correct when all of the following are
true:

- an instance exposes one stable ACP endpoint on `2529`
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
