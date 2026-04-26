---
date: 2026-04-26
author: Spritz Contributors <user@example.com>
title: Channel Delivery Feedback Architecture
tags: [spritz, channel-gateway, delivery, feedback, slack, discord, teams, architecture]
---

## Overview

Spritz channel gateways should own provider-visible delivery feedback.

Delivery feedback means the user-visible or provider-visible signals that show a
message was received, accepted, delayed, running, completed, failed, or silently
completed. Examples include reactions on the source message, short automatic
status messages, provider assistant status, and future provider-specific
progress indicators.

The core ownership split is:

- Spritz owns channel delivery UX
- the runtime owns reasoning and response generation
- provider adapters translate delivery state into provider-native UI

This keeps Spritz backend-agnostic and keeps runtimes from needing provider
tokens, provider message ids, or provider-specific UX rules.

Related docs:

- [Channel Gateway Provider Status Updates](2026-03-30-channel-gateway-provider-status-updates.md)
- [Slack Gateway No-Reply Outcome Architecture](2026-04-16-slack-gateway-no-reply-outcome-architecture.md)
- [Channel Install Message Policy Config](2026-04-24-channel-install-message-policy-config.md)
- [Slack Channel Gateway Implementation Plan](2026-03-24-slack-channel-gateway-implementation-plan.md)

## Problem

A channel gateway often needs to show progress before the runtime produces a
final answer.

For example:

- add an acknowledgement reaction to the user's original Slack message
- remove the acknowledgement reaction after the runtime finishes
- send one automatic "still starting" message if runtime recovery is slow
- avoid posting an error when the runtime completes with an intentional
  `no_reply` outcome

These signals are not model output. They are gateway-authored delivery UX.

If the runtime owns them, the architecture becomes provider-coupled:

- the runtime needs Slack, Discord, or Teams credentials
- the runtime needs provider-specific message ids and timestamps
- provider scopes and retry behavior leak into backend adapters
- automatic delivery messages can be confused with real agent replies

Spritz should instead model these as delivery feedback emitted by the channel
gateway around the runtime call.

## Core Decision

Channel gateways should maintain a generic delivery feedback lifecycle for each
accepted inbound provider message.

The gateway receives the provider event, resolves the target runtime, creates or
resumes delivery state, applies configured feedback policy, calls the runtime,
then finalizes the feedback based on the typed delivery outcome.

Provider-specific adapters own only the last mapping step:

| Feedback type | Slack mapping | Discord mapping | Teams mapping |
| --- | --- | --- | --- |
| acknowledgement reaction | `reactions.add` / `reactions.remove` on source message | message reaction add/remove | supported reaction or fallback status |
| automatic status message | `chat.postMessage` in the same conversation/thread | channel/thread message | conversation reply or follow-up |
| agent-authored reaction | provider action/tool calls `reactions.add` on a requested message | provider action/tool calls message reaction API | provider action/tool when supported |
| assistant status | Slack assistant thread status when available | provider-specific status when available | provider-specific status when available |
| no reply | acknowledge event and post nothing | acknowledge event and post nothing | acknowledge event and post nothing |

The runtime should see only a channel-agnostic prompt context and should return
a typed outcome such as `deliver_message`, `no_reply`, or `hard_error`.

## Delivery Feedback Policy

Spritz should represent delivery feedback policy as generic installation or
channel configuration. The exact storage owner can be deployment-specific, but
the semantics should remain portable.

Recommended shape:

```json
{
  "deliveryFeedback": {
    "acknowledgement": {
      "type": "reaction",
      "reaction": "eyes",
      "scope": "group-all",
      "removeAfterReply": true
    },
    "statusMessages": {
      "runtimeStarting": {
        "enabled": true,
        "delayMs": 3000,
        "message": "Still starting. I will continue here shortly."
      },
      "timeout": {
        "enabled": true,
        "message": "I could not finish this request. Please try again."
      },
      "error": {
        "enabled": true,
        "message": "Something went wrong while processing that request."
      }
    }
  }
}
```

Field meanings:

- `deliveryFeedback`: provider-facing UX policy for runtime delivery.
- `acknowledgement`: immediate or near-immediate feedback for an accepted
  inbound message.
- `acknowledgement.type`: the feedback mechanism. Initial supported values
  should be `reaction` and `none`.
- `reaction`: provider-neutral reaction name. Provider adapters translate this
  to provider API syntax.
- `scope`: when acknowledgement applies. Initial values should match existing
  OpenClaw-style semantics where practical, such as `group-mentions` and
  `group-all`.
- `removeAfterReply`: whether the acknowledgement should be removed when the
  delivery reaches a terminal outcome.
- `statusMessages`: gateway-authored automatic messages for delay, timeout,
  and error cases.
- `delayMs`: how long the gateway waits after entering the relevant delay state
  before posting a visible status message.

Default behavior should be conservative:

- acknowledgement feedback disabled unless configured by the deployment or
  installation
- automatic status messages enabled only for real runtime recovery or delivery
  delays, not every slow model response
