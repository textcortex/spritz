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
- route state such as `ready` or `disconnected`

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

### Disconnect or uninstall

1. User removes the Slack app or the deployment explicitly disconnects it.
2. Gateway calls the external disconnect API for the same routing identity.
3. Route resolution must immediately stop returning a concierge for that
   workspace.
4. The concierge instance may remain for later reuse, depending on deployment
   policy.

## Inbound Event Flow

### Slack ingress

The shared Slack channel gateway should expose dedicated Slack ingress
surfaces for:

- OAuth callback
- Events API
- interactive payloads when the app enables buttons or modals

Phase 1 can keep the event set narrow:

- app mentions
- direct messages
- message events that should reach the concierge

### Event handling

1. Slack sends the inbound request to the shared gateway.
2. Gateway verifies the Slack signing secret and request timestamp.
3. Gateway extracts routing identity from the payload:
   - `team_id`
   - event type
   - channel id
   - message ts or thread ts
   - external sender id
4. Gateway resolves the target concierge through `channel.route.resolve`.
5. Gateway forwards a normalized inbound event to that concierge instance.

The normalized event should carry at least:

- `provider`
- `externalScopeType`
- `externalTenantId`
- `channelId`
- `threadTs`
- `messageTs`
- `externalSenderId`
- `text`
- raw provider event reference

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
- deduplicate by Slack event id or equivalent request identity
- keep a bounded dedupe window
- ack quickly and hand off longer work asynchronously if needed

### Install retries

- installation upsert must be safe to repeat
- repeated install callbacks for the same workspace must converge on one active
  concierge instance

### Outbound retries

- outbound actions must carry an idempotency key
- gateway should deduplicate repeated send requests for the same concierge
  action

## Threading Defaults

Phase 1 should keep channel behavior predictable:

- direct-message conversations reply inline
- channel conversations reply in thread by default
- if inbound Slack payload already has `thread_ts`, reuse it
- if inbound channel message is not already threaded, use the source message
  `ts` as `thread_ts`

That keeps public channels cleaner and gives the concierge a consistent reply
target.

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

## Follow-ups

- Add Discord and Teams gateway plans once the Slack path is validated.
- Decide whether the Slack gateway should be its own binary or a dedicated
  module inside a shared channel-gateway service.
- Add interactive payload handling once message routing is proven stable.
