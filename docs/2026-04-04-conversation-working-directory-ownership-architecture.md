---
date: 2026-04-04
author: Onur Solmaz <onur@textcortex.com>
title: Conversation Working Directory Ownership Architecture
tags: [spritz, acp, conversation, cwd, architecture]
---

## Overview

This document defines the production ownership model for conversation working
directories in Spritz.

Goal in plain English:

- keep the runtime or instance as the owner of the default working directory
- keep conversations responsible only for explicit user overrides
- keep the browser UI out of default resolution
- make reconnect and bootstrap behavior consistent across all clients

The current model lets a saved conversation `cwd` behave like both:

- an inherited instance default
- a user-selected override

That conflation makes old values sticky and lets stale client state override a
correct runtime default.

Spritz should converge on a model with three distinct concepts:

- instance default working directory
- conversation override working directory
- effective working directory

The server should resolve the effective value. Clients should render it and may
request an override, but they should not invent or replay defaults on their
own.

## Problem

Today Spritz stores `spec.cwd` on `SpritzConversation` and uses it in multiple
places:

- conversation create and update accept `cwd`
- ACP bootstrap passes `conversation.Spec.CWD` into `session/new` and
  `session/load`
- the browser ACP client passes `conversation.spec.cwd` into `session/load`

That creates one ambiguous field with two incompatible meanings:

- "what this instance should default to"
- "what this conversation explicitly overrode"

This causes several failure modes:

- a stale conversation value can override a correct runtime default
- reconnect behavior may differ across clients
- changing an instance default does not help conversations that copied the old
  default into `spec.cwd`
- browser state and server state can disagree about the right path
- migration is hard because inherited defaults and real overrides are mixed

The core issue is not that clients may send `cwd`. The issue is that Spritz
does not distinguish default from override.

## Design Principles

### 1. The instance owns the default

The default working directory is runtime configuration.

Examples:

- repo-backed runtime starts in `/workspace/project`
- home-directory runtime starts in `/home/dev`

That value belongs to the instance, preset, or runtime configuration layer.

### 2. The conversation owns only explicit user intent

If a user deliberately changes directory and wants the conversation to reopen
there, that is conversation state.

That value is an override, not a default.

### 3. The server resolves effective state

The server should compute:

`effectiveCwd = conversationOverrideCwd ?? instanceDefaultCwd`

Clients should not reproduce this logic independently.

### 4. Existing sessions own their current runtime state

If a session already exists, the runtime should prefer the persisted session
working directory over any reconnect hint.

Creating a session needs a resolved working directory.

Loading an existing session should not depend on a client replaying the same
directory correctly.

### 5. Spec stores intent, status stores resolved truth

This follows the normal control-plane split:

- `spec`: what the user or caller asked for
- `status` or bootstrap response: what Spritz resolved and is actually using

## Target Model

### Instance default working directory

Each runtime instance should expose a canonical default working directory.

Possible sources:

- preset metadata
- resolved runtime binding data
- runtime environment configuration
- runtime capability metadata if Spritz later exposes it explicitly

Spritz should resolve one canonical `instanceDefaultCwd` before session
creation or repair.

### Conversation override working directory

The conversation should store only an explicit override.

There are two acceptable ways to model this:

1. Pragmatic compatibility model:
   keep `spec.cwd`, but define it as override-only semantics.
2. Clean schema model:
   introduce `spec.cwdOverride` and treat `spec.cwd` as legacy input only.

For the short term, Spritz can keep `spec.cwd` for compatibility and redefine
its meaning. The important part is the semantics, not the field rename.

### Effective working directory

Spritz should compute an effective working directory at bootstrap time and make
it visible to clients.

Recommended surfaces:

- `status.effectiveCwd`
- `bootstrap.effectiveCwd`

If Spritz wants to minimize CRD churn, returning `effectiveCwd` from bootstrap
first is enough to fix reconnect behavior. Publishing it in `status` is still
useful because it makes the resolved state inspectable through standard API
reads.

## Resolution Rules

Spritz should resolve working directory with the following rules.

### Create conversation

- if request `cwd` is absent, create the conversation with no override
- if request `cwd` is present, validate and normalize it, then store it as an
  explicit override

