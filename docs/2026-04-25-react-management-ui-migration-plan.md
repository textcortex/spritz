---
date: 2026-04-25
author: Onur Solmaz <onur@textcortex.com>
title: React Management UI Migration Plan
tags: [spritz, ui, react, slack-gateway, settings, migration]
---

## Overview

Spritz user-facing management pages should be served by the first-party React
UI under `ui/`.

The Slack gateway currently renders several HTML pages directly from Go
templates. That was useful for a fast admin path, but it creates a second UI
stack with different navigation, styling, routing, form behavior, and
confirmation patterns. It also makes future provider settings harder because
Teams, Discord, and Slack would each be tempted to add their own integration
templates.

This plan migrates the rendered gateway pages to the React app and leaves the
gateway responsible for protocol work: Slack OAuth redirects and callbacks,
Slack event ingestion, health checks, and JSON APIs consumed by the React UI.

## Decision

All new user-facing UI belongs in the Spritz React app.

Gateway and integration services may expose:

- external protocol redirects
- external protocol callbacks
- event ingestion endpoints
- health endpoints
- JSON APIs for the React app

Gateway and integration services should not own product settings pages,
admin pages, channel settings pages, install result pages, or test-message
forms.

## Existing Chat UI Boundary

The existing chat view should change as little as possible.

The migration should add only one settings entry point to the existing chat UI,
such as a settings button or link in the current header or navigation area.
That entry point should navigate to the new `/settings/*` route group.

The settings experience should be its own React view. It should not redesign
the chat page, add a settings sidebar inside the chat surface, or make Slack
management part of the active chat layout. Chat remains focused on agent
conversation. Settings owns integration management.

## Current Server-Rendered Surfaces

The current Slack gateway owns these rendered pages:

| Current gateway route | Current purpose | React destination |
|---|---|---|
| `/slack-gateway/slack/install/select` | Select install target after Slack OAuth | `/settings/slack/install/select` |
| `/slack-gateway/slack/install/result` | Show install success or failure | `/settings/slack/install/result` |
| `/slack-gateway/slack/workspaces` | List manageable Slack workspaces | `/settings/slack/workspaces` |
| `/slack-gateway/slack/workspaces/target` | Change a workspace target | `/settings/slack/workspaces/target?teamId=...` |
| `/slack-gateway/slack/workspaces/test` | Send a test message | `/settings/slack/workspaces/test?teamId=...` |
| `/slack-gateway/settings/channels` | List channel-installation settings | `/settings/slack/channels` |
| `/slack-gateway/settings/channels/installations/:installationId` | List connections for one installation | `/settings/slack/channels/installations/:installationId` |
| `/slack-gateway/settings/channels/installations/:installationId/connections/:connectionId` | Edit channel route policies | `/settings/slack/channels/installations/:installationId/connections/:connectionId` |

The gateway should keep these non-UI protocol routes:

| Gateway route | Reason it remains in gateway |
|---|---|
| `/slack-gateway/slack/install` | Starts Slack OAuth and builds provider-specific state. |
| `/slack-gateway/slack/oauth/callback` | Receives Slack OAuth callback and exchanges provider code. |
| `/slack-gateway/slack/events` | Receives Slack signed events. |
| `/slack-gateway/healthz` | Service health. |

## Target React Route Shape

Add a settings section to the React app:

```text
/settings
/settings/slack
/settings/slack/install/select
/settings/slack/install/result
/settings/slack/workspaces
/settings/slack/workspaces/target?teamId=...
/settings/slack/workspaces/test?teamId=...
/settings/slack/channels
/settings/slack/channels/installations/:installationId
/settings/slack/channels/installations/:installationId/connections/:connectionId
```

The settings section should use shared React layout components:

- settings sidebar
- page header
- status and notice banners
- destructive-action confirmation dialogs
- loading and empty states
- shared table/list and form controls

Slack-specific content can live under a Slack integration section, but the
shell should be generic enough for future providers.

## JSON API Shape

The React UI needs JSON endpoints with the same browser authentication model
as the current gateway pages. The endpoints may live behind the Slack gateway
while the backend contract settles.

Recommended gateway JSON endpoints:

```text
GET  /slack-gateway/api/slack/install/selection
POST /slack-gateway/api/slack/install/selection
GET  /slack-gateway/api/slack/install/result?...

GET  /slack-gateway/api/slack/workspaces
GET  /slack-gateway/api/slack/workspaces/target?teamId=...
POST /slack-gateway/api/slack/workspaces/target
POST /slack-gateway/api/slack/workspaces/test
POST /slack-gateway/api/slack/workspaces/disconnect

GET  /slack-gateway/api/settings/channels
GET  /slack-gateway/api/settings/channels/installations/:installationId
GET  /slack-gateway/api/settings/channels/installations/:installationId/connections/:connectionId
PUT  /slack-gateway/api/settings/channels/installations/:installationId/connections/:connectionId
```

API rules:

- Return JSON only.
- Require the same browser principal checks as the current pages.
- Use opaque backend-assigned IDs exactly as returned by the backend.
- Do not expose Slack tokens or credential material.
- Return `allowedActions` so React can enable or hide controls.
- Return provider display labels from the backend or provider adapter.
- Use structured errors with user-safe messages and request IDs.

## OAuth Flow

The OAuth entry and callback remain gateway-owned.

Target flow:

1. User starts at React: `/settings/slack`.
2. React links to gateway: `/slack-gateway/slack/install`.
3. Gateway redirects to Slack OAuth.
4. Slack calls gateway callback: `/slack-gateway/slack/oauth/callback`.
5. If target selection is needed and gateway APIs are same-origin/proxied with
   React, gateway redirects to React: `/settings/slack/install/select`.
