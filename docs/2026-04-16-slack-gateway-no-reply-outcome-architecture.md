---
date: 2026-04-16
author: Onur Solmaz <onur@textcortex.com>
title: Slack Gateway No-Reply Outcome Architecture
tags: [spritz, slack, channel-gateway, runtime, error-handling, architecture]
---

## Overview

Spritz should treat "the runtime produced no user-visible reply" as a normal,
typed outcome instead of a gateway error.

Today the Slack gateway can collapse this case into a generic public error
message. That is the wrong product behavior. If the runtime produced no visible
reply, the gateway should usually send nothing to Slack.

This document defines the long-term contract for that behavior.

The verified ownership boundary is:

- OpenClaw-specific silent control tokens such as `NO_REPLY` must be consumed
  in the OpenClaw ACP bridge before they become ACP-visible assistant text
- the Slack gateway should stay ACP-generic and only decide between "deliver a
  visible message", "deliver nothing", and "surface a real failure"

Related docs:

- [Slack Channel Gateway Implementation Plan](2026-03-24-slack-channel-gateway-implementation-plan.md)
- [Unified Public Error Architecture](2026-04-03-unified-public-error-architecture.md)
- [OpenClaw Integration](2026-03-13-openclaw-integration.md)

## Problem

The Slack gateway currently has a binary outcome model after it prompts the
conversation runtime:

- reply succeeded and a message is posted to Slack
- prompt path failed and the gateway may post a generic internal error message

That is too coarse.

There is a third real-world outcome:

- the runtime accepted and processed the prompt, but produced no user-visible
  message

This can happen for valid reasons, for example:

- the runtime only emitted internal reasoning or trace material
- the runtime intentionally decided not to answer
- the runtime ended with no assistant text after filtering or normalization
- a future backend supports explicit "no reply" behavior

When that happens, posting a generic Slack error is misleading. Nothing may
actually be broken. The runtime may have completed successfully and simply not
produced deliverable content.

## Goals

- make "no visible reply" a first-class outcome in Spritz
- stop posting false error messages to Slack for that outcome
- keep true runtime or transport failures visible
- make the contract reusable across channel gateways, not Slack-only in spirit
- preserve observability so operators can distinguish `no_reply` from failures

## Non-Goals

- changing model behavior to always emit visible text
- exposing internal reasoning or trace content to end users
- inventing Slack-specific business logic for one backend only
- suppressing genuine runtime, gateway, or transport errors

## Core Decision

Spritz should model delivery after a prompted conversation as three distinct
outcomes:

1. `deliver_message`
2. `no_reply`
3. `hard_error`

The important rule is:

- `no_reply` is not a public error

For Slack, that means:

- `deliver_message`: post the message
- `no_reply`: do not post a message
- `hard_error`: post the generic failure message only when product policy says
  the user should see one

The ownership rule is:

- OpenClaw semantics belong in the OpenClaw ACP bridge
- channel transport semantics belong in the Slack gateway

That means literal `NO_REPLY` handling should not be owned primarily by the
Slack gateway.

## Why This Is the Right Abstraction

The Slack gateway is a delivery adapter. Its job is to:

- send user input to a conversation runtime
- receive the runtime outcome
- map that outcome to Slack delivery behavior

The gateway should not infer that "empty visible output" means failure.

That inference is unsafe because:

- the runtime may have succeeded
- the backend may intentionally support silent completion
- "no visible output" and "internal execution failed" are semantically
  different
- users see a false signal when the adapter converts silence into an error

The clean architecture is to make the runtime outcome explicit, then let the
gateway handle each outcome deterministically.

## Proposed Contract

### Runtime prompt result

The prompt path should return a typed result, not just `(reply, promptSent,
err)`.

Recommended shape:

```json
{
  "type": "deliver_message",
  "message": "Hello from the runtime."
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
  "publicMessage": "I hit an internal error while processing that request."
}
```

The internal representation does not need to match this JSON exactly, but the
typed semantics should.

## Verified Current Behavior

The current system has two different layers:

1. OpenClaw, which has a real silent sentinel token: `NO_REPLY`
2. Spritz Slack gateway, which consumes ACP `agent_message_chunk` updates

Verified facts:

- OpenClaw treats exact `NO_REPLY` as a silent reply token and suppresses it in
  its own delivery layer
- upstream OpenClaw ACP behavior can still forward raw assistant text blocks
  through ACP
- the Spritz OpenClaw ACP wrapper currently forwards assistant text from
  transcript replay as `agent_message_chunk` without stripping `NO_REPLY`
- the Spritz Slack gateway currently treats only empty assembled assistant text
  as `no_reply`

So the current bug is not "Slack needs provider-specific Kimi logic."

The current bug is:

- an OpenClaw-specific silent token can cross the OpenClaw-to-ACP boundary as
  visible assistant text
- once that happens, the Slack gateway has no typed signal telling it that the
  outcome was intentionally silent

### Required fields

- `type`: one of `deliver_message`, `no_reply`, `hard_error`

### Outcome-specific fields

For `deliver_message`:

- `message`: non-empty user-visible text

For `no_reply`:

- `reason`: stable machine-readable reason such as `empty_visible_output`
- optional operator metadata for logs and metrics

For `hard_error`:

- internal cause information for logs
- optional public copy override when the channel should show one

## Slack Delivery Rules

### `deliver_message`

The gateway posts the returned message into the correct Slack thread.

Rules:

- message must be non-empty after final normalization
- this is the only outcome that produces a normal assistant reply post

### `no_reply`

The gateway acknowledges the Slack event and posts nothing.

Rules:

- do not post the generic internal error message
- do not synthesize filler text such as "No response"
- do record structured logs and metrics

This is the key product fix.

### `hard_error`

The gateway handles the failure through the existing public error policy.

Rules:

- only real failures should reach this outcome
- the generic Slack failure message remains acceptable here
- transport failures and runtime execution failures stay visible

## What Counts as `no_reply`

Spritz should classify the following cases as `no_reply` unless product policy
explicitly says otherwise:

- the runtime completed but returned no assistant-visible text
- the runtime output reduced to empty content after normalization
- the runtime emitted internal-only material that the channel adapter cannot
  deliver as a user message
- the runtime explicitly signaled a silent completion outcome

The key test is simple:

- was there a successful prompt execution with no deliverable user-visible
  message?

If yes, the outcome is `no_reply`, not `hard_error`.

## What Does Not Count as `no_reply`

These are still `hard_error`:

- the prompt request could not be sent
- the runtime failed before completing the request
- the session could not be bootstrapped
- the gateway could not resolve the channel session
- the gateway had a real Slack post failure after deciding to deliver a message

That boundary matters because silent suppression is only correct for successful
no-message completions, not real failures.

## Current Gap in the Slack Gateway

The current Slack gateway flow in
[`integrations/slack-gateway/slack_events.go`](/Users/onur/repos/spritz/integrations/slack-gateway/slack_events.go)
still treats part of this space as an error path, and the current OpenClaw ACP
wrapper does not fully normalize OpenClaw silent control output before it
reaches the gateway.

Today, after prompting the runtime:

- if the prompt was sent and the path still returns an error
- the gateway can overwrite the reply with:
  `I hit an internal error while processing that request.`
- then it posts that message back to Slack

That behavior is reasonable for true failures, but wrong for the specific case
where the prompt completed and the only issue is missing visible output.

The implementation gap is not "Slack needs to understand one model provider."
The gap is:

- the OpenClaw ACP bridge does not fully translate OpenClaw silent semantics
  into an ACP-visible no-reply outcome
- the runtime result contract does not cleanly distinguish no visible reply
  from hard failure once the gateway receives the ACP stream

## Recommended Implementation Shape

### 1. Introduce a typed delivery outcome in the prompt path

Refactor the conversation prompt flow so it returns a typed outcome object
instead of relying on a mixed interpretation of:

- reply text
- prompt-sent bookkeeping
- error presence

That keeps the decision at the right layer.

### 1a. Consume OpenClaw silent tokens in the ACP bridge

For the OpenClaw-backed path specifically, the bridge that converts OpenClaw
runtime events into ACP updates should own OpenClaw sentinel handling.

Rules:

- exact silent outputs such as `NO_REPLY` must not be emitted as visible
  `agent_message_chunk` text