- no status message for `no_reply`
- failures in feedback delivery never fail the user message delivery

## Delivery State Machine

The gateway should treat feedback as derived from durable delivery state.

Recommended states:

1. `received`
2. `accepted`
3. `resolving_runtime`
4. `runtime_starting`
5. `running`
6. `reply_delivered`
7. `no_reply`
8. `failed`
9. `timed_out`

Feedback transitions:

- when the message is `accepted`, apply acknowledgement feedback if policy
  allows it
- when runtime recovery crosses the configured delay threshold, send the
  configured automatic status message once
- when the runtime returns `deliver_message`, post the final reply and finalize
  acknowledgement cleanup
- when the runtime returns `no_reply`, post no final reply and finalize
  acknowledgement cleanup
- when the runtime returns `hard_error`, apply the configured error behavior and
  finalize acknowledgement cleanup
- when the gateway times out, apply the configured timeout behavior and finalize
  acknowledgement cleanup

This means reactions and automatic status messages use the same lifecycle. They
are different renderings of delivery feedback, not separate product features.

## Slack Mapping

For Slack, acknowledgement reactions should be implemented by the Slack gateway
because the gateway receives the original message event and owns the Slack bot
token.

Slack mapping:

- source conversation: Slack channel id
- source message: Slack event `ts`
- acknowledgement reaction: `reactions.add`
- acknowledgement cleanup: `reactions.remove`
- automatic status message: `chat.postMessage`
- final assistant reply: existing Slack reply path

Slack requirements:

- the app must have `reactions:write` to add and remove reactions
- existing Slack installs must be reauthorized before reaction feedback works
- reaction failures such as missing scope, already-reacted, or no-reaction must
  be logged but must not block runtime delivery
- gateway retries must be idempotent
- gateway-routed runtimes must not expose direct Slack, Discord, Teams, or other
  provider-channel send tools

Slack should prefer reaction names such as `eyes` in stored policy. The Slack
adapter can normalize aliases and provider syntax at the edge.

## Deferred Agent-Authored Reactions

Automatic acknowledgement reactions and agent-authored reactions are separate
capabilities. This plan implements only automatic acknowledgement reactions.

Automatic acknowledgement reactions are gateway control flow. The gateway adds
and removes them because a message was accepted for delivery. The runtime should
not need to decide whether to send these reactions.

Agent-authored reactions are explicit user-requested channel actions. They are
not part of the current implementation because the first production requirement
is reliable gateway-owned delivery feedback.

A future implementation may add a narrow Spritz-owned action such as
`react_to_channel_message`. That future action must still keep provider
credentials at the provider adapter edge:

- the runtime chooses an action such as `react`
- the provider adapter validates the action against channel policy and scopes
- the provider adapter maps the action to the provider API
- the provider adapter reports success or failure back to the runtime

For Slack, this requires:

- a provider action/tool that can add and remove reactions
- access to the target message reference, normally Slack channel id and message
  timestamp
- `reactions:write` on the Slack app installation
- clear error reporting when the bot is not allowed to react

Do not implement this by enabling OpenClaw's generic Slack, Discord, Teams, or
other provider-channel integrations in a Spritz gateway-routed runtime. Those
integrations are for deployments where OpenClaw owns the provider connection
directly. In Spritz gateway mode, they give the model a direct provider-send
path that cannot work without provider tokens in the runtime pod.

The important boundary is:

- automatic delivery feedback is gateway-owned
- future explicit user-requested reactions must be narrow Spritz-owned actions,
  not generic provider-channel tools

## Runtime Boundary

The runtime should not be asked to add automatic acknowledgement reactions or
send automatic gateway status messages.

The runtime should emit normal assistant output through ACP or another runtime
protocol. In gateway-routed mode, it should not be given direct provider-channel
send tools for Slack, Discord, Teams, or any future chat provider where Spritz
owns delivery.

Gateway prompts should carry the same boundary as an explicit runtime
instruction:

```text
Spritz channel gateway will deliver your visible reply. Reply by returning
normal assistant text over ACP. Do not call Slack, Discord, Teams, or other
provider-channel send tools.
```

This instruction is a guardrail, not the only enforcement layer. Runtime config
must still hide or disable direct provider-channel send tools in gateway-routed
deployments.

Spritz maps the typed runtime outcome to provider delivery behavior:

```json
{
  "type": "deliver_message",
  "message": "Visible assistant reply."
}
```

```json
{
  "type": "no_reply",
  "reason": "empty_visible_output"
}
```

```json
{
  "type": "hard_error",
  "publicMessage": "Something went wrong while processing that request."
}
```

Provider feedback must be finalized for all three outcomes.

## OpenClaw Compatibility

OpenClaw has its own channel integrations and may support status reactions when
OpenClaw directly owns the provider connection.

That should remain separate from the Spritz gateway path.

