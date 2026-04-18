---
date: 2026-03-24
author: Onur Solmaz <onur@textcortex.com>
title: Slack Channel Gateway Implementation Plan
tags: [spritz, slack, channel-gateway, concierge, implementation-plan]
---

## Overview

This document turns the shared channel gateway architecture into a concrete
Slack-first implementation plan for Spritz.

It covers the generic Spritz side only:

- one shared Slack app
- one shared Slack channel gateway
- one concierge instance per Slack workspace
- route resolution from Slack `team_id` to the correct concierge instance
- outbound replies sent back through the same shared Slack gateway

It does not define deployment-specific owner resolution, billing, or secret
manager wiring.

Related docs:

- `docs/2026-03-23-shared-app-tenant-routing-architecture.md`
- `docs/2026-03-31-shared-channel-concierge-lifecycle-architecture.md`

## Scope

Phase 1 should support:

- Slack app install and reinstall
- Slack app uninstall or disconnect
- Slack Events API ingestion for message-driven concierge flows
- routing from one Slack workspace to one concierge instance
- outbound Slack actions through the shared gateway

Phase 1 should not require:

- Slack credentials inside concierge runtimes
- one Slack app per workspace
- workspace-local runtime ownership of Slack sockets or tokens

## Core Decisions

### Slack routing identity

Slack should use:

- `provider = slack`
- `externalScopeType = workspace`
- `externalTenantId = team_id`

That means one Slack workspace maps to one concierge instance.

### Gateway is a separate shared deployable

The Slack channel gateway should not run inside concierge pods.

It should be a shared deployable alongside `spritz-api` because it must:

- hold the shared Slack app credentials
- receive Slack webhook traffic
- deduplicate Slack retries
- route inbound events to the correct concierge
- perform outbound Slack API calls

### Concierge never gets Slack secrets

Concierge instances should receive normalized Slack events, not raw Slack app
credentials.

All Slack API calls should go through the shared Slack channel gateway.

## Required Contracts

### 1. External route resolution

Spritz should continue to use the existing extension transport for inbound
routing:

- `operation = "channel.route.resolve"`

The gateway provides:

- authenticated principal context
- `provider = slack`
- `externalScopeType = workspace`
- `externalTenantId = team_id`

The resolver returns:

- `instanceId`
- route state such as `ready`

Released or disconnected workspace routes should resolve as `unresolved`
rather than surfacing a reserved `disconnected` active state.

### 2. Installation registry client

Spritz should not hardcode Slack installation storage.

The Slack channel gateway should call a deployment-owned installation registry
API for:

- install upsert
- disconnect
- optional install metadata refresh

Spritz only needs the gateway to know:

- how to persist install state externally
- how to resolve a workspace back to an instance later

### 3. Lifecycle notifications

Spritz should continue using `instance.lifecycle.notify` so the external
registry can learn when a concierge becomes:

- provisioned
- ready
- unhealthy
- deleted

Lifecycle notifications should invalidate stale runtime bindings, but they are
not enough on their own. The live resolution path must still validate that the
returned concierge exists before the gateway uses it.

### 4. Live concierge resolution

The Slack gateway must not treat a stored concierge runtime name as durable
truth.

Before a Slack message is handed to Spritz, the gateway needs a session
exchange contract that guarantees one of these outcomes:

- `resolved`: the returned concierge exists and is usable now
- `unavailable`: recovery is still creating or restoring a live concierge
- `unresolved`: no active Slack installation exists for that routing identity

That session exchange surface should be a channel-facing adapter over the
shared live resolver, not its own separate resolution implementation.

That means session exchange must:

- validate the last-known concierge runtime before returning success
- recreate the concierge when the runtime is missing
- treat completed create replay pointing to a missing concierge as stale state,
  not success
- fail closed with `unavailable` when recovery has not finished yet

## Slack Install Flow

### Install or reinstall

1. User starts the Slack install flow for the shared Slack app.
2. Slack redirects back to the shared Slack channel gateway callback.
3. Gateway validates state and exchanges the OAuth code with Slack.
4. Gateway extracts installation identity:
   - `team.id`
   - `enterprise.id` when present
   - installing Slack user id
   - bot user id
   - granted scopes
