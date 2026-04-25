---
date: 2026-04-24
author: Onur Solmaz <onur@textcortex.com>
title: Slack Install Modes
tags: [slack, oauth, install, channels, workspace, enterprise]
---

## Overview

Slack apps are normally installed into a workspace or an Enterprise Grid
organization.

People sometimes say "channel install" when they mean one of two different
things:

- the OAuth flow included an incoming webhook channel picker
- the app is already installed in the workspace and is then added to, invited
  into, or configured for a specific channel

Those are different from a normal workspace app install.

Slack's core install model is OAuth-based. The install grants tokens and scopes
to the app for a workspace or organization. Channel behavior is usually a later
authorization, membership, routing, or configuration layer.

Sources:

- [Installing with OAuth](https://docs.slack.dev/authentication/installing-with-oauth)
- [Developing apps for Enterprise orgs](https://docs.slack.dev/enterprise/developing-for-enterprise-orgs)
- [Incoming webhook scope](https://docs.slack.dev/reference/scopes/incoming-webhook)
- [Sending messages using incoming webhooks](https://docs.slack.dev/messaging/sending-messages-using-incoming-webhooks)
- [conversations.join](https://docs.slack.dev/reference/methods/conversations.join/)

## Short Version

Use "workspace install" for the normal Slack app OAuth install.

Use "org-wide install" for Enterprise Grid installs.

Use "incoming webhook channel selection" when OAuth asks the user to choose one
channel and returns a webhook URL for that channel.

Use "channel membership" or "channel configuration" when an already-installed
app is enabled for a channel.

Avoid using "channel install" by itself unless the product defines exactly what
it means.

## Modes

| Mode | What Slack Creates | Main Scope | Use It For | Important Detail |
| --- | --- | --- | --- | --- |
| Workspace OAuth install | Bot token, app installation, optional user token | One workspace | Most Slack apps and bots | The app is installed in the workspace, not automatically in every channel. |
| Enterprise org-wide install | Org-level installation and enterprise identity | Enterprise Grid org, then selected workspaces | Enterprise-wide apps and admin-managed deployments | The OAuth result can be `is_enterprise_install=true`; apps still need to reason about workspace grants. |
| Incoming webhook channel selection | Incoming webhook URL tied to one selected channel | One destination channel for webhook posting | Simple notification-style posting | This is the closest Slack-native thing to a channel install, but it is only for the webhook destination. |
| Channel membership | App/bot joins or is invited to a channel | One Slack conversation at a time | Reading, replying, or participating in a channel | This happens after workspace install. It is not a separate OAuth install. |
| User authorization | User token under `authed_user` | One installing or authorizing user | Acting on behalf of a user or reading user-accessible data | User scopes are separate from bot scopes. Sign in with Slack scopes must use a separate OAuth flow. |
| Single-workspace app install | App installed only into its development workspace | One fixed workspace | Internal tools and development | This is a distribution choice, not a different runtime model. |
| Distributed app install | OAuth install into many customer workspaces | Many workspace installs, one per customer workspace | Marketplace or customer-installed apps | Store each installation separately by workspace or enterprise identity. |

## Workspace OAuth Install

This is the standard Slack app install.

The user approves an OAuth URL. Slack redirects back to the app. The app
exchanges the OAuth code and receives an access response with workspace
identity, bot token details, scopes, and optionally user token details.

The workspace identity is normally the Slack `team.id`.

Use this when the app needs to:

- receive workspace events
- post as the app's bot user
- respond to mentions or messages
- expose slash commands or app home
- serve many workspaces through the same distributed app

A workspace install does not mean the bot can act in every channel. Private
channels still require invitation. Public channel access depends on scopes,
membership, and Slack API behavior.

## Enterprise Org-Wide Install

Enterprise Grid supports org-ready apps and organization-level installation.

This is different from installing the app into one workspace inside an
Enterprise Grid organization.

In an org-wide install, the app receives enterprise identity, and Slack's OAuth
install data can indicate that this is an enterprise install. The app should
store and fetch this installation by enterprise identity when appropriate.

An org-wide install may still require the admin to add or grant the app to
specific workspaces in the organization.

Use this when the app is intended to operate across an Enterprise Grid
organization or use Enterprise-level capabilities.

## Incoming Webhook Channel Selection

If an app requests the `incoming-webhook` scope, Slack shows a channel picker
during OAuth.

The OAuth access response includes an `incoming_webhook` object with:

- `channel`
- `channel_id`
- `configuration_url`
- `url`

That webhook URL posts to the selected channel.

This is channel-specific, but it is not a general channel-scoped app install.
It only grants an incoming webhook destination.

Use this for notification-style apps that only need to post to one selected
channel.

## Channel Membership Or Channel Configuration

Many products say "install to a channel" when the real behavior is:

1. the app is installed into the workspace
2. the bot is invited to a channel or joins a public channel
3. the product stores a channel preference, route, allowlist, or subscription

This is not a separate Slack OAuth install.

It is a product-level configuration on top of a workspace install.

Use this when the app needs to operate only in selected channels.

Good names for this are:

- channel configuration
- channel binding
- channel allowlist
- add app to channel
- enable app in channel

## User Authorization

Slack OAuth can return user-level authorization under `authed_user` when the app
requests `user_scope`.

This is not the same thing as installing the app into a channel.

Use user authorization when the app must act on behalf of a specific user or
read data according to that user's access.

Slack treats Sign in with Slack scopes separately from normal Slack app scopes.
Do not mix those scopes into the same OAuth flow.

## Recommended Vocabulary

Use Slack-native words when talking about Slack behavior:

- workspace install
- org-wide install
- incoming webhook channel selection
- channel membership
- channel configuration
- user authorization

Use product-specific words only after defining them.

For example:

- "Workspace mode" can mean one app install per Slack workspace.
- "Channel mode" can mean one workspace install plus a selected channel route.

But Slack itself does not treat those as symmetric install modes.

## Spritz Mapping

For Spritz shared Slack apps, the Slack side is a workspace OAuth install.

Spritz then maps the Slack workspace identity to a deployment-owned target and a
live concierge runtime.

That means Spritz's shared-channel install flow is built on top of Slack
workspace install, not instead of it.

If Spritz later adds channel-specific settings, those should be modeled as
installation configuration or channel routing rules, not as a second Slack app
install.
