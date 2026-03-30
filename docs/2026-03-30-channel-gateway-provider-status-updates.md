---
date: 2026-03-30
author: Onur Solmaz <onur@textcortex.com>
title: Channel Gateway Provider Status Updates
tags: [spritz, channel-gateway, slack, discord, teams, architecture]
---

## Overview

This document defines a shared channel gateway feature that lets the gateway
send one provider-native status message back into the originating conversation
while the target runtime is waking up, recovering, provisioning, or otherwise
delayed.

Slack should ship this first because the shared Slack gateway already exists.
The feature itself must remain provider-agnostic so the same model can later
support Discord, Teams, and similar providers.

These messages are gateway-authored. They are not emitted by the underlying
runtime.

Related docs:

- `docs/2026-03-23-shared-app-tenant-routing-architecture.md`
- `docs/2026-03-24-slack-channel-gateway-implementation-plan.md`

## Context

The shared channel gateway model already supports:

1. provider event ingestion
2. route resolution to the target runtime
3. runtime execution
4. outbound provider replies through the same shared gateway

That leaves a UX gap when the runtime is not immediately available. Common
examples:

- the runtime was expired and is being recreated
- the runtime is still provisioning
- the gateway is retrying route or session resolution
- the first meaningful reply is delayed long enough that the conversation would
  otherwise stay silent

In those cases the gateway should be able to post one visible status message
before the real reply is ready.

## Core Decision

The shared channel gateway should be allowed to create one provider-visible
status message without waiting for a runtime-originated outbound action.

The feature should follow these rules:

- the gateway authors the status message with the shared provider app identity
- the status message targets the same conversation context as the source
  message
- the fast path stays silent if recovery finishes before a short threshold
- at most one active status message exists per inbound message and purpose
- terminal success does not require cleanup, replacement, or a second
  "I am back" message when a real reply is about to follow

## Goals

- provide immediate in-channel feedback when runtime recovery or delay becomes
  user-visible
- keep provider credentials in the shared gateway instead of passing them into
  runtimes
- define one provider-agnostic model that works for Slack first and can later
  extend to Discord and Teams
- make status delivery idempotent across provider retries, gateway retries, and
  gateway restarts

## Non-Goals

- streaming partial model output through the gateway
- exposing raw runtime phases directly to end users
- sending multiple progress updates for the same delayed request in v1
- replacing the normal outbound reply path for final responses

## UX Contract

### When to show a status message

The gateway should not send a visible status message immediately.

Recommended default:

- wait 3 to 5 seconds after inbound processing starts
- if the real reply or runtime recovery completes before that threshold, send
  nothing extra
- if the threshold is crossed, ensure one visible status message exists

Recommended initial trigger categories:

- runtime recovery in progress
- runtime provisioning in progress
- retrying a failed route or session lookup
- first-reply latency crossed the visible-delay threshold

### What the user should see

The text should be short, neutral, and action-oriented.

Examples:

- "Still waking up. I will continue here shortly."
- "Still working on that. I will reply here shortly."
- "I could not recover the channel runtime. Please try again."

The gateway should describe the effect on the user, not cluster terminology.

### How success should look

Phase 1 should stay simple:

- once the gateway posts the status message, leave it in place
- when the runtime is ready, send the normal final reply once
- do not delete, edit, or replace the status message in v1
- do not send a separate terminal "ready" message

### How failure should look

If recovery fails or times out, the gateway may leave the status message in
place and send one clear terminal error reply into the same conversation
context.

## Provider Targeting Model

The feature must use normalized conversation targeting so providers can map the
same behavior onto different APIs.

At minimum the gateway should derive:

- `conversationRef`
- `threadRef` when the provider has a thread or reply primitive
- `sourceMessageRef`
- `senderRef`

Provider-specific examples:

| Provider | Conversation target | Thread or reply target | Preferred status form |
| --- | --- | --- | --- |
| Slack | channel id | `thread_ts` when present, otherwise provider default for that conversation type | bot message in the same Slack conversation |
| Discord | channel id or thread id | source message reference when replying | reply or follow-up bot message |
| Teams | chat or channel conversation id | reply-to message id when supported | reply in conversation or fallback follow-up |

The provider adapter decides the exact API call shape, but the user should
experience the same behavior across providers.

## Status Message Model

The gateway should track one durable status record per inbound message that
crosses the visible-delay threshold.

