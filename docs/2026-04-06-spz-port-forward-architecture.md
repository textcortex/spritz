---
date: 2026-04-06
author: Onur Solmaz <onur@textcortex.com>
title: spz Port-Forward Architecture
tags: [spritz, spz, cli, ssh, websocket, https, port-forwarding, architecture]
---

## Overview

This document defines the preferred Spritz CLI contract for local access to
private instance ports.

The core design decision is that Spritz should expose an explicit,
deployment-agnostic low-level primitive:

```bash
spz port-forward <instance> --local <port> --remote <port>
```

This should be the canonical Spritz CLI shape for forwarding a local loopback
port to a private port inside one Spritz instance.

It should be implemented as an instance-scoped control-plane feature. The
preferred public transport should be the authenticated Spritz control plane
over HTTPS/WebSocket on port `443`, not raw public SSH on port `22`.

It should not be framed as a Kubernetes workflow, not as a browser preview
product, and not as a deployment-specific convenience alias.

## TL;DR

- Spritz core should add `spz port-forward`.
- The command should be explicit and transport-agnostic in meaning.
- The preferred public transport should be authenticated HTTPS/WebSocket over
  the existing Spritz control plane on port `443`.
- The current SSH-backed implementation may remain as a fallback, but SSH
  should be treated as deprecated for default public use unless somebody
  explicitly asks for it.
- The command should default to:
  - local bind host `127.0.0.1`
  - remote target host `127.0.0.1`
- Spritz should authenticate the tunnel, not the application protocol behind
  the forwarded port.
- Application-specific wrappers may exist downstream, but the upstream Spritz
  primitive should stay generic and explicit.

## Problem

Users and downstream deployments need a safe way to reach private ports inside
an instance from a local machine.

Common examples:

- open a local browser against a web app running inside an instance
- attach a local debugger to a process listening only inside the instance
- inspect an internal HTTP endpoint without exposing it on public ingress

Today, the CLI has raw instance access primitives such as:

- `spz ssh <name>`
- `spz terminal <name>`

Those are useful, but they force callers to manually construct port-forwarding
behavior on top of raw SSH usage.

That creates several problems:

- the workflow is discoverable only for users who already know `ssh -L`
- the product contract is implicit instead of first-class
- downstreams are pushed toward app-specific commands too early
- users may reach for the wrong tool, such as `kubectl port-forward`, which
  bypasses the Spritz instance access boundary

Spritz should expose the intent directly.

## Goals

- Provide one clear CLI primitive for local access to private instance ports.
- Keep the contract generic across apps, runtimes, and deployments.
- Reuse the existing Spritz instance auth boundary.
- Avoid requiring Kubernetes credentials or pod-level knowledge.
- Keep the default security posture loopback-only on both sides.
- Make it easy for downstream deployments to build higher-level wrappers later.

## Non-goals

- Defining app-specific preview commands in upstream Spritz.
- Requiring public ingress for development servers.
- Turning Spritz into a remote desktop or VNC product.
- Solving application-layer login, cookies, or browser session reuse.
- Replacing `spz ssh` as the raw shell-access primitive.

## Design Decision

Spritz should standardize an explicit low-level forwarding command:

```bash
spz port-forward <instance> --local <port> --remote <port>
```

This should be the core primitive. It should mean:

- listen on one local loopback port
- forward to one private remote port inside one named instance
- keep the tunnel alive until interrupted

The public-facing design should prefer an authenticated control-plane tunnel
over HTTPS/WebSocket on `443`.

The existing SSH certificate mint flow may still be used as an implementation
fallback where raw TCP is available or explicitly desired, but Spritz should
not require public raw SSH exposure as the default internet-facing transport.

The product contract, however, should be described as "instance port
forwarding", not as "raw SSH with custom flags". That distinction matters:

- users reason about what they want to do, not the transport internals
- the control plane remains the owner of authorization and target resolution
- future transports may change without renaming the user-facing intent