6. React fetches selection data from gateway JSON and submits the selected
   opaque target payload back to gateway JSON.
7. If target selection is needed but the gateway and React are different
   origins, gateway renders the minimal target picker itself so its HTTP-only
   pending-state cookie remains readable by the gateway.
8. Gateway upserts the install and redirects to React result:
   `/settings/slack/install/result?...`.
9. React renders the result page.

The opaque OAuth state stays gateway-owned. The callback stores pending
selection state in a short-lived, HTTP-only gateway cookie scoped to the
selection API. React must not receive, parse, or forward Slack token material
or encrypted OAuth state in the URL.

Exception: when the gateway public URL and the Spritz React URL are different
origins and there is no same-origin `/slack-gateway` proxy, that HTTP-only
gateway cookie cannot be sent by browser calls from the React origin. In that
case the gateway must keep the target picker on the gateway origin as a minimal
protocol fallback. Same-origin/proxied deployments should use the React picker.

## Migration Phases

### Phase 1: JSON API parity

Add JSON endpoints that mirror the current rendered page capabilities.

Required work:

- Add response DTOs for workspace list, target picker, test message result,
  channel installation list, connection detail, and route update result.
- Reuse the existing backend client calls where possible.
- Add tests for browser auth, allowed actions, validation errors, and backend
  error mapping.
- Keep existing rendered pages working during this phase.

Exit criteria:

- Every rendered page can be rebuilt from JSON endpoint data.
- The existing Go template tests still pass.
- New JSON handler tests cover success and failure paths.

### Phase 2: React settings shell

Add the shared settings routes and layout in `ui/`.

Required work:

- Add `/settings` route group.
- Add one minimal settings button or link to the existing chat view.
- Add a settings sidebar with integration navigation.
- Add Slack integration landing and workspace list pages.
- Add API client helpers under `ui/src/lib`.
- Add loading, empty, notice, and error states.
- Add React tests for route rendering and API failure states.

Exit criteria:

- The chat view has no layout redesign beyond the settings entry point.
- React can show the Slack workspace list using JSON endpoints.
- The page uses the shared Spritz UI components and design tokens.
- `pnpm build`, `pnpm typecheck`, and UI tests pass.

### Phase 3: Management actions

Move workspace and channel management actions to React.

Required work:

- Move change-target flow into React.
- Move channel route policy editing into React.
- Move test-message form into React.
- Add confirmation dialogs for disconnect and destructive route removals.
- Add optimistic or explicit reload behavior after successful saves.
- Preserve old URL redirects to the new React routes.

Exit criteria:

- A user can manage a Slack workspace without visiting a Go-rendered page.
- A user can edit `requireMention` channel policy from React.
- A user can send a test message from React.
- Disconnect requires confirmation.

### Phase 4: Install result and target selection

Move install completion pages into React.

Required work:

- Redirect OAuth callback outcomes to React result routes.
- Render all known install result codes in React.
- Move target selection rendering and submission into React.
- Keep OAuth state validation in the gateway.

Exit criteria:

- Slack OAuth install flow starts at React and returns to React.
- Gateway only handles provider OAuth mechanics and JSON submission.

### Phase 5: Remove rendered templates

Retire the Go templates after the React routes are deployed.

Required work:

- Replace old rendered page routes with redirects to React equivalents.
- Keep redirects for bookmarks and stale links.
- Delete template constants and template-only tests after one stable release.
- Keep protocol routes and JSON endpoints.

Exit criteria:

- No user-facing Slack management page is rendered from Go.
- `integrations/slack-gateway` has no product UI templates.
- Existing old URLs land on React pages.

## Acceptance Criteria

- All user-facing Slack management UI is implemented in `ui/`.
- Slack gateway no longer owns product settings pages.
- Existing chat UI changes are limited to a settings entry point.
- React pages share a settings sidebar and common layout.
- Browser auth behavior stays the same or becomes stricter.
- Existing OAuth, event, and routing behavior remains unchanged.
- Old gateway page URLs redirect to React equivalents.
- UI tests cover the main Slack management routes.
- Gateway tests cover JSON endpoint auth, validation, and backend mapping.

## Non-Goals

- Rewriting Slack event handling.
- Moving Slack OAuth code into React.
- Making React handle Slack signed events.
- Redesigning the existing chat UI.
- Changing the provider-agnostic channel installation data model.
- Changing how deployment backends assign opaque installation IDs.
- Removing gateway JSON endpoints before a stable backend product API exists.

## Risks

The main risk is breaking the Slack OAuth callback path while moving result
and target-selection UI. Keep the callback handler gateway-owned and migrate
only the rendered page after JSON parity exists.

The second risk is routing ambiguity between `/slack-gateway/*` and React
routes. Avoid that by putting React settings pages under `/settings/*` and
using explicit redirects from the old gateway page URLs.

The third risk is duplicating backend calls between gateway and React. Keep
React on gateway JSON endpoints at first. Move to a direct product API only
after that API is stable and authenticated for browser use.

## Implementation Checklist

- [ ] Add gateway JSON endpoints for each current rendered page.
- [ ] Add React settings shell and sidebar.
- [ ] Add Slack workspace list page in React.
- [ ] Add Slack workspace target page in React.
- [ ] Add Slack test message page in React.
- [ ] Add Slack channel settings list/detail pages in React.
- [ ] Add install target selection page in React.
- [ ] Add install result page in React.
- [ ] Redirect old gateway page URLs to React routes.
- [ ] Delete rendered templates after one stable release.
