---
date: 2026-04-24
author: Onur Solmaz <onur@textcortex.com>
title: Channel Installation Data Model
tags: [spritz, channel-gateway, install, data-model, slack, discord, teams, architecture]
---

## Overview

This document defines the long-term data model for shared channel
installations.

This is a logical model and API contract, not a Spritz-owned database
migration plan. Spritz should stay storage-agnostic. The deployment-owned
backend that persists installation state is responsible for creating physical
tables, documents, or any other storage shape through its own migration
system.

The immediate use case is Slack workspace installs where one workspace can
have one or more Spritz-backed connections, and selected channels can be
configured with `requireMention: false`.

The model is intentionally not Slack-specific or deployment-specific. Slack is
the first provider. Discord, Microsoft Teams, and other channel
providers should fit the same shape without adding provider-specific core
entities.

Related docs:

- [Channel Install Target Selection Architecture](2026-04-17-channel-install-target-selection-architecture.md)
- [Channel Install Ownership and Management Architecture](2026-04-17-channel-install-ownership-and-management-architecture.md)
- [Shared Channel Concierge Lifecycle Architecture](2026-03-31-shared-channel-concierge-lifecycle-architecture.md)
- [Shared App Tenant Routing Architecture](2026-03-23-shared-app-tenant-routing-architecture.md)

## Plain Language

A Slack workspace install is not the same thing as a Spritz agent.

The workspace install says, "this Slack workspace has installed this shared
app." A connection under that install says, "messages for this workspace can go
to this internal assistant." A channel route says, "this Slack channel should
use this connection, with this mention policy."

That split lets one Slack workspace have multiple internal connections without
duplicating the provider install or mixing Slack OAuth data with Spritz runtime
state.

## Problem

The current shape treats a provider installation, an internal target, runtime
binding state, and mutable channel policy as one record.

That works for the first narrow case:

- one shared Slack app
- one Slack workspace
- one backing concierge
- one workspace-level mention behavior

It becomes awkward as soon as any of these change:

- one Slack workspace has more than one backing assistant
- a channel should route without requiring a bot mention
- a channel should route to a different assistant from the workspace default
- the same provider install needs to reconnect without changing its internal
  target
- Spritz-specific runtime state needs to change without changing provider
  install identity
- a future provider such as Discord or Teams needs the same installation model

The data model needs to separate those concepts now so the UI and routing
contract do not keep growing one-off fields.

## Goals

- Model provider installations independently from internal assistant
  connections.
- Support one or more connections under one external workspace install.
- Support per-channel routing and mention policy.
- Keep the core logical model provider-agnostic.
- Keep Spritz runtime fields out of the generic channel-installation entity.
- Keep the URL and API model stable by using internal installation and
  connection IDs.
- Avoid deployment-specific names in the core channel schema.

## Non-Goals

- Redesigning provider OAuth.
- Defining provider-specific UI copy.
- Moving all deployment-owned authorization into Spritz.
- Making Spritz understand deployment-specific target types such as agents,
  teams, organizations, or accounts.
- Supporting multiple active connections for the same external channel in v1.
- Having Spritz core own the physical database schema or storage migrations
  for deployment-owned installation state.

## API And Storage Boundary

Spritz should define the generic channel-installation contract.

That includes:

- provider route identity fields
- stable installation, connection, and route IDs
- management API shapes
- UI routing and rendering expectations
- gateway behavior for routing and mention policy

Spritz should not require one specific storage implementation.

The deployment-owned backend should define and create the physical storage
through its normal migration or provisioning system. One deployment may create
relational database tables. Another deployment could store the same logical
entities in a different database or service as long as it satisfies the same
Spritz-facing contract.

The entity names in this document are canonical logical names. The SQL below
is an illustrative relational implementation shape, not a requirement that
Spritz core ships or runs these migrations.

## Naming Decisions

### `provider`

Use `provider` inside the channel-installation domain.

For Slack, the value is:

```text
slack
```