### Update conversation

- if request `cwd` is `null` or empty, clear the override
- if request `cwd` is non-empty, validate and normalize it and store it as the
  override

### Bootstrap and repair

Bootstrap should compute:

`effectiveCwd = normalizedOverrideCwd ?? resolvedInstanceDefaultCwd`

Then:

- if the conversation already has a live session, load it and prefer the
  session's own stored cwd where the runtime can provide it
- if the session is missing and must be recreated, create the replacement
  session with `effectiveCwd`
- return `effectiveCwd` in the bootstrap response

### Reconnect

Clients should use `effectiveCwd` returned by Spritz, not `spec.cwd`, when a
runtime still requires a `cwd` argument during replay.

Longer term, `session/load` should not require callers to resend cwd for an
existing session.

## API Contract

### Conversation API

The conversation API should treat `cwd` as optional override input.

Required behavior:

- no `cwd` means "inherit instance default"
- explicit `cwd` means "use this override"
- empty `cwd` means "clear override"

### Bootstrap API

Bootstrap should become the authoritative source for resolved ACP session
metadata.

It should return:

- `effectiveSessionId`
- `bindingState`
- `effectiveCwd`
- normalized agent info and capabilities

That keeps client reconnect logic simple:

- call bootstrap
- trust bootstrap response
- connect
- do not recompute cwd from local state

### ACP runtime protocol hardening

Spritz should also harden its ACP runtime expectations:

- `session/new` requires a resolved cwd
- `session/load` should use the session's stored cwd when possible
- if `session/load` still accepts `cwd`, treat it as optional compatibility
  input rather than the source of truth

The built-in Codex ACP example already behaves this way for `session/load`: it
replays the stored session and does not need the caller's cwd to resolve it.
Spritz should make that the general expectation for runtimes.

## Migration Strategy

Spritz needs a compatibility path for conversations that currently store
copied defaults.

### Read-time normalization

As a first step, Spritz should normalize legacy values during bootstrap:

- values known to be old inherited defaults
- values equal to the resolved instance default
- empty or whitespace-only values

These should be treated as "no override".

This read-time normalization fixes behavior before any data migration runs.

### Data migration

After read-time normalization exists, Spritz may run a one-time migration that:

- scans existing conversations
- resolves instance defaults
- clears `spec.cwd` when it only mirrors the default
- preserves `spec.cwd` when it is a true explicit override

This migration is optional for correctness once bootstrap normalization exists,
but it improves clarity and reduces future ambiguity.

## Why This Is the Production Model

This split gives each layer one clear responsibility:

- instance or runtime config owns defaults
- conversation resource owns explicit user overrides
- server owns resolved truth
- UI owns presentation and override requests

That gives Spritz the properties a production control plane should want:

- one source of truth for effective state
- consistent behavior across browser, CLI, and future clients
- safer migrations when runtime defaults change
- less coupling between UI implementation details and runtime behavior
- better observability because resolved state is visible in one place

## Validation

The design should be considered correct when all of the following are true:

- a new conversation with no override starts in the instance default
- an explicit override persists across refresh, reconnect, and second-client
  access
- reconnect does not require the browser to rediscover or replay the default
  cwd correctly
- changing an instance default affects only conversations without overrides
- repairing a missing ACP session recreates it with the same effective cwd
- old copied defaults no longer override the current instance default

## Recommended Implementation Order

### Phase 1

- change bootstrap to resolve and return `effectiveCwd`
- normalize copied defaults to "no override" during bootstrap
- update the UI to consume `effectiveCwd` instead of `spec.cwd`

### Phase 2

- expose `status.effectiveCwd` on the conversation resource
- update create and update semantics so `cwd` clearly means override-only

### Phase 3

- harden runtime expectations so `session/load` does not depend on client cwd
- optionally migrate existing conversation data to clear inherited defaults

## References

- `docs/2026-03-09-acp-port-and-agent-chat-architecture.md`
- `docs/2026-03-10-acp-adapter-and-runtime-target-architecture.md`
- `docs/2026-03-10-acp-conversation-storage-and-replay-model.md`
- `api/acp_conversations.go`
- `api/acp_bootstrap.go`
- `ui/src/lib/acp-client.ts`
