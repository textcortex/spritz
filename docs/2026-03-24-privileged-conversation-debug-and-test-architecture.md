---
date: 2026-03-24
author: Onur Solmaz <onur@textcortex.com>
title: Privileged Conversation Debug and Test Architecture
tags: [spritz, conversations, debug, testing, auth, audit, architecture]
---

## Overview

This document defines the preferred architecture for privileged conversation
debugging and testing in Spritz.

For the broader long-term conversation architecture, see
[Spritz-Native Conversation Broker Architecture](2026-03-24-spritz-native-conversation-broker-architecture.md).

The target model is:

- the Spritz control plane owns all privileged debug and test access,
- runtimes remain private and do not trust engineer laptops directly,
- a narrow internal API brokers conversation actions on behalf of authorized
  callers,
- a thin `spz` command becomes the standard machine client for this flow,
- every privileged action is authorized, scoped, time-bounded, and audited.

This is the "holy grail" end state for headless conversation testing:

- no direct pod access as the normal workflow,
- no browser dependency for routine debugging,
- no special runtime backdoors,
- no duplicate fake conversation path,
- one canonical way to send a message, read a response, and inspect the result.

## Problem

Today, conversation debugging is possible, but the workflow is operationally
awkward:

- discover the target instance,
- reach ACP through port-forward or equivalent plumbing,
- speak ACP directly from the client,
- collect session state and response chunks locally,
- clean up the temporary path by hand.

That is acceptable for one-off debugging, but it is not the correct long-term
abstraction for:

- fast local verification,
- repeatable smoke tests,
- CI validation,
- incident triage,
- privileged support workflows,
- agent-driven testing.

The platform needs a first-class internal conversation testing surface that is:

- elegant,
- secure,
- auditable,
- portable,
- runtime-agnostic.

## Goals

- Make "send a message and read the response" a first-class control-plane
  operation for authorized internal callers.
- Keep the real runtime path intact by using the same conversation transport the
  product already depends on.
- Centralize authorization, audit logging, and environment policy in
  `spritz-api`.
- Give engineers, CI, and authorized agents a stable CLI/API path that does not
  require direct Kubernetes access.
- Preserve a strict separation between:
  - user-facing access,
  - service-principal automation,
  - privileged internal debugging.

## Non-goals

- Exposing a privileged conversation interface to all end users.
- Replacing browser-based end-to-end tests.
- Making runtimes accept hidden admin passwords or debug bypass flags.
- Giving engineers direct pod or ACP access as the preferred workflow.
- Duplicating conversation execution logic outside the real runtime path.

## Design Principles

### Spritz remains the control plane

Privileged debugging must be owned by the control plane, not by ad hoc client
scripts or direct runtime access.

That means Spritz owns:

- authorization,
- policy checks,
- session minting,
- audit logging,
- environment gating,
- conversation brokering,
- debug result normalization.

### One real message path

The privileged debug/test path must use the same conversation execution path as
normal product traffic.

It may add:

- stronger authorization,
- richer observability,
- server-side brokering,
- privileged inspection APIs.

It must not add:

- a fake shortcut that skips the real runtime,
- a separate model invocation implementation,
- a special-case execution path only used by tests.

### Runtimes trust the control plane, not the caller

The runtime should not need to understand whether the caller is:

- a human user,
- a CI job,
- a support engineer,
- an internal coding agent.

The runtime should trust only:

- the Spritz control plane,
- a short-lived capability minted by the control plane,
- the existing per-conversation transport and binding rules.

### Capabilities over ambient privilege

Privileged access should be expressed as narrow, short-lived capabilities, not
as long-lived broad admin power.

Every capability should be bound to:

- one actor,
- one environment,
- one target instance or conversation,
- one allowed action set,
- one reason,
- one expiration time.

### Default deny for cross-owner access

The safest default is:

- a caller may only inspect or drive conversations they already own.

Cross-owner access should require explicit privileged scope and stronger audit
requirements.

## Large-Scale Internal Platform Model

A large internal platform would usually avoid direct workstation-to-runtime
debug access and would instead centralize the path behind a privileged internal
service.

The essential traits of that model are:

