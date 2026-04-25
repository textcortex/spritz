---
date: 2026-04-24
author: Onur Solmaz <onur@textcortex.com>
title: Channel Install Message Policy Config
tags: [spritz, channel-gateway, slack, install, config, openclaw, architecture]
---

## Overview

This document defines how a workspace-level channel installation can allow
selected channels to relay normal messages without requiring a bot mention.

The immediate use case is Slack:

- the Slack app remains installed at workspace scope
- one or more Slack channels are configured with `requireMention: false`
- messages in those channels are relayed without tagging the bot account
- other workspace channels continue to require a mention

The design keeps Spritz generic. The setting is modeled as installation config,
not as deployment-specific target selection and not as organization-specific
state.

Related docs:

- [Channel Install Target Selection Architecture](2026-04-17-channel-install-target-selection-architecture.md)
- [Channel Install Ownership and Management Architecture](2026-04-17-channel-install-ownership-and-management-architecture.md)
- [Slack Channel Gateway Implementation Plan](2026-03-24-slack-channel-gateway-implementation-plan.md)
- [Shared Channel Concierge Lifecycle Architecture](2026-03-31-shared-channel-concierge-lifecycle-architecture.md)

## Problem

Workspace installs are useful because one provider installation can route
messages for a whole external tenant, such as one Slack workspace.

However, message delivery policy is not always workspace-wide. A deployment may
want only specific channels to behave as always-on relay channels, while all
other channels still require an explicit bot mention.

If this setting is encoded in install target selection, the model becomes
incorrect:

- the selected target did not change
- the Slack workspace install did not change
- only provider message policy for selected channels changed

Spritz therefore needs a separate installation-config surface.

## Core Decision

Store channel mention policy as mutable installation config on the durable
channel installation record.

The route identity remains unchanged:

- `principalId`
- `provider`
- `externalScopeType`
- `externalTenantId`

For Slack workspace installs, `externalTenantId` is the Slack team or workspace
ID. The installation can then carry message policy for channels inside that
workspace.

## Config Shape

The generic installation config should use provider-owned external IDs, not
deployment-owned concepts.

Recommended v1 shape:

```json
{
  "channelPolicies": [
    {
      "externalChannelId": "C1234567890",
      "requireMention": false
    }
  ]
}
```

Field meanings:

- `channelPolicies`: message handling rules for external provider channels.
- `externalChannelId`: the provider's stable channel ID.
- `requireMention`: whether normal channel messages must mention the bot before
  Spritz relays them.

Default behavior:

- if no matching channel policy exists, `requireMention` is `true`
- explicit app mentions continue to work
- direct messages keep their existing behavior

The config can be extended later without changing the route identity:

```json
{
  "channelPolicies": [
    {
      "externalChannelId": "C1234567890",
      "externalChannelType": "channel",
      "requireMention": false
    }
  ]
}
```

## What This Is Not

This setting must not be stored in `presetInputs`.

`presetInputs` selects the deployment-owned target that backs the installation.
Installation config controls mutable provider behavior for that installation.

Pinned split:

- route identity determines the shared app and external tenant
- `presetInputs` determines the target behind that installation
- installation config determines provider message behavior

This keeps the design organization-agnostic. Spritz only stores provider IDs
and generic message policy. The deployment remains free to decide what the
selected target means.

## Storage Ownership

The deployment that owns channel installations should persist the config on the
same durable installation object that already stores route identity,
ownership, provider auth, and `presetInputs`.

Spritz defines the API contract and semantics. The deployment stores the data.

That means:

- Spritz owns the install-management UX and generic API shape
- the deployment owns the database table and authorization checks
- Spritz and the channel gateway consume the effective config
- OpenClaw receives a projected runtime config during concierge boot

Existing installs should default to empty config, which preserves current
mention-required behavior.

## API Contract

Add a generic install-management operation for config updates.

Recommended operation:

- `channel.installation.config.update`

`installationId` is a deployment-backend-assigned opaque ID. Spritz should
receive it from the installation-management API, render it in UI URLs when
needed, and send it back unchanged. Spritz must not generate it, parse it, or
assume any specific prefix or ID-generation scheme.

Recommended request shape:

```json
{
  "installationId": "opaque-installation-id",
  "installationConfig": {
    "channelPolicies": [
      {
        "externalChannelId": "C1234567890",
        "requireMention": false
      }
    ]
  }
}
```

