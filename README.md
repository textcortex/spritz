# Spritz

Spritz is a Kubernetes-native control plane for creating and accessing short-lived workspaces and agent runtimes.

## Status

Spritz is in active development and should be treated as alpha software.

That means:

- the core direction is set, but APIs, CRDs, and Helm values may still change
- deployment defaults are being hardened now, not frozen yet
- it is usable for development and system testing, but it is not positioned as a stable GA platform yet

## What Spritz is

Spritz is the control plane around a workspace, not the workspace runtime itself.

Its job is to:

- create workspace instances as `Spritz` resources in Kubernetes
- expose a single UI and API for opening those workspaces
- standardize the default workspace shape, including `/home/dev` as the home directory
- discover ACP-capable backends on port `2529`
- provide a built-in ACP chat surface so operators can talk to running agents through Spritz

The runtime inside a workspace is pluggable. A workspace may run OpenClaw or any other backend, as long as it follows the Spritz runtime contract.

## What exists today

Current Spritz includes:

- a Kubernetes operator that reconciles `Spritz` resources into running workspace instances
- a Spritz API that brokers access to workspaces and proxies browser ACP traffic
- a Spritz UI for listing workspaces, opening them, and testing ACP-capable agents
- a Helm chart intended to bring up a working standalone install with one public host
- ACP discovery and status reporting through `Spritz.status.acp`

## Default model

The default deployment model is intentionally simple:

- one Helm install
- one public host
- one UI and one API behind the same ingress surface
- browser traffic to agents always flows through `spritz-api`
- workspace home starts at `/home/dev`
- ACP is reserved on internal workspace port `2529`

The default goal is: install Spritz with a Helm chart, create workspaces, and talk to ACP-capable agents without needing custom edge routing or product-specific infrastructure.

## ACP in Spritz

Spritz reserves ACP on:

- port `2529`
- WebSocket transport
- path `/`

If something inside the workspace listens there and responds to ACP `initialize`, Spritz treats that workspace as ACP-capable.

The operator owns discovery and writes canonical ACP readiness into `Spritz.status.acp`. The browser never connects directly to workspace ACP ports; it always goes through `spritz-api`.

## What is still moving

The broad architecture is established, but some surfaces are still evolving while Spritz is in alpha:

- Helm values and install defaults
- auth gateway packaging and default auth flows
- ACP UI behavior and conversation management
- backend presets for different workspace runtimes
- operational hardening for production-scale installs

## Design constraints

Spritz is intended to stay portable and standalone:

- no hard dependency on TextCortex-specific infrastructure inside the Spritz codebase
- no required edge-worker path routing in the default deployment path
- no backend-specific ACP logic in the Spritz control plane
- no assumption that OpenClaw is the only supported runtime

## Key docs

- `/Users/onur/repos/spritz/docs/2026-02-24-simplest-spritz-deployment-spec.md`
- `/Users/onur/repos/spritz/docs/2026-03-09-acp-port-and-agent-chat-architecture.md`
- `/Users/onur/repos/spritz/OPENCLAW.md`