5. Gateway persists install state through the external installation registry
   upsert API.
6. The external registry creates or reuses the concierge instance.
7. Gateway treats the install as complete only after the registry returns an
   active installation record.

Install must be idempotent:

- reinstalling the same workspace must not create duplicate concierge
  instances
- if the same workspace is already active, the same concierge should be reused
  unless deployment policy rejects reuse
- if stale create replay still points at a missing concierge, install or
  recovery must create a fresh live concierge instead of replaying the dead
  name

### Disconnect or uninstall

1. User removes the Slack app or the deployment explicitly disconnects it.
2. Gateway calls the external disconnect API for the same routing identity.
3. Route resolution must immediately stop returning a concierge for that
   workspace.
4. The active workspace claim must be released so a later install may bind the
   same workspace to a different owner.
5. Any retained concierge/runtime may remain only as detached history or a
   reusable artifact, depending on deployment policy.

## Inbound Event Flow

### Slack ingress

The shared Slack channel gateway should expose dedicated Slack ingress
surfaces for:

- OAuth callback
- Events API
- interactive payloads when the app enables buttons or modals
- slash-command callback when slash commands are enabled

Phase 1 should explicitly subscribe to:

- `app_mention`
- `message.channels`
- `message.im`
- `message.mpim` only if group DMs are in scope at launch
- `reaction_added`, `reaction_removed` only if reactions are part of the
  product flow

Important Slack nuance:

- `message.channels`, `message.groups`, `message.im`, and `message.mpim` are
  subscription labels
- the real inbound event type is still `message`
- the gateway should use `channel_type` to distinguish channel, group, DM, and
  group DM traffic

Phase 1 interactive support should be:

- block actions: yes
- slash commands: optional but supported by the gateway shape
- modal submissions: optional
- modal close events: optional

Phase 1 should treat these message subtypes as non-chat system events unless a
product requirement says otherwise:

- `message_changed`
- `message_deleted`
- `thread_broadcast`

### Event handling

1. Slack sends the inbound request to the shared gateway.
2. Gateway verifies the Slack signing secret and request timestamp.
3. Gateway extracts routing identity from the payload:
   - `api_app_id`
   - `team_id`
   - event type
   - `channel_type`
   - channel id
   - message ts
   - thread ts when present
   - external sender id
4. Gateway rejects the request if `api_app_id` or `team_id` do not match the
   expected shared Slack app installation.
5. Gateway resolves a live concierge session through the deployment-owned
   session exchange path.
6. If the concierge is unavailable, gateway may enter bounded recovery UX.
7. Gateway forwards a normalized inbound event only after it has a live
   concierge binding.

The normalized event should carry at least:

- `provider`
- `externalScopeType`
- `externalTenantId`
- `channelId`
- `threadTs`
- `messageTs`
- `externalSenderId`
- `text`
- `source`
- raw provider event reference

The `source` field should distinguish at least:

- `message`
- `app_mention`
- `slash_command`
- `block_action`
- `view_submission`
- `view_closed`

The gateway should also preserve enough raw event metadata to debug Slack
delivery problems without logging whole secrets or oversized payloads.

## Outbound Action Flow

Concierge instances should not call Slack directly.

Instead:

1. Concierge emits a normalized outbound action request.
2. The shared Slack channel gateway validates that the requesting concierge is
   allowed to act for that workspace.
3. Gateway performs the real Slack API call.

Phase 1 outbound actions should stay narrow:

- send message
- edit message
- add reaction

The action contract should include:

- routing identity
- concierge instance id
- action type
- action payload
- idempotency key

## Retry And Idempotency Rules

Slack retries are normal. The gateway must handle them deliberately.

### Inbound Slack retries

- verify the Slack retry headers
- deduplicate by Slack `event_id` when present
- for message dispatch, also guard against duplicate processing by `channel:ts`
- keep a bounded dedupe window
- ack quickly and hand off longer work asynchronously if needed

