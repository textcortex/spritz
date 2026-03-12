---
date: 2026-03-12
author: Onur Solmaz <onur@textcortex.com>
title: Workspace Transition and Chat Loading UX
tags: [spritz, ui, ux, acp, chat]
---

## Overview

This document defines the production target UX for Spritz workspace state transitions, especially when a user opens a chat URL before the workspace is fully ready.

The core rule is simple:

- one workspace route must always represent one workspace
- the UI must never reuse another workspace's chat state to fill that route
- a chat URL must be valid before the workspace is chat-ready
- non-ready states must render as explicit loading or failure states, not as missing UI or stale transcript state

This document is the source of truth for the ACP chat route, list actions, and workspace transition behavior.

## Problem Statement

The bad UX we need to eliminate is:

- a user opens `#chat/<workspace>` while that workspace is still provisioning
- the UI cannot find an ACP-ready agent for that workspace yet
- instead of staying anchored to the requested workspace, it falls back to a previously ready agent or stale chat state
- the user sees the wrong transcript or a misleading empty chat

This is incorrect for both product semantics and user trust.

A workspace route must never silently switch to a different workspace just because the requested one is not ready yet.

## UX Principles

### Route identity is stable

For chat routes, the URL is the identity anchor.

- `#chat/<workspace>` means: show the chat surface for that workspace
- `#chat/<workspace>/<conversation>` means: show that exact conversation for that workspace

If the workspace is not ready, the route still remains valid. The UI should show the workspace state, not redirect elsewhere.

### One route, many valid states

A chat route is not only valid in the fully ready state.

A workspace chat route may be in one of these user-visible states:

- `missing`
- `provisioning`
- `preparing-chat`
- `ready`
- `failed`

The UI must render a deliberate shell for each state.

### No borrowed transcript state

The transcript shown in the chat panel must belong only to the currently selected workspace and conversation.

The UI must never:

- redirect to a different ready workspace just to show content
- hydrate a cached transcript from another workspace into the current route
- preserve a previous chat pane visually when the route target is not chat-ready

### Chat action must stay visible

The list view must not make chat seem to disappear during transitions.

Instead of hiding the chat action when ACP is not ready, the UI should keep a route to the chat page visible with a stateful label.

Examples:

- `Chat`
- `Preparing chat…`
- `Starting…`
- `Chat status`

This keeps the user anchored on the correct next step.

## Workspace State Model

The chat route should derive its state from the selected workspace.

### `missing`

The workspace does not exist or is not visible to the current user.

UI behavior:

- keep the route on the requested workspace
- render a not-found shell
- do not redirect to any other workspace
- disable conversation creation and composer
- allow the user to navigate back

### `provisioning`

The workspace exists but is not yet `Ready`.

Typical signals:

- `status.phase != Ready`
- status message such as `waiting for deployment`

UI behavior:

- render a pending shell for that workspace
- show the workspace name and current status message
- keep the `Open workspace` action if a workspace URL exists
- hide conversation content and composer state
- poll for readiness automatically

### `preparing-chat`

The workspace is `Ready`, but ACP is not yet ready.

Typical signals:

- `status.phase == Ready`
- `status.acp.state != ready`

UI behavior:

- render a pending shell
- message should explain that the workspace is ready but chat services are still starting
- do not show another workspace's conversation list or transcript
- keep polling automatically

### `ready`

The workspace is fully chat-ready.

Typical signal:

- `status.acp.state == ready`

UI behavior:

- show conversation list
- allow conversation bootstrap
- allow composer input
- hydrate only the selected conversation's cached transcript

### `failed`

The workspace or chat path failed to come online.

Typical signals:

- `status.phase == Failed`
- `status.phase == Error`
- explicit failure status message

UI behavior:

- render a failure shell for that workspace
- preserve route identity
- expose `Open workspace` if it exists
- do not show stale transcript state

## List View Behavior

The spritz list is the first place users understand system state, so it must expose transitions clearly.

### Chat action

Every workspace row should expose a chat entry point when a workspace name exists.

Behavior by state:

- `ready`: label `Chat`
- `preparing-chat`: label `Preparing chat…`
- `provisioning`: label `Starting…`
- `failed`: label `Chat status`

Clicking any of these should route to `#chat/<workspace>`.

The route itself is responsible for rendering the correct state shell.

### Terminal action

Terminal remains tied to workspace readiness.

- terminal may stay disabled until `phase == Ready`
- terminal behavior is separate from chat readiness