When Spritz receives Slack events and relays prompts to an OpenClaw-backed
runtime, Spritz owns the Slack UX. OpenClaw should receive a clean runtime
prompt and should not need Slack credentials, Slack timestamps, or Slack scopes.
Direct OpenClaw Slack, Discord, Teams, and similar provider-channel integrations
must be disabled or hidden for these gateway-routed runtimes.

Where practical, Spritz should use the same configuration semantics as OpenClaw
for concepts such as acknowledgement reaction scope. That keeps behavior
familiar without coupling Spritz storage or gateway code to OpenClaw internals.

## OpenClaw Defaults In Spritz

Spritz should ship portable OpenClaw defaults for the example OpenClaw image and
for any generated OpenClaw config that Spritz creates while projecting channel
installation config.

The defaults should make gateway-routed Spritz deployments behave well out of
the box without adding deployment-specific values:

```json
{
  "messages": {
    "ackReaction": "👀",
    "ackReactionScope": "group-all",
    "removeAckAfterReply": true,
    "statusReactions": {
      "enabled": true,
      "emojis": {
        "thinking": "👀",
        "tool": "👀",
        "coding": "👀",
        "web": "👀",
        "done": "👀",
        "error": "👀",
        "stallSoft": "👀",
        "stallHard": "👀",
        "compacting": "👀"
      }
    }
  }
}
```

These defaults are safe for Spritz because they are:

- provider-neutral OpenClaw settings
- free of tenant, organization, workspace, channel, and user IDs
- only defaults, so deployments can override them through `OPENCLAW_CONFIG_JSON`,
  `OPENCLAW_CONFIG_B64`, or `OPENCLAW_CONFIG_FILE`
- compatible with later channel-policy projection, which should merge channel
  rules without dropping default message feedback settings
- avoid direct provider-channel send tools in gateway-routed runtimes

This does not change the ownership rule for Spritz-managed Slack installs. When
Spritz receives the Slack event, Spritz still owns Slack-visible delivery
feedback. Provider-owned OpenClaw deployments may still use OpenClaw's native
channel integrations, but that is a different mode from Spritz shared-gateway
delivery.

## Storage Ownership

Spritz should define the portable API and config semantics. The deployment that
owns channel installations should persist the effective policy on the durable
installation or channel-policy record.

The gateway may also need durable per-message delivery records when feedback
must survive retries or gateway restarts. That storage should belong to the
gateway deployment or shared channel delivery store, not to runtime instance
objects.

Recommended durable records:

- installation or channel config: stores desired feedback policy
- message delivery record: stores one accepted inbound provider message
- feedback artifact record: stores provider-visible artifacts such as reaction
  state or status-message ids

Provider auth stays with the gateway. Runtime objects should not store provider
tokens or provider-visible feedback artifacts.

## Implementation Plan

### Phase 1 - Slack acknowledgement reactions

- add `reactions:write` to Slack app scopes
- add Slack gateway helpers for `reactions.add` and `reactions.remove`
- apply acknowledgement reaction after inbound message acceptance
- remove acknowledgement reaction on all terminal outcomes when configured
- ignore non-fatal reaction API errors without hiding them from logs and metrics
- add tests for success, already-reacted, no-reaction, missing-scope, no-reply,
  hard-error, and timeout paths
- disable or hide direct Slack, Discord, Teams, and similar provider-channel
  integrations in gateway-routed Zeno/OpenClaw runtimes

### Phase 2 - Shared delivery feedback controller

- extract acknowledgement and status-message lifecycle into one gateway-owned
  controller
- make provider adapters implement small feedback operations
- keep policy evaluation shared and provider-neutral
- add idempotency around provider-visible artifacts

### Phase 3 - Durable delivery feedback

- persist accepted inbound messages and feedback artifact state
- resume incomplete feedback cleanup after gateway restart
- add metrics for accepted, running, replied, no-reply, failed, timed-out, and
  feedback-failed states

### Phase 4 - Provider expansion

- add Discord mapping using the same policy
- add Teams mapping using supported provider features
- expose installation-level feedback settings in the Spritz React UI

## Validation

Implementation should be considered complete only when these cases pass:

- Slack message receives the configured acknowledgement reaction after gateway
  acceptance
- Slack acknowledgement reaction is removed after a normal final reply
- Slack acknowledgement reaction is removed after `no_reply`
- Slack acknowledgement reaction is removed after timeout or hard error when
  `removeAfterReply` is enabled
- reaction add failure does not prevent the runtime prompt
- reaction remove failure does not duplicate final replies
- automatic status messages are posted only for configured delay/error states
- the runtime cannot cause provider feedback through model text alone
- existing installations without feedback config keep current behavior

## Follow-Ups

- decide whether delivery feedback policy belongs only at installation scope or
  can also be overridden per channel
- define the exact management API response shape that returns effective
  delivery feedback policy
- add a React settings surface for feedback policy after the shared config model
  is implemented
- decide which provider-visible feedback errors should be surfaced in the
  management UI versus logs only
- design a future Spritz-owned, scoped provider-action surface for explicit
  agent-authored reactions without exposing generic provider-channel send tools