The gateway should explicitly handle the `message` vs `app_mention` overlap for
the same Slack message:

- a DM should be handled through `message`, not duplicated through
  `app_mention`
- channel messages may arrive through both paths for the same `ts`
- the gateway should allow one controlled fallback and otherwise suppress the
  duplicate path

The gateway should also resolve missing `thread_ts` for true thread replies
before forwarding the normalized event to the concierge when Slack does not
include it directly in the inbound payload.

### Install retries

- installation upsert must be safe to repeat
- repeated install callbacks for the same workspace must converge on one active
  concierge instance

### Outbound retries

- outbound actions must carry an idempotency key
- gateway should deduplicate repeated send requests for the same concierge
  action

### Availability retries and status messages

Slack should treat the visible `Still waking up...` message as a recovery
artifact, not as a generic slow-request indicator.

That means:

- the normal prompt path must not start a wake-up timer
- the gateway should enter the recovery path only after a real availability
  failure
- the visible-delay timer should start when recovery starts, not when the
  inbound Slack event arrives

Phase 1 retryable recovery signals should be:

- session exchange returned `unavailable`
- a refreshed route or session lookup found a missing runtime
- the first Spritz write failed with `spritz not found`
- the runtime exists but the first ACP prompt failed with `acp unavailable`
  before prompt delivery completed

Phase 1 non-recovery signals should be:

- ordinary prompt latency on a healthy runtime
- a slow ACP connect on a runtime that is already available

Slack should also treat inbound message handling as a durable pending delivery,
not as one synchronous webhook request that must finish the whole recovery path
before Slack's request budget is exhausted.

That means:

- acknowledge the inbound Slack event quickly
- create or resume one pending delivery record for that source Slack message
- let the recovery loop continue asynchronously behind that delivery record
- post `Still waking up...` only if that pending delivery crosses the
  visible-delay threshold
- send the final reply only after the runtime is actually prompt-ready

Phase 1 defaults for Slack:

- acknowledge the Slack event in under 1 second
- visible-delay threshold: 5 seconds after real recovery begins
- same-runtime `acp unavailable` retry: exponential backoff starting at 250
  milliseconds, capped at 2 seconds, for up to 8 seconds total
- stale-binding poll interval after `spritz not found` or missing runtime: 1
  second
- stale-binding poll budget: 20 seconds
- total pending-delivery timeout: 45 seconds

### Pending delivery flow

Phase 1 should use this flow for the first inbound Slack message and for
retries of the same message:

1. accept the Slack event and persist or resume one pending delivery keyed by
   the inbound Slack message identity
2. ask the shared live resolver for the current runtime binding
3. if the runtime is missing, terminal, or still being recreated, keep the
   delivery pending while recovery continues
4. if the runtime is live but the first ACP prompt is not ready yet, keep the
   same delivery pending and retry briefly
5. if the pending delivery crosses the visible-delay threshold, ensure one
   `Still waking up...` message exists for that source Slack message
6. when the prompt is accepted, continue the normal reply path and mark the
   delivery completed
7. if the delivery reaches terminal failure, send one failure reply and mark it
   failed

Phase 1 Slack storage should use:

- one durable delivery row keyed by `team_id + channel id + source ts`
- one optional status row keyed by `team_id + channel id + source ts +
  purpose`

If the Slack event is already threaded, the delivery row should still be keyed
by the source message identity, while `thread_ts` remains part of the reply
target metadata rather than the delivery identity itself.

The recovery loop should behave like this:

1. stay on the fast path for healthy ready runtimes
2. on `spritz not found`, retry session exchange with `forceRefresh = true`
3. on pre-delivery `acp unavailable`, retry the same runtime briefly before
   escalating to binding refresh
4. post `Still waking up...` only if that recovery loop crosses the
   visible-delay threshold
5. if recovery succeeds, send the normal reply
6. if recovery times out, send the terminal failure reply

Phase 1 Slack worker shape should be:

1. the webhook handler inserts or resumes the delivery row and returns `200`
   immediately