### Open action

The workspace open URL may still be useful before chat is ready.

- keep it available whenever the backend provides a workspace URL

## Chat Page Behavior

### Initial load

When the chat route loads:

1. resolve the requested workspace from the route
2. ask for ACP-ready agents
3. if the requested workspace is already ACP-ready, load it normally
4. otherwise fetch the requested workspace directly
5. render the requested workspace's state shell
6. never redirect to another workspace just because another one is ready

### Polling

For `provisioning` and `preparing-chat` states:

- poll the workspace state on a short interval
- keep the user on the same route
- upgrade the page into the normal ready chat experience as soon as ACP becomes ready

Polling should be lightweight and cancel when the page is destroyed.

### Conversation list and composer

For all non-ready states:

- clear selected conversation
- clear active ACP runtime client
- disable conversation creation
- disable composer input
- do not show any cached transcript as if it belongs to that state

### Transition into ready state

When the workspace becomes ACP-ready:

- keep the route unchanged
- re-render the agent selection and thread list
- bootstrap or select a conversation only after the workspace is actually ready
- only then allow transcript hydration and ACP websocket connection

## Cache Rules

Cached transcript state is an optimization only.

For workspace transition UX, the rules are:

- cached transcript may only hydrate when the selected workspace is chat-ready
- cached transcript must belong to the selected conversation id
- cached transcript must never appear on a non-ready workspace route
- changing routes to another workspace must destroy the current ACP runtime and clear visible transcript state immediately

## API and Data Requirements

The first slice of this UX does not require a new backend API.

Existing endpoints are sufficient:

- `GET /api/spritzes`
- `GET /api/spritzes/:name`
- `GET /api/acp/agents`
- `GET /api/acp/conversations?...`
- `POST /api/acp/conversations`
- `POST /api/acp/conversations/:id/bootstrap`

The important rule is ownership of state interpretation:

- `GET /api/spritzes/:name` is the source of truth for workspace existence and phase
- `GET /api/acp/agents` is the source of truth for chat readiness

## Visual Direction

The loading state should feel intentional, not like a blank or broken chat.

Requirements:

- preserve the full chat shell layout
- keep sidebar, header, and main pane stable
- show a centered pending card in the main pane
- keep text concise and explicit
- show the workspace name in the main header
- use state-specific copy instead of generic errors

The desired feel is:

- stable route identity
- stable layout
- dynamic status inside the content area

Not:

- jumping between agents
- blank panels
- hidden actions
- recycled transcript state

## Implementation Plan

### Step 1: Route-state owner

Create one owner in the ACP page for the selected workspace state.

It must:

- resolve the requested workspace from the route
- distinguish ready vs non-ready workspace state
- fetch direct workspace details when ACP-ready agent data is not available yet
- keep the route pinned to the requested workspace

### Step 2: Non-ready chat shell

Add a dedicated non-ready rendering path for:

- missing
- provisioning
- preparing-chat
- failed

This shell must replace conversation content and composer state.

### Step 3: Stateful chat action in the list

The list should surface chat state transitions by label instead of hiding the chat action.

### Step 4: Safe polling

Add polling for non-ready workspace states with these constraints:

- no polling after page destroy
- no polling once the workspace becomes ready
- no transcript reuse while polling

### Step 5: Cache discipline

Ensure cached transcript hydration only happens for ready conversations on the selected workspace.

### Step 6: Regression tests

Add tests for:

- explicit chat route on a provisioning workspace
- explicit chat route on a missing workspace
- list action label for non-ready chat state
- no redirect to another ready workspace
- no client bootstrap while the workspace is not ready

## Validation

A correct implementation should satisfy all of the following:

- opening `#chat/<workspace>` for a provisioning workspace shows a loading shell for that workspace
- the route does not redirect to a different ready agent
- the previous transcript is not shown while the new workspace is still starting
- once ACP becomes ready, the same route turns into a usable chat screen
- the list view always exposes a visible chat entry point with a stateful label

## Non-goals

This document does not define:

- a new backend API surface
- conversation transcript persistence changes
- terminal UX
- workspace creation form UX

Those are separate concerns.

## References

- `/Users/onur/repos/spritz/docs/2026-03-09-acp-port-and-agent-chat-architecture.md`
- `/Users/onur/repos/spritz/docs/2026-03-10-acp-conversation-storage-and-replay-model.md`
- `/Users/onur/repos/spritz/docs/2026-03-10-acp-adapter-and-runtime-target-architecture.md`
