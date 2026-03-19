---
date: 2026-03-12
author: Onur Solmaz <onur@textcortex.com>
title: Instance Transition and Chat Loading UX
tags: [spritz, ui, ux, acp, chat]
---

## Overview

This document defines the production target UX for Spritz instance state transitions, especially when a user opens a chat URL before the instance is fully ready.

The core rule is simple:

- one instance route must always represent one instance
- the UI must never reuse another instance's chat state to fill that route
- a chat URL must be valid before the instance is chat-ready
- non-ready states must render as explicit loading or failure states, not as missing UI or stale transcript state

This document is the source of truth for the ACP chat route, list actions, and instance transition behavior.

## Problem Statement

The bad UX we need to eliminate is:

- a user opens `#chat/<instance>` while that instance is still provisioning
- the UI cannot find an ACP-ready agent for that instance yet
- instead of staying anchored to the requested instance, it falls back to a previously ready agent or stale chat state
- the user sees the wrong transcript or a misleading empty chat

This is incorrect for both product semantics and user trust.

An instance route must never silently switch to a different instance just because the requested one is not ready yet.

## UX Principles

### Route identity is stable

For chat routes, the URL is the identity anchor.

- `#chat/<instance>` means: show the chat surface for that instance
- `#chat/<instance>/<conversation>` means: show that exact conversation for that instance

If the instance is not ready, the route still remains valid. The UI should show the instance state, not redirect elsewhere.

### One route, many valid states

A chat route is not only valid in the fully ready state.

An instance chat route may be in one of these user-visible states:

- `missing`
- `provisioning`
- `preparing-chat`
- `ready`
- `failed`

The UI must render a deliberate shell for each state.

### No borrowed transcript state

The transcript shown in the chat panel must belong only to the currently selected instance and conversation.

The UI must never:

- redirect to a different ready instance just to show content
- hydrate a cached transcript from another instance into the current route
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

## Instance State Model

The chat route should derive its state from the selected instance.

### `missing`

The instance does not exist or is not visible to the current user.

UI behavior:

- keep the route on the requested instance
- render a not-found shell
- do not redirect to any other instance
- disable conversation creation and composer
- allow the user to navigate back

### `provisioning`

The instance exists but is not yet `Ready`.

Typical signals:

- `status.phase != Ready`
- status message such as `waiting for deployment`

UI behavior:

- render a pending shell for that instance
- show the instance name and current status message
- keep the `Open instance` action if an instance URL exists
- hide conversation content and composer state
- poll for readiness automatically

### `preparing-chat`

The instance is `Ready`, but ACP is not yet ready.

Typical signals:

- `status.phase == Ready`
- `status.acp.state != ready`

UI behavior:

- render a pending shell
- message should explain that the instance is ready but chat services are still starting
- do not show another instance's conversation list or transcript
- keep polling automatically

### `ready`

The instance is fully chat-ready.

Typical signal:

- `status.acp.state == ready`

UI behavior:

- show conversation list
- allow conversation bootstrap
- allow composer input
- hydrate only the selected conversation's cached transcript

### `failed`

The instance or chat path failed to come online.

Typical signals:

- `status.phase == Failed`
- `status.phase == Error`
- explicit failure status message

UI behavior:

- render a failure shell for that instance
- preserve route identity
- expose `Open instance` if it exists
- do not show stale transcript state

## List View Behavior

The spritz list is the first place users understand system state, so it must expose transitions clearly.

### Chat action

Every instance row should expose a chat entry point when an instance name exists.

Behavior by state:

- `ready`: label `Chat`
- `preparing-chat`: label `Preparing chat…`
- `provisioning`: label `Starting…`
- `failed`: label `Chat status`

Clicking any of these should route to `#chat/<instance>`.

The route itself is responsible for rendering the correct state shell.

### Terminal action

Terminal remains tied to instance readiness.

- terminal may stay disabled until `phase == Ready`
- terminal behavior is separate from chat readiness

### Open action

The instance open URL may still be useful before chat is ready.

- keep it available whenever the backend provides an instance URL

## Chat Page Behavior

### Initial load

When the chat route loads:

1. resolve the requested instance from the route
2. ask for ACP-ready agents
3. if the requested instance is already ACP-ready, load it normally
4. otherwise fetch the requested instance directly
5. render the requested instance's state shell
6. never redirect to another instance just because another one is ready

### Polling

For `provisioning` and `preparing-chat` states:

- poll the instance state on a short interval
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

When the instance becomes ACP-ready:

- keep the route unchanged
- re-render the agent selection and thread list
- bootstrap or select a conversation only after the instance is actually ready
- only then allow transcript hydration and ACP websocket connection

## Cache Rules

Cached transcript state is an optimization only.

For instance transition UX, the rules are:

- cached transcript may only hydrate when the selected instance is chat-ready
- cached transcript must belong to the selected conversation id
- cached transcript must never appear on a non-ready instance route
- changing routes to another instance must destroy the current ACP runtime and clear visible transcript state immediately

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

- `GET /api/spritzes/:name` is the source of truth for instance existence and phase
- `GET /api/acp/agents` is the source of truth for chat readiness

## Visual Direction

The loading state should feel intentional, not like a blank or broken chat.

Requirements:

- preserve the full chat shell layout
- keep sidebar, header, and main pane stable
- show a centered pending card in the main pane
- keep text concise and explicit
- show the instance name in the main header
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

Create one owner in the ACP page for the selected instance state.

It must:

- resolve the requested instance from the route
- distinguish ready vs non-ready instance state
- fetch direct instance details when ACP-ready agent data is not available yet
- keep the route pinned to the requested instance

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

Add polling for non-ready instance states with these constraints:

- no polling after page destroy
- no polling once the instance becomes ready
- no transcript reuse while polling

### Step 5: Cache discipline

Ensure cached transcript hydration only happens for ready conversations on the selected instance.

### Step 6: Regression tests

Add tests for:

- explicit chat route on a provisioning instance
- explicit chat route on a missing instance
- list action label for non-ready chat state
- no redirect to another ready instance
- no client bootstrap while the instance is not ready

## Validation

A correct implementation should satisfy all of the following:

- opening `#chat/<instance>` for a provisioning instance shows a loading shell for that instance
- the route does not redirect to a different ready agent
- the previous transcript is not shown while the new instance is still starting
- once ACP becomes ready, the same route turns into a usable chat screen
- the list view always exposes a visible chat entry point with a stateful label

## Non-goals

This document does not define:

- a new backend API surface
- conversation transcript persistence changes
- terminal UX
- instance creation form UX

Those are separate concerns.

## References

- `/Users/onur/repos/spritz/docs/2026-03-09-acp-port-and-agent-chat-architecture.md`
- `/Users/onur/repos/spritz/docs/2026-03-10-acp-conversation-storage-and-replay-model.md`
- `/Users/onur/repos/spritz/docs/2026-03-10-acp-adapter-and-runtime-target-architecture.md`
