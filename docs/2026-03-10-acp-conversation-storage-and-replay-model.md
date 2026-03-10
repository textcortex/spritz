---
date: 2026-03-10
author: Spritz Maintainers <user@example.com>
title: ACP Conversation Storage and Replay Model
tags: [spritz, acp, conversation, transcript, architecture]
---

## Overview

This document defines how ACP conversations should be identified, stored, and
restored in Spritz.

The goal is a model that keeps Spritz backend-agnostic, keeps Kubernetes
resources small, and makes conversation restore correct across reconnects,
browser refreshes, and different clients.

## Current State

Spritz currently uses three different identifiers in the ACP chat flow:

1. `SpritzConversation.metadata.name`
2. `SpritzConversation.spec.sessionId`
3. the backend runtime session key used by the ACP implementation

Today:

- the URL uses `SpritzConversation.metadata.name`
- Spritz stores ACP session metadata in `SpritzConversation`
- Spritz API owns the authoritative thread-to-session bootstrap and repair flow
- the ACP backend owns the actual runtime session
- the browser keeps a session-scoped transcript cache so returning to the same
  URL restores the last rendered transcript immediately
- OpenClaw-backed `session/load` replays stored transcript history from backend
  session storage

That browser cache improves user experience, but it is not the source of truth
for transcript restore.

## Identity Model

### Spritz conversation id

`SpritzConversation.metadata.name` is the stable Spritz thread id.

Example:

- `young-crest-c2195b88`

Responsibilities:

- identifies the chat thread in Spritz
- appears in the URL as `#chat/<spritz>/<conversation>`
- keys browser-local transcript cache
- owns thread metadata such as title, owner, workspace, and cwd

### ACP session id

`SpritzConversation.spec.sessionId` is the ACP protocol session id for that
thread.

Responsibilities:

- identifies the ACP session lifecycle
- is passed to `session/load`
- is updated by Spritz when a new ACP session is created

This id is not used as the route id.

### Backend runtime session key

The ACP backend may map the ACP session id to its own runtime session key.

For OpenClaw this is currently a gateway session key derived from the ACP
session id.

Responsibilities:

- identifies the real backend conversation state
- keys backend transcript storage
- remains an implementation detail of the ACP backend

This id must not be used as the Spritz route id.

## Storage Responsibilities

### Spritz stores metadata

Spritz stores conversation metadata in `SpritzConversation`.

It should store:

- spritz name
- owner
- title
- cwd
- ACP `sessionId`
- binding status such as active vs replaced
- normalized agent metadata and capabilities when useful

It should not store:

- full transcript history
- high-churn streamed message state
- backend-specific transcript structures

### ACP backend stores transcript truth

The ACP backend should store the actual conversation history in its own session
store.

That store must be keyed by the backend runtime session identity, not by the
Spritz conversation object name.

For OpenClaw, this means:

- OpenClaw gateway/session storage remains the transcript source of truth
- `session/load` must resolve the backend session from the ACP `sessionId`
- history replay must be built from that backend store

### Browser stores only an acceleration cache

The browser may keep a session-scoped cache of the last rendered transcript.

That cache is allowed only as a user-experience optimization:

- to show the previous thread immediately while backend replay starts
- to survive short route changes within the same browser session

It must not be required for correctness.

## Replay Model

The correct restore flow is:

1. User opens `#chat/<spritz>/<conversation>`
2. Spritz loads `SpritzConversation`
3. Spritz API bootstraps the selected conversation
4. Spritz API reads `spec.sessionId`
5. Spritz API initializes ACP against the selected workspace
6. Spritz API loads the existing ACP session or explicitly repairs it by
   creating a replacement session and patching the conversation record
7. The browser opens the ACP connection for that conversation and sends
   `session/load(sessionId=...)`
8. The ACP backend resolves the real backend session
9. The ACP backend reads stored transcript history
10. The ACP backend replays the transcript through ordered `session/update`
   events
11. The client rebuilds the thread from replayed updates

The browser transcript cache may render first, but the backend replay must be
able to reconstruct the same conversation without relying on that cache.

## Backend Requirements

To make this model correct, the ACP backend must implement `session/load` as a
real replay operation.

Minimum requirements:

- resolve the ACP `sessionId` to the backend session
- read persisted message history from backend storage
- replay historical user and assistant messages in order
- replay tool-call state when the backend can represent it
- replay session metadata updates needed by the client
- keep the replay idempotent and safe across reconnects

For OpenClaw specifically, the expected behavior is:

- map ACP `sessionId` to the OpenClaw gateway session key
- read transcript data from existing gateway session storage
- translate stored history into ACP `session/update` notifications
- emit the replay on `session/load`

## Implemented Flow

The current Spritz implementation now follows this model for OpenClaw-backed
workspaces:

1. Spritz routes by `SpritzConversation.metadata.name`
2. Spritz API bootstraps the selected conversation before the UI connects
3. Spritz API keeps `SpritzConversation.spec.sessionId` as the canonical ACP
   session id for that thread
4. the ACP UI connects through the Spritz API bridge using the conversation id
5. the ACP client sends `initialize` and `session/load` for the confirmed
   session id
6. the Spritz OpenClaw ACP wrapper resolves the OpenClaw gateway session key
   deterministically from the ACP session id
7. the wrapper calls OpenClaw `sessions.get`
8. the wrapper translates stored history into ordered ACP updates
9. the UI replaces any cached transcript with the replayed backend transcript

Concretely, the shipped implementation does the following:

- replays historical user messages as `user_message_chunk`
- replays historical assistant messages as `agent_message_chunk`
- replays tool calls as `tool_call`
- replays tool results as `tool_call_update`
- replays current thinking level as `current_mode_update` when available

This keeps the replay transport ACP-native and does not introduce a
Spritz-specific transcript replay protocol.

## Client Behavior

The client should treat backend replay as canonical.

The client should:

- route by Spritz conversation id
- use `spec.sessionId` only for ACP session operations after API bootstrap
- restore local cached transcript immediately when available
- replace cached transcript state with backend replay once replay begins
- continue to work when no browser cache exists

During bootstrap replay, the client should treat replayed message chunks as
historical completed messages, not as a live typing stream.

The client should not:

- invent transcript history when the backend has none
- depend on browser storage for correctness
- create or replace ACP sessions on its own
- route directly by ACP session id or backend session key

## Why This Split Is Correct

This split keeps each layer responsible for the right thing:

- Spritz owns routing, ownership, and thread records
- the ACP backend owns runtime session state and transcript history
- the browser owns only temporary local rendering state

That keeps Spritz portable across different ACP backends while preserving a
correct restore model.

## Validation

The system should be considered correct when all of the following are true:

- reopening the same URL in the same tab restores immediately
- refreshing the page restores from backend replay even with empty browser
  storage
- opening the same conversation from a second browser/client restores the same
  transcript from backend replay
- transcript restore does not require Kubernetes transcript storage
- route ids remain stable and independent from backend runtime ids

## References

- `docs/2026-03-09-acp-port-and-agent-chat-architecture.md`
- `docs/2026-03-10-acp-adapter-and-runtime-target-architecture.md`
- `docs/2026-02-24-simplest-spritz-deployment-spec.md`