This is already how the gateway and backend contracts identify Slack. In this
domain, `provider` means the external messaging provider, not an LLM provider
or an auth provider.

If an API surface needs extra clarity, it may describe the field as
"messaging provider" or "channel provider" in documentation. The database
column can still be `provider` because the table name gives the field enough
context.

Expected values:

- `slack`
- `discord`
- `msteams`

### No core `targetType`

Do not add `targetType` to the core channel schema.

The core channel system does not need to know whether the internal target is
an agent, workflow, team assistant, or future product concept. That belongs to
the deployment or to a product-specific extension table.

### No core `preset` or `runtime`

Do not put Spritz `preset` or `runtime` fields on the generic installation
entity.

Those are Spritz implementation details:

- `preset` describes how Spritz creates or resolves the backing instance
- `runtime` describes the current live Spritz instance

They should live in a Spritz-specific extension table attached to a generic
channel connection.

### Avoid `scopeType` in the core key

The core model should not require a separate `scopeType` column as part of
the installation identity.

Different providers expose different install surfaces:

- Slack workspace
- Slack enterprise grid
- Discord guild
- Teams tenant
- Teams team

Instead of forcing those into a shared enum, each provider adapter should
produce one stable `externalInstallationKey` for routing. For Slack workspace
installs, that key can be derived from the Slack team ID. A future Teams
adapter can derive a different key without changing the core schema.

The provider-specific facts can still be stored as metadata for display,
debugging, and migration.

## Logical Data Model

The sections below describe logical entities that a deployment must be able to
read and write. A relational deployment can map them to tables with similar
names. A non-relational deployment can use another storage layout as long as
the behavior and uniqueness rules are preserved.

### `channel_installation`

One logical record means one external messaging app installation.

For Slack workspace mode, this is the workspace-level Slack app installation.
It is not an agent and it is not a Spritz runtime.

A relational implementation might use this shape:

```sql
CREATE TABLE channel_installation (
    id                         VARCHAR(32) PRIMARY KEY,
    provider                   VARCHAR(32) NOT NULL,
    principal_id               VARCHAR(128) NOT NULL,
    external_installation_key  VARCHAR(256) NOT NULL,
    external_tenant_id         VARCHAR(256) NULL,
    external_display_name      VARCHAR(512) NULL,
    provider_auth_ref          VARCHAR(512) NULL,
    status                     VARCHAR(32) NOT NULL,
    provider_metadata          JSON NULL,
    installed_by_external_id   VARCHAR(256) NULL,
    created_at                 DATETIME NOT NULL,
    updated_at                 DATETIME NOT NULL,
    deleted_at                 DATETIME NULL,

    UNIQUE KEY uq_channel_installation_route (
        provider,
        principal_id,
        external_installation_key
    )
);
```

Field meanings:

- `id` is the stable product/API ID, for example `ci_...`.
- `provider` is the messaging provider, for example `slack`.
- `principal_id` identifies which shared app or gateway principal owns this
  install route.
- `external_installation_key` is a deterministic provider-adapter key for the
  external install surface.
- `external_tenant_id` is a searchable/displayable provider tenant ID when
  one exists, such as a Slack team ID.
- `external_display_name` caches the workspace, guild, team, or tenant name.
- `provider_auth_ref` points to provider OAuth credentials or another durable
  auth reference.
- `provider_metadata` stores provider-specific facts that should not become
  core columns.
- `installed_by_external_id` stores the provider user ID that performed the
  latest install or reconnect when available.

Example Slack row:

```json
{
  "id": "ci_01k...",
  "provider": "slack",
  "principalId": "shared-slack-gateway",
  "externalInstallationKey": "workspace:T021GRS5F4P",
  "externalTenantId": "T021GRS5F4P",
  "externalDisplayName": "Example Workspace",
  "status": "active"
}
```

### `channel_connection`

One logical record means one internal connection under an external
installation.

This is where "the Slack workspace is connected to this assistant" should
start. A workspace can have more than one connection.

A relational implementation might use this shape:

```sql
CREATE TABLE channel_connection (
    id                  VARCHAR(32) PRIMARY KEY,
    installation_id     VARCHAR(32) NOT NULL,
    display_name        VARCHAR(512) NULL,
    is_default          BOOLEAN NOT NULL DEFAULT FALSE,
    status              VARCHAR(32) NOT NULL,
    created_at          DATETIME NOT NULL,
    updated_at          DATETIME NOT NULL,
    deleted_at          DATETIME NULL,

    FOREIGN KEY (installation_id) REFERENCES channel_installation(id)
);
```

Recommended constraints:

- one active default connection per installation
- soft-deleted connections do not count against the active default constraint
- each connection belongs to exactly one installation

The default connection is used for workspace-level behavior when no explicit
channel route exists.

### `spritz_channel_connection`

One logical record stores Spritz-specific backing data for a generic channel
connection.

This entity is allowed to contain Spritz words because it is no longer part of
the provider-agnostic core model.

A relational implementation might use this shape:

```sql
CREATE TABLE spritz_channel_connection (
    connection_id              VARCHAR(32) PRIMARY KEY,
    preset_id                  VARCHAR(128) NOT NULL,
    preset_inputs              JSON NULL,
    preset_inputs_hash         VARCHAR(128) NULL,
    spritz_binding_key         VARCHAR(256) NULL,
    spritz_instance_id         VARCHAR(256) NULL,
    namespace                  VARCHAR(256) NULL,
    applied_revision           BIGINT NOT NULL DEFAULT 0,
    runtime_binding_assigned_at DATETIME NULL,
    created_at                 DATETIME NOT NULL,
    updated_at                 DATETIME NOT NULL,

    FOREIGN KEY (connection_id) REFERENCES channel_connection(id)
);
```

This is the right logical home for the fields that currently describe Spritz
provisioning and runtime binding. A deployment can store those fields in a
separate table, a nested document, or another equivalent persistence shape.

### `channel_route`

One logical record means one external channel has explicit routing behavior.

For Slack, this maps a Slack channel ID to one connection and one mention
policy.

A relational implementation might use this shape:

```sql
CREATE TABLE channel_route (
    id                   VARCHAR(32) PRIMARY KEY,
    installation_id      VARCHAR(32) NOT NULL,
    connection_id        VARCHAR(32) NOT NULL,
    external_channel_id  VARCHAR(256) NOT NULL,
    external_channel_name VARCHAR(512) NULL,
    require_mention      BOOLEAN NOT NULL DEFAULT TRUE,
    enabled              BOOLEAN NOT NULL DEFAULT TRUE,
    created_at           DATETIME NOT NULL,
    updated_at           DATETIME NOT NULL,
    deleted_at           DATETIME NULL,

    FOREIGN KEY (installation_id) REFERENCES channel_installation(id),
    FOREIGN KEY (connection_id) REFERENCES channel_connection(id),

    UNIQUE KEY uq_channel_route_channel (
        installation_id,
        external_channel_id
    )
);
```

The unique route constraint is intentional. In v1, one external channel should
route to one active connection for a given provider installation. That avoids
two assistants responding to the same unmentioned message.

The implementation must also enforce that `channel_route.connection_id`
belongs to the same `installation_id` as the route. That can be a composite
foreign key, a generated constraint, or a service-layer invariant depending on
the database and migration path.

If the product later needs fan-out or multi-agent rooms, that should be a new
explicit routing mode, not an accidental side effect of duplicate route rows.

## Routing Behavior

Incoming provider events should resolve in this order:

1. The provider gateway computes the provider route:
   - `provider`
   - `principal_id`
   - `external_installation_key`
2. The backend resolves the matching `channel_installation`.
3. If the event has an external channel ID, the backend looks for an enabled
   `channel_route`.
4. If a route exists, the event uses that route's `connection_id` and
   `require_mention`.
5. If no route exists, the event uses the installation's default connection
   and requires a bot mention.
6. If no matching route and no default connection exist, the event is ignored
   or returned as not configured.

For the no-mention Slack use case:

- the Slack app is still installed in workspace mode
- the route row points the selected Slack channel to the desired connection
- `require_mention` is set to `false`
- only that channel wakes the assistant without a bot mention

That keeps channel-level behavior separate from Slack installation mode.

## UI Model

The UI should be installation-scoped first, then connection-scoped.

Recommended routes:

```text
/settings/channels/installations/:installationId
/settings/channels/installations/:installationId/connections/:connectionId
```

The installation page should show:

- provider
- external workspace, guild, team, or tenant name
- install status
- reconnect or disconnect actions
- all connections under the installation

The connection page should show:

- display name
- backing Spritz target summary when the connection is Spritz-backed
- whether it is the default connection
- explicit channel routes
- per-channel `requireMention` policy

Slack-specific labels are fine in the UI when the provider is Slack. The route
shape should still use stable internal IDs instead of Slack team IDs or agent
IDs.

## API Shape

The management API should expose internal IDs as the stable product surface.

Examples:

```http
GET /channel/installations
GET /channel/installations/ci_123
GET /channel/installations/ci_123/connections
POST /channel/installations/ci_123/connections
PATCH /channel/connections/cc_456
GET /channel/connections/cc_456/routes
PUT /channel/connections/cc_456/routes/C0ANJGDB4Q5
DELETE /channel/routes/cr_789
```

The route upsert body can stay generic:

```json
{
  "externalChannelId": "C0ANJGDB4Q5",
  "externalChannelName": "support-triage",
  "requireMention": false,
  "enabled": true
}
```

Provider-specific validation should happen server-side. For example, Slack
channel ID validation belongs to the Slack provider adapter or deployment
backend service, not to a generic UI component.

## Ownership And Authorization

The channel schema should not encode a product-specific ownership taxonomy.

Each deployment still needs to answer:

- who may see an installation
- who may reconnect or disconnect it
- who may create a connection under it
- who may change channel routes and mention policy

Those checks should be enforced by the deployment's normal authorization
system and surfaced to Spritz as server-driven action availability.

The generic channel model only needs stable IDs and enough route state to
resolve provider events.

## Migration From The Current Shape

The existing combined installation row can migrate into the split logical model
in four steps.

1. Create one `channel_installation` row for each existing external route.
   The current provider route fields map into `provider`, `principal_id`, and
   `external_installation_key`.
2. Create one `channel_connection` row for each existing backing concierge.
   Existing single-connection installs should mark that row as default.
3. Move Spritz-specific fields into `spritz_channel_connection`.
   This includes preset inputs, binding key, instance ID, namespace, applied
   revision, and runtime binding timestamps.
4. Expand existing channel-policy config into `channel_route` rows.
   Each current `channelPolicies[]` entry becomes a route row with
   `require_mention`.

During the migration window, the deployment backend can keep serving the old
session exchange response shape by reading from the new storage and projecting
the legacy response.

## Validation

Minimum validation for this model:

- a Slack workspace install creates one `channel_installation`
- the same Slack workspace can have multiple `channel_connection` rows
- exactly one active default connection is allowed per installation
- a channel route points to exactly one connection
- a channel route with `require_mention = false` relays unmentioned messages
  for that channel
- channels without an explicit route keep requiring a bot mention
- reconnect updates provider auth without rewriting Spritz runtime state
- runtime replacement updates `spritz_channel_connection` without rewriting
  provider installation identity
- Discord or Teams can add provider adapters without changing the core logical
  model

## Pinned Decisions

- Keep `provider`; for Slack the value is `slack`.
- Use internal IDs for URLs and management APIs.
- Model provider installation, internal connection, Spritz runtime backing,
  and channel routes as separate concepts.
- Do not add core `targetType`, `preset`, or `runtime` fields.
- Do not require `scopeType` in the core uniqueness key.
- Keep Spritz storage-agnostic; deployment backends create and own the
  physical storage.
- Use one explicit channel route per external channel in v1.
- Treat no-mention behavior as channel route policy, not as Slack install
  mode.