- workforce or service identity at the edge,
- centralized authorization,
- delegated short-lived credentials,
- mandatory audit logging,
- environment-specific policy,
- explicit production break-glass controls,
- a boring, standard RPC surface.

Spritz should follow the same model.

The practical lesson is simple:

- engineers should talk to `spritz-api`,
- `spritz-api` should talk to the conversation runtime,
- the runtime should not need to trust the engineer directly.

## Core Model

The clean target architecture introduces a new internal component inside
`spritz-api`:

- privileged conversation broker

This broker:

- resolves the target instance or conversation,
- authorizes the actor,
- opens or resumes the real conversation transport,
- sends the message over the real runtime path,
- streams or collects the resulting events,
- returns normalized output to the caller,
- records a complete audit trail.

### Canonical actors

The model should distinguish three principal classes:

### Human owner

The default case.

The human owner can:

- read their own conversation,
- send a message to their own conversation,
- inspect their own transcript,
- use the normal browser flow.

### Internal automation principal

A machine caller such as:

- CI,
- a regression harness,
- a support automation,
- an internal coding agent.

This caller should only have the narrow scopes it needs.

### Admin or break-glass principal

A separate elevated role for cross-owner investigation or production response.

This role should be:

- rare,
- explicit,
- heavily audited,
- environment-gated.

## Privileged Debug Session Model

The broker should mint a short-lived debug session before it performs a
privileged conversation action.

Recommended fields:

- `sessionId`
- `actor`
- `target`
- `environment`
- `reason`
- `allowedActions`
- `expiresAt`
- `createdAt`

Recommended target binding:

- instance name and namespace, or
- canonical Spritz conversation id

Recommended action set:

- `conversation.read`
- `conversation.send`
- `conversation.events.read`
- `conversation.transcript.read`
- `conversation.cancel`

The session should be invalid outside its narrow target and lifetime.

## Authorization Model

### Owner-scoped default path

The default privileged API should still respect ownership.

Example:

- a human or service principal acting on their own instance can debug without
  cross-owner privileges.

This supports:

- local verification,
- CI for pre-owned test principals,
- internal agent-driven testing with dedicated owners.

### Cross-owner privileged path

Cross-owner conversation access should require an explicit elevated scope such
as:

- `spritz.debug.conversations.read.any`
- `spritz.debug.conversations.send.any`

These scopes must not be implied by:

- instance creation,
- standard admin UI access,
- generic bearer access,
- service-principal create permissions.

### Production break-glass

Production should require stronger controls than staging.

Recommended requirements:

- explicit reason,
- optional ticket or incident reference,
- explicit production debug scope,
- short TTL,
- stronger audit retention,
- optional dual control or approval outside Spritz core.

### Environment gates

The broker should support deployment policy such as:

- enabled in staging,
- disabled by default in production,
- enabled in production only for explicit privileged principals.

## Internal API Model

The exact path names can change, but the control-plane shape should look like
this:

### Create a privileged debug session

```text
POST /internal/v1/debug/sessions
```

Request shape:

- target instance or conversation
- requested actions
- reason
- optional environment-specific metadata

Response shape:

- debug session metadata
- expiration time
- effective allowed actions

### Send a message

```text
POST /internal/v1/debug/sessions/{sessionId}/messages
```

Request shape:

- message content
- conversation id or runtime session hint
- wait mode (`stream` or `complete`)

Response shape:

- accepted message id
- conversation id
- stream handle or synchronous result envelope

### Read events

```text
GET /internal/v1/debug/sessions/{sessionId}/events
```

This should support:

- streaming chunked events,
- reconnectable event cursors,
- normalized assistant text extraction,
- timing metadata.

### Read transcript

```text
GET /internal/v1/debug/sessions/{sessionId}/transcript
```

This should return:

- normalized transcript items,
- message roles,
- event timestamps,
- stop reason,
- optional tool/event metadata.

### Close the debug session

```text
DELETE /internal/v1/debug/sessions/{sessionId}
```

## Broker Execution Model

The privileged broker should execute the conversation operation server-side.

Recommended flow:

1. authenticate actor
2. authorize target and requested actions
3. mint debug session
4. resolve target runtime binding
5. bootstrap or resume the real conversation
6. send the message through the real runtime path
7. collect and stream events
8. write audit records
9. expire the session automatically

The broker may talk to the runtime over:

- ACP,
- an internal conversation gateway,
- another runtime-native transport.

The important invariant is:

- the control plane owns the privileged hop,
- the client does not.

## CLI Model

`spz` should be the standard machine client for this workflow.

Recommended commands:

```bash
spz debug session create \
  --instance example-instance \
  --namespace example-namespace \
  --reason "regression verification"

spz chat send \
  --instance example-instance \
  --namespace example-namespace \
  --message "Reply with the exact token example-token and nothing else." \
  --wait \
  --json

spz chat transcript \
  --instance example-instance \
  --namespace example-namespace \
  --json
```

### Why `spz`

`spz` is already the thin machine client for Spritz.

Reusing it preserves:

- one operational surface,
- one auth model,
- one output format,
- one place for smoke and debug tooling.

The CLI should remain thin. It should not:

- speak ACP directly by default,
- implement conversation routing logic,
- evaluate authorization rules locally.

## Audit Model

Every privileged conversation action must emit an audit record.

Recommended fields:

- actor id
- actor type
- target owner id
- target instance or conversation id
- environment
- reason
- action type
- created at
- completed at
- result classification
- message hash
- response hash

Optional content capture should be deployment-controlled.

Safe default:

- store structured metadata and hashes by default,
- allow full content capture only where explicitly approved.

## Data Safety and Privacy

Privileged debug access must be treated as sensitive because conversations may
contain:

- user prompts,
- assistant output,
- tool traces,
- secrets,
- customer data,
- internal source code.

Recommended defaults:

- redact obvious secrets in derived logs,
- keep raw content out of broad metrics,
- bound transcript reads to explicit scope,
- avoid long-lived storage in the broker,
- expire debug session material quickly.

## Security Properties

This design should guarantee all of the following:

- direct runtime access is not required for routine debugging,
- the runtime does not accept broad ambient admin access,
- privileged access is short-lived and target-bound,
- cross-owner access is explicit and auditable,
- production access is more constrained than staging,
- the same real conversation path is used for debugging and product traffic,
- the CLI remains a thin client over the control plane.

## Migration Path

### Phase 1

Add an internal broker API in `spritz-api` and a thin `spz chat send` client
for owner-scoped usage.

The current phase-one shape is:

- `POST /api/internal/v1/debug/chat/send`
- `spz chat send --instance ... --message ...`
- `spz chat send --conversation ... --message ...`

This phase-one endpoint is intentionally synchronous and owner-scoped.
It should only be registered when both internal auth and normal caller auth are
enabled, so the control plane can bind the request to a real authenticated
principal instead of trusting a caller-supplied owner id.
Transcript reads, event streaming, and explicit cross-owner break-glass remain
later phases.

This replaces the need for direct ACP from local laptops for routine testing.

The longer-term target is to move this flow under the canonical conversation
broker described in
[Spritz-Native Conversation Broker Architecture](2026-03-24-spritz-native-conversation-broker-architecture.md).

### Phase 2

Add server-side event streaming and transcript reads.

This makes CI and agent-driven debugging first-class.

### Phase 3

Add explicit cross-owner privileged scopes and production break-glass policy.

This supports support and incident-response workflows safely.

### Phase 4

Treat direct port-forward and raw ACP debugging as fallback-only operator tools,
not as the standard path.

## Validation

The architecture is successful when all of the following are true:

- an authorized caller can send a message to an owned conversation without
  browser access
- the caller can read the real assistant response through the control plane
- the runtime does not need to trust the caller directly
- all privileged actions create audit records
- cross-owner access is denied without explicit elevated scope
- production access is gated more tightly than staging
- CI can run a real conversation smoke without direct ACP plumbing on the
  runner

## References

- `docs/2026-03-09-acp-port-and-agent-chat-architecture.md`
- `docs/2026-03-10-acp-conversation-storage-and-replay-model.md`
- `docs/2026-03-11-external-provisioner-and-service-principal-architecture.md`
- `docs/2026-03-13-acp-smoke-contract.md`