Recommended response shape:

```json
{
  "installation": {
    "id": "opaque-installation-id",
    "provider": "slack",
    "externalScopeType": "workspace",
    "externalTenantId": "T1234567890",
    "installationConfig": {
      "channelPolicies": [
        {
          "externalChannelId": "C1234567890",
          "requireMention": false
        }
      ]
    }
  }
}
```

Management list responses should also include `installationConfig` so Spritz can
render current channel policy.

Validation rules:

- the caller must be allowed to manage the installation
- `externalChannelId` must be non-empty
- `requireMention` must be boolean
- duplicate channel policies should be rejected or normalized
- provider-specific ID validation may be applied by the deployment
- deployments may cap the number of channel policies per installation

Reinstall should preserve existing installation config unless the reinstall
request explicitly changes it through a config-management operation.

## Session Exchange

The channel gateway needs access to effective install config before deciding
whether a normal channel message can be relayed without a mention.

Recommended v1 behavior:

- session exchange returns the workspace installation config
- the gateway caches it by provider tenant ID for a short TTL
- each event checks the current channel against `channelPolicies`

For Slack this means the gateway can receive normal `message` events for a
workspace install, then decide locally:

```text
if event is app_mention:
  relay

if event is normal message and channel policy says requireMention=false:
  relay

if event is normal message and channel policy is missing:
  require bot mention
```

The gateway must keep existing safety checks:

- ignore bot messages
- ignore unsupported Slack message subtypes
- ignore empty prompts after mention stripping
- dedupe by Slack team, channel, and timestamp

The dedupe rule is important because Slack can surface both a normal message
event and an app mention event for the same user message.

## OpenClaw Projection

The stored installation config is the source of truth. OpenClaw config is a
runtime projection of that source of truth.

When concierge is created, recovered, or reconciled, Spritz should project the
same channel policies into the OpenClaw config passed to the runtime.

Example projection:

```json
{
  "channels": {
    "slack": {
      "channels": {
        "C1234567890": {
          "allow": true,
          "requireMention": false
        }
      }
    }
  }
}
```

The exact OpenClaw JSON should match OpenClaw's config schema. Spritz should
perform that mapping at the runtime adapter or preset boundary, not in the
generic installation-management API.

## Reconciliation

Config changes must affect the desired concierge state.

If `installationConfig` changes, the binding or rollout desired state must
change too. Otherwise the database can be updated while the running concierge
keeps old OpenClaw config.

Recommended implementation:

- store deterministic JSON for installation config
- compute a config hash or revision
- include that hash in the desired binding or rollout input
- reconcile the concierge when the hash changes

This makes config changes behave like other desired runtime changes.

## Failure Behavior

The default must fail closed.

Rules:

- missing config means mentions are required
- invalid config should be rejected before save
- if config cannot be loaded during message handling, require a mention
- if OpenClaw projection fails, concierge rollout should fail rather than start
  with broader message access than intended

## Implementation Notes

Recommended platform changes:

- add an `installation_config` JSON/Text field to the durable channel
  installation model
- add config serialization to installation list and detail responses
- add the config update operation
- pass installation config into binding creation or reconciliation
- include config revision in desired state

Recommended Slack gateway changes:

- include installation config in session exchange response or add a lightweight
  config lookup
- cache config by Slack team ID with a short TTL
- allow unmentioned normal channel messages only when the matching channel
  policy has `requireMention: false`
- keep mention-required behavior as the default

Recommended Spritz runtime changes:

- accept installation config separately from `presetInputs`
- project relevant channel policies into OpenClaw config during concierge boot
- reconcile runtime config when installation config changes

## Test Plan

Backend tests:

- saves and returns installation config
- rejects invalid channel policies
- preserves config across reinstall
- keeps config separate from `presetInputs`
- changes desired binding state when config changes

Slack gateway tests:

- relays unmentioned message in configured channel
- ignores unmentioned message in unconfigured channel
- still relays explicit app mention
- dedupes duplicate `message` and `app_mention` delivery for the same Slack
  timestamp
- ignores bot messages in configured channels

Runtime tests:

- concierge boot includes configured `requireMention: false` channels in
  OpenClaw config
- config changes trigger reconcile or rollout
- missing config preserves mention-required behavior