Recommended fields:

- `provider`
- `principalId`
- `externalScopeType`
- `externalTenantId`
- `conversationRef`
- `threadRef`
- `sourceMessageRef`
- `purpose`
- `state`
- `providerMessageRef`
- `expiresAt`
- timestamps

Recommended purposes:

- `runtime-recovery`
- `provisioning-delay`
- `first-reply-delay`
- `terminal-failure`

Recommended states:

- `pending`
- `visible`
- `completed`
- `failed`

The storage implementation belongs to the shared gateway deployment or another
external integration store. Spritz core should not need to persist provider
message ids inside runtime objects.

## Gateway Behavior

### Inbound processing

1. provider event reaches the gateway
2. the gateway acknowledges the provider webhook within the provider timeout
3. the gateway starts route resolution, runtime reconciliation, and normal
   delivery
4. if the request crosses the visible-delay threshold, the gateway ensures one
   status message exists for that source message

### Recovery loop

While the runtime is unavailable, the gateway may continue recovery work such
as:

- session exchange retries
- runtime recreation polling
- installation reconciliation
- provider retry coordination

Once the visible-delay threshold has produced one status message, the gateway
should keep using that same status record for deduplication and bookkeeping.
It should not post repeated progress messages for the same source message in
v1.

### Finalization

When the request reaches a terminal state:

- on success, mark the status record completed and continue with the normal
  reply path
- on failure, mark the status record failed and send one clear terminal error
  reply if needed
- on duplicate inbound delivery, converge on the same status record instead of
  creating a second one

## Provider Adapter Contract

The provider gateway layer should expose a narrow status-message interface
alongside the normal outbound reply interface.

Example shape:

```text
ensure_status_message(target, purpose, idempotency_key, body) -> provider_message_ref
```

Phase 1 requires only creation. Editing and deletion are optional future
capabilities, not part of the base contract.

Provider capabilities still differ, so the adapter should declare whether it
supports:

- replying in a thread or reply chain
- fallback follow-up messages when native reply targeting is unavailable

The gateway should use the same provider authorization boundary as normal
outbound replies. The runtime should not receive raw provider tokens for this
feature.

## Relationship To Runtime Outbound Actions

This feature is separate from runtime-authored outbound actions.

There are two outbound initiation paths:

1. runtime-authored actions, such as normal replies, edits, and reactions
2. gateway-authored status messages, created by gateway control logic

Both paths should reuse the same provider adapter primitives where possible,
but they must remain distinguishable in logging, metrics, and idempotency.

## Idempotency And Reliability Rules

- one inbound provider message may create at most one active status message for
  a given purpose
- provider webhook retries must resolve to the same status record
- gateway restarts must be able to recover and continue from the same status
  record
- finalizing an already-completed or failed status record must be safe
- the gateway must not emit a second final reply because a status message was
  retried

Recommended idempotency key input:

- provider
- principal id
- external tenant identity
- source message identity
- status purpose

## Implementation Sequence

### Phase 1: Foundation (Critical Priority)

- define the gateway-owned status record abstraction and storage contract
- add visible-delay threshold handling to the inbound processing path
- implement the generic provider adapter method for ensure
- implement Slack first because the shared Slack gateway already exists
- add metrics for creation, completion, failure, and time-to-first-status

### Phase 2: Additional Providers (High Priority)

- implement Discord targeting and status behavior
- implement Teams targeting and fallback behavior where reply capabilities are
  weaker
- normalize provider capability flags so behavior stays consistent across
  gateways

### Phase 3: Enhancements (Medium Priority)

- add richer status purposes such as queue delay or downstream retry delay
- add localization or provider-specific copy templates
- add operational controls for delay thresholds and timeout budgets

## Validation

Before calling the feature production-ready, validate:

1. a fast request produces no visible status message
2. a delayed request produces exactly one status message in the correct
   conversation target
3. duplicate provider deliveries do not create duplicate status messages
4. runtime recovery does not post additional status messages for the same
   source message
5. successful completion leaves the status message in place and sends the final
   reply once
6. terminal failure leaves the status message in place and sends a clear error
   reply once
7. gateway restart during recovery still converges on the same provider message
8. the runtime never receives raw provider credentials for status delivery

## References

- `docs/2026-03-23-shared-app-tenant-routing-architecture.md`
- `docs/2026-03-24-slack-channel-gateway-implementation-plan.md`