2. the webhook handler nudges an asynchronous worker
3. the worker claims a 30-second lease on the delivery row
4. the worker runs resolution, recovery, and delivery attempts
5. if the worker crashes or loses the lease, another worker resumes from the
   same row
6. duplicates from Slack converge on the same row instead of creating another
   execution path

The gateway should not recreate the runtime just because ACP is still coming
up on an otherwise healthy runtime.

The gateway should also not mark delivery success just because session exchange
returned `resolved`. Success means the prompt has actually been handed off to
the runtime and the normal reply path can continue.

## ACP Reply Text Integrity

The Slack gateway must treat ACP assistant text as lossless content, not as
display text that may be normalized.

That means:

- `agent_message_chunk` text must be assembled without trimming individual
  chunks
- spaces and newlines at chunk boundaries are part of the payload and must be
  preserved
- the gateway may trim only for emptiness checks at the final boundary, not as
  part of text extraction or chunk joining
- channel adapters should reuse one shared ACP text extraction and chunk-join
  helper instead of reimplementing their own whitespace rules

If this contract is violated, the provider-visible reply can silently corrupt
content even when the runtime output is correct. Typical failures are:

- merged words across chunk boundaries
- lost paragraph breaks
- flattened lists or code blocks

## Threading Defaults

Phase 1 should keep channel behavior predictable:

- direct-message conversations reply inline
- top-level channel turns reply top-level by default
- top-level channel turns use the source Slack message ts as the conversation
  identity
- threaded channel turns use the thread root `thread_ts` as the conversation
  identity
- if the gateway posts a visible top-level assistant reply, later user replies
  threaded off that bot message must map back to the original source-message
  conversation instead of forking a new one
- if inbound Slack payload already has `thread_ts`, reuse it for the outbound
  reply so existing threaded follow-ups stay in that thread

That matches the desired Zenobot-style room behavior: visible top-level replies
for normal channel turns, with stable follow-up context only when the user is
already continuing the same Slack root message or thread.

## Persisted Metadata

The external installation registry should persist enough Slack metadata to make
routing and reinstall deterministic.

Required fields:

- shared channel gateway principal id
- `provider = slack`
- `externalScopeType = workspace`
- `externalTenantId = team_id`
- install state
- target concierge instance id
- provider install reference

Recommended Slack metadata:

- `enterprise_id` when present
- `api_app_id`
- installing Slack user id
- bot user id
- granted scopes
- install timestamp
- last refresh timestamp

Raw bot tokens should remain in deployment-owned secret storage, not in Spritz
instance metadata.

## Suggested Validation

Before calling Phase 1 done, verify:

1. The same shared Slack app can be installed into two workspaces.
2. Each workspace resolves to a different concierge instance.
3. Reinstalling the same workspace reuses the same concierge.
4. Disconnecting a workspace causes route resolution to return `unresolved`.
5. A DM event reaches the correct concierge.
6. A channel event replies in the correct thread.
7. Concierge outbound replies go through the shared Slack channel gateway, not
   directly from the runtime.
8. The same Slack message delivered through both `message` and `app_mention`
   produces one concierge execution.
9. A mismatched `team_id` or `api_app_id` is rejected before route resolution.
10. A deleted concierge does not get returned as `resolved`.
11. A deleted concierge is recreated before the next successful Slack turn.
12. A stale completed create reservation does not replay a dead concierge name.
13. A normal slow reply on a healthy runtime does not post `Still waking up...`.
14. A recovered runtime may post `Still waking up...` only while real recovery
    is in progress.
15. A pre-delivery `acp unavailable` is retried as an availability case and
    does not fail the first recovered turn prematurely.
16. Duplicate Slack webhook deliveries converge on the same pending delivery.
17. The first recovered Slack turn is not marked successful until the prompt is
    actually accepted by ACP.
18. Multiline assistant replies preserve spaces and newlines across ACP chunk
    boundaries.

## Follow-ups

- Add Discord and Teams gateway plans once the Slack path is validated.
- Decide whether the Slack gateway should be its own binary or a dedicated
  module inside a shared channel-gateway service.
- Add interactive payload handling once message routing is proven stable.