## Preferred Public Transport

The ideal Spritz system should not require a public inbound instance port at
all.

For public usage, `spz port-forward` should terminate on the existing Spritz
control plane host over HTTPS/WebSocket on `443`, and the control plane should
perform the pod-scoped forwarding inside the cluster.

This is the preferred architecture because it:

- works in environments where raw public TCP may be restricted
- keeps auth, policy, rate limiting, and audit under the control plane
- avoids depending on cloud-specific behavior for arbitrary inbound TCP
- gives one clean public access surface instead of separate web and SSH entry
  points

SSH may still exist as a transport, but it should not be the primary public
story.

## Why `spz port-forward` Instead Of `spz preview`

`preview` is the wrong upstream primitive because it encodes application
semantics into the core CLI.

Problems with `preview` as the base command:

- it sounds browser-specific
- it sounds frontend-specific
- it does not generalize cleanly to debuggers, admin endpoints, metrics, or
  other private instance ports
- it encourages Spritz core to learn too much about downstream app types

`port-forward` is better because it is:

- explicit
- generic
- already familiar to operators and developers
- honest about what the command does

Downstreams may still layer higher-level wrappers later, but those wrappers
should sit on top of the explicit primitive rather than replace it.

## Why Not `kubectl port-forward`

`kubectl port-forward` is useful for cluster operators, but it is not the
right Spritz product contract.

Problems with making it the standard workflow:

- it requires Kubernetes credentials
- it exposes pod and namespace details to the caller
- it targets infrastructure objects instead of instance identity
- it bypasses the Spritz access boundary and control-plane policy

Spritz should own instance access through its own control plane.

Cluster-level port-forwarding should remain an operator fallback, not the
standard user path.

## Authentication And Security Boundary

The forwarding contract should be explicit about what Spritz does and does not
authenticate.

Spritz owns:

- authenticating the caller to the Spritz control plane
- authorizing access to the target instance
- minting any short-lived forwarding credentials
- binding the local tunnel to the target instance and target remote port

Spritz does not own:

- application login inside the forwarded service
- application cookies
- bearer tokens for the app behind the forwarded port
- browser session reuse across origins

This separation is desirable.

Example:

- a caller may use `spz port-forward` to reach `http://localhost:3000`
- the forwarded app may still ask the browser to log in normally
- that application auth flow is outside the Spritz forwarding contract

Spritz should authenticate the tunnel, not the application protocol.

## Security Model

The default security posture should be strict.

### Local bind defaults

The command should bind locally to loopback only by default:

- `127.0.0.1:<local-port>`

It should not expose the forwarded port on:

- `0.0.0.0`
- public interfaces
- LAN interfaces

If wider binding is ever supported, it should require an explicit flag and a
separate security review. It should not be part of the first implementation.

### Remote target defaults

The command should target loopback inside the instance by default:

- `127.0.0.1:<remote-port>`

This keeps the first implementation focused on the common safe case:

- connecting to services that intentionally listen only inside the instance

### Forwarding direction

The first implementation should support local forwarding only.

Allowed:

- local machine -> instance

Not in scope:

- remote forwarding
- reverse tunnels
- listener exposure from the instance back toward the caller

### Credential lifetime

When implemented over SSH, the command should reuse the existing short-lived
certificate flow and host verification behavior used by `spz ssh`.

The caller should not need to manage long-lived static keys.

## Proposed Command Contract

The initial CLI surface should stay simple and explicit.

Preferred shape:

```bash
spz port-forward <instance> --local 3000 --remote 3000
```

Recommended flags for the first version:

- positional instance name
- `--namespace <ns>` when needed
- `--local <port>`
- `--remote <port>`
- `--print`

Recommended defaults:

- local bind host: `127.0.0.1`
- remote target host: `127.0.0.1`
- transport implementation: existing SSH path

Recommended behavior:

- print a clear one-line summary before attaching
- stay in the foreground
- exit cleanly on interrupt
- clean up temporary credentials on exit

Example user-visible output:

```text
Forwarding 127.0.0.1:3000 -> my-instance:127.0.0.1:3000
Press Ctrl+C to stop.
```

## Relationship To `spz ssh`

`spz ssh` should remain the raw shell-access command when explicitly needed.

`spz port-forward` should be a sibling command with a narrower purpose:

- `spz ssh`: interactive shell access
- `spz port-forward`: local access to one private instance port

The implementation may still share credential plumbing, but the public default
for `spz port-forward` should not be "SSH unless proven otherwise".

That split is still good:

- less duplicated auth logic
- one clear control-plane contract for instance access

For now:

- `spz ssh` remains available
- SSH-backed `spz port-forward` may remain available
- SSH should be considered deprecated as the default public transport unless a
  deployment or operator explicitly asks for it

## Downstream Wrappers

Spritz core should stop at the generic primitive.

Downstream deployments may build convenience wrappers on top, for example:

- frontend development helpers
- app-specific "open preview" flows
- wrapper commands that start a local browser automatically

Those are valid downstream choices, but they should not define the core Spritz
contract.

The holy grail shape is:

- Spritz core provides `spz port-forward`
- Spritz routes public interactive access through one authenticated control
  plane on `443`
- downstream deployments compose deployment-specific UX on top of it

That keeps Spritz portable while still enabling polished local workflows where
needed.

## Implementation Plan

### Phase 1: Core CLI Primitive

- add `spz port-forward`
- keep the user-facing contract transport-agnostic
- support one local forward per command invocation
- keep both local and remote hosts pinned to loopback

Acceptance criteria:

- an authorized caller can reach a private instance port without manual
  `ssh -L` composition
- the command requires no Kubernetes credentials
- the command targets one named instance, not a pod

### Phase 2: Public Control-Plane Transport

- implement forwarding over the authenticated Spritz control plane on
  HTTPS/WebSocket
- make that path the preferred public transport
- avoid requiring any public raw TCP listener on the instance gateway

Acceptance criteria:

- the standard public path works over `443`
- no public per-instance or per-feature raw TCP exposure is required
- authorization remains owned by the Spritz control plane

### Phase 3: SSH Fallback

- keep the SSH-backed transport available for private networks, operators, or
  deployments that explicitly want it
- document that SSH is a fallback transport, not the preferred public one

Acceptance criteria:

- SSH remains available when explicitly requested
- SSH is no longer the default public transport assumption in docs or UX

### Phase 4: CLI Help And Tests

- document the new command in CLI help
- add help tests for the new usage line
- add command tests for printed transport execution shape or equivalent command
  plumbing

Acceptance criteria:

- the new command is discoverable through `spz --help`
- printed guidance stays generic and deployment-agnostic

### Phase 5: Downstream Composition

- allow downstreams to add wrappers without changing the core primitive
- document that application auth remains outside Spritz forwarding

Acceptance criteria:

- upstream Spritz stays generic
- downstream deployments can build local developer UX on top cleanly

## Validation

This architecture is successful when all of the following are true:

- the standard path for instance port access is `spz port-forward`, not raw
  `ssh -L`
- the preferred public path runs through the authenticated Spritz control
  plane on `443`
- the caller does not need Kubernetes credentials
- the command works by instance identity rather than pod identity
- the default bind scope is local loopback only
- the application behind the forwarded port can keep its own auth model
- SSH remains optional rather than mandatory for public use
- downstream wrappers can exist without forcing Spritz core to become
  app-specific

## References

- `docs/2026-03-13-spz-audience-and-external-owner-guidance.md`
- `docs/2026-03-24-privileged-conversation-debug-and-test-architecture.md`
- `docs/2026-03-30-browser-websocket-auth-model.md`
- `cli/src/index.ts`