- silent-token lead fragments must not leak during streaming
- if OpenClaw produces no deliverable assistant text after normalization, the
  bridge should complete the prompt without a visible assistant message

This keeps OpenClaw-specific knowledge in the OpenClaw integration boundary.

### 2. Centralize empty-visible-output classification

One owning function should decide whether the runtime result is:

- `deliver_message`
- `no_reply`
- `hard_error`

Do not duplicate that logic at multiple Slack callsites.

### 3. Keep Slack posting logic dumb

The Slack gateway should only map typed outcomes to channel behavior:

- post message
- post nothing
- post failure message

This keeps the adapter simple and reusable.

Defense in depth is still acceptable:

- Slack gateway may suppress empty final text
- Slack gateway may optionally guard against exact silent control tokens if
  they somehow slip through

But that guard should not be the primary owner of OpenClaw sentinel semantics.

### 4. Keep structured operator visibility

`no_reply` must still be visible operationally.

Record:

- outcome type
- normalized reason
- conversation ID
- channel ID
- message timestamp
- whether the prompt was accepted

That gives operators evidence without turning silent completions into public
errors.

## Observability

Spritz should track `no_reply` explicitly.

Recommended logs:

- prompt completed with `delivery_outcome=no_reply`
- stable reason such as `empty_visible_output`
- conversation and channel identifiers

Recommended metrics:

- `channel_gateway_prompt_outcomes_total{provider="slack",type="deliver_message"}`
- `channel_gateway_prompt_outcomes_total{provider="slack",type="no_reply"}`
- `channel_gateway_prompt_outcomes_total{provider="slack",type="hard_error"}`

Recommended alerts:

- alert on sustained `hard_error` rate
- do not alert on normal low-volume `no_reply`
- investigate `no_reply` spikes because they may reveal runtime regressions or
  policy mismatches

## Testing Strategy

This behavior needs direct regression coverage.

Required tests:

1. runtime returns normal visible text
   - Slack gateway posts exactly one reply
2. OpenClaw ACP bridge receives exact `NO_REPLY`
   - bridge emits no visible `agent_message_chunk`
   - prompt resolves as a no-reply outcome
3. OpenClaw ACP bridge receives silent-token lead fragments
   - fragments do not leak into visible assistant output
4. runtime completes with empty visible output
   - Slack gateway posts nothing
   - gateway reports success for delivery bookkeeping
5. runtime fails before prompt completion
   - Slack gateway follows the hard-failure path
6. runtime fails after a typed `hard_error`
   - Slack gateway posts the generic error message when configured to do so
7. Slack post fails after `deliver_message`
   - gateway preserves existing retry and deduplication behavior

Important assertion:

- the empty-output case must not post `I hit an internal error while
  processing that request.`

## Interaction With Public Error Policy

This design fits the broader public error architecture.

The public error model should be used when something user-visible failed.
`no_reply` is different:

- it is a valid delivery outcome
- it may still deserve operator visibility
- it does not automatically deserve a user-facing error message

In plain terms:

- no visible answer is not the same thing as a visible failure

## Future Extension

Although Slack is the immediate driver, this should be treated as a shared
channel-gateway contract.

Other adapters may also need to distinguish:

- message to send
- nothing to send
- actual failure

That argues for defining the outcome in the shared conversation delivery layer,
not as Slack-only conditional logic.

## Migration Plan

1. Normalize OpenClaw silent control output in the OpenClaw ACP bridge.
2. Define or preserve a typed no-reply prompt delivery outcome in the prompt
   layer.
3. Update Slack gateway prompt handling to consume the typed result without
   learning OpenClaw-specific sentinel rules.
4. Add regression tests for bridge-level silent handling and gateway-level
   no-reply handling.
5. Add outcome metrics and logs.
6. Reuse the same contract in other channel adapters if and when needed.

## Final Recommendation

The production-ready fix is:

- make `no_reply` a first-class outcome
- consume OpenClaw `NO_REPLY` semantics in the OpenClaw ACP bridge
- classify empty visible output into `no_reply` centrally
- have Slack acknowledge the event and send nothing
- reserve the generic Slack error message for real failures only

That is the smallest clean architecture that fixes the current behavior without
adding provider-specific hacks or hiding genuine failures.
