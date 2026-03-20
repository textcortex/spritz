# Spritz

<p align="center">
  <img src="docs/assets/agentol.svg" alt="Spritz" width="128">
</p>

<p align="center">
  <strong>Open-source Kubernetes orchestrator for disposable agent instances</strong>
</p>

Spritz is a self-hosted control plane for spawning isolated agent instances on Kubernetes.

You deploy Spritz on your own cluster, package one or more agent runtimes as presets, and let humans or gateway automations spawn fresh agents on demand. Each spawned agent runs in its own instance workload, is owned by a specific user, and is exposed through a consistent UI, API, and ACP gateway.

Spritz is built to stay runtime-agnostic. [OpenClaw](https://docs.openclaw.ai/) is one example runtime in this repository, but Spritz is not tied to OpenClaw. Any agent that speaks the [Agent Client Protocol (ACP)](https://agentclientprotocol.com/get-started/introduction) on the Spritz runtime contract can run behind it, whether that is OpenClaw, Claude Code, a Codex-based runtime, or a custom internal agent image.

> Spritz is in active development and should be treated as alpha software. APIs, CRDs, Helm values, and UI details may still change while the deployment model is being hardened.

[Quick Start](#quick-start) · [What It Feels Like](#what-it-feels-like) · [Architecture](#architecture) · [UI Branding](docs/2026-03-20-ui-branding-customization.md) · [Deployment Spec](docs/2026-02-24-simplest-spritz-deployment-spec.md) · [ACP Architecture](docs/2026-03-09-acp-port-and-agent-chat-architecture.md) · [External Provisioners](docs/2026-03-11-external-provisioner-and-service-principal-architecture.md) · [External Identity Resolution](docs/2026-03-12-external-identity-resolution-api-architecture.md) · [Spawn Vocabulary](docs/2026-03-13-spawn-language-for-agent-instances.md) · [OpenClaw Integration](docs/2026-03-13-openclaw-integration.md)

## What Spritz is for

Spritz is for teams that want to run many user-owned agents on shared Kubernetes infrastructure without turning the gateway bot into the runtime.

The model is simple:

- the gateway bot or automation is just the requester
- the actual agent runs in a separate Spritz instance
- the instance is owned by the human user it was created for
- the human opens it through a Spritz client surface and talks to the agent there

That makes Spritz useful for:

- disposable per-task agent instances
- internal gateway bots on Discord, Slack, Teams, or other messaging surfaces
- self-managed enterprise deployments on private infrastructure
- high-concurrency setups where many users may run multiple agents at once
- agent fleets that need consistent access, auth, ownership, and lifecycle rules

## What it feels like

In user-facing language, `spawn` means `create a fresh agent instance`.

A typical flow looks like this:

1. A company deploys Spritz on its own Kubernetes cluster with the Helm chart.
2. The company defines presets for the agent runtimes it wants to offer, such as OpenClaw, Claude Code, or a custom ACP-capable image.
3. A human asks a gateway bot or internal automation to spawn an agent for a task.
4. The gateway bot calls Spritz to create the instance for that user.
5. Spritz provisions the instance, binds it to the resolved owner, and returns canonical open URLs.
6. The user opens a Spritz client surface and works with the spawned agent.

Today the built-in interactive surface is the Spritz web UI. That UI is one first-party client, not the only intended interface. Over time, Spritz is meant to sit behind adapters that embed the same instance flow into chat products such as Discord, Slack, Teams, or other messaging surfaces. Those adapters are planned future work; today the web UI is the shipped interactive client.

## Why Spritz exists

Most agent demos run one agent in one process for one operator. That breaks down quickly once you want:

- many users
- many tasks
- many agents
- isolated runtimes
- self-managed infrastructure

Spritz treats the agent runtime as a workload and puts a reusable control plane around it:

- provisioning
- ownership
- access URLs
- ACP discovery
- terminal access
- chat access
- TTLs and lifecycle
- gateway-bot-friendly create flows

The result is a system where each spawned agent can be disposable and isolated, while the cluster-level experience still feels coherent.

## What can run on Spritz

Spritz is not an OpenClaw console. It is a control plane for ACP-capable agents.

A runtime fits Spritz if it can expose ACP on the current Spritz contract:

- port `2529`
- WebSocket transport
- path `/`
- successful ACP `initialize`

That can be:

- the example [OpenClaw](https://docs.openclaw.ai/) runtime in this repo
- the example Claude Code runtime in this repo
- a custom internal image
- a wrapper or adapter around another agent runtime

The important boundary is the protocol, not the brand of the agent.

## Current capabilities

Spritz currently provides:

- a Kubernetes operator that reconciles `Spritz` resources into running instances
- a Spritz API that owns auth, instance access, ACP metadata, and ACP proxying
- a built-in web UI for creating instances, opening them, and chatting with ACP-capable agents
- a CLI and service-principal-friendly create flow for external provisioners
- preset-based instance creation with canonical URLs in the create response
- external owner resolution for gateway bots that know a platform user ID but not an internal owner ID
- owner-bound instances where the creator and the later user do not need to be the same principal
- ACP readiness discovery written into `Spritz.status.acp`
- browser terminal access routed through `spritz-api`
- optional shared mounts for owner-scoped state sharing between disposable instances

That means a gateway bot can create an instance for a human user, but the bot does not need to become that user and does not automatically inherit post-create access to the instance. The built-in UI is the current first-party client for opening and using those instances, while chat-native adapters are expected to layer on top later.

## Gateway bots and external owner resolution

One of Spritz's core use cases is the gateway-agent pattern.

For example, a deployment can run a bot on Discord, Slack, Teams, or another messaging platform. That bot can ask Spritz to spawn an instance using the platform-native user identifier it already has. Spritz then resolves the true instance owner through a deployment-owned resolver and creates the instance for that owner.

In practice, this gives you a clean separation:

- the messaging bot knows the platform user ID
- Spritz owns instance creation, access, and lifecycle
- the deployment decides how platform identities resolve to real owners
- the created instance remains owned by the human user, not by the bot

This is the long-term stable path for chat-triggered agent spawns.

## Architecture

```text
Human or gateway bot
         |
         v
+------------------------+
|      Spritz API/UI     |
| auth, create, URLs,    |
| ACP gateway, terminal  |
+-----------+------------+
            |
            v
+------------------------+
|    Spritz operator     |
| reconcile, status,     |
| readiness, lifecycle   |
+-----------+------------+
            |
            v
+------------------------+
|   Agent instance      |
| OpenClaw, Claude Code, |
| or any ACP runtime     |
| on ws://:2529/         |
+------------------------+
```

The workload is the agent runtime. Spritz is the orchestration layer around it.

## Core model

### Deployment model

The default install path is intentionally simple:

- one Helm release
- one public host
- one ingress surface
- `/` -> `spritz-ui`
- `/api` -> `spritz-api`
- `/oauth2` -> auth gateway when forward-auth is enabled

This keeps the default deployment understandable and portable.

### Instance model

An instance is the actual running environment for one spawned agent. In Kubernetes terms, that is a `Spritz` resource reconciled into a deployment and related services.

The user-facing verb can be `spawn`, but the stored resource remains an instance.

### ACP model

Spritz reserves one internal ACP endpoint per instance:

- port `2529`
- transport `WebSocket`
- path `/`
- protocol `ACP JSON-RPC`

If a workload answers there successfully, Spritz treats it as ACP-capable and exposes it through the Spritz API and UI.

The browser never needs direct access to the workload ACP port. ACP traffic flows through `spritz-api`.

## Quick start

The current default path is a standalone Helm install.

```bash
git clone https://github.com/textcortex/spritz.git
cd spritz

helm upgrade --install spritz ./helm/spritz \
  --namespace spritz-system \
  --create-namespace \
  --set global.host=spritz.example.com
```

For an authenticated install, enable the in-cluster auth gateway and provide OIDC values with [helm/spritz/examples/portable-oidc-auth.values.yaml](helm/spritz/examples/portable-oidc-auth.values.yaml).

For one browser host, prefer one browser-facing auth gateway. If a deployment
uses multiple auth gateway instances on the same host anyway, it must isolate
cookie state and callback ownership per gateway. See
[Shared-Host Auth Gateway Architecture](docs/2026-03-20-shared-host-auth-gateway-architecture.md).

After install:

1. open the configured host
2. create an instance from the UI or API
3. open the instance directly or use the built-in ACP chat surface if the runtime exposes ACP on `2529`

## UI branding

Spritz supports deployment-wide UI branding for operators that want a white-label presentation without adding a separate settings service.

The v1 path is a small `ui.branding` Helm values block that flows into the built-in UI at startup. It supports:

- product name
- logo URL
- favicon URL
- a single app theme palette
- terminal colors

See [Deployment-Wide UI Branding](docs/2026-03-20-ui-branding-customization.md) for the exact values shape and an example override file.

## Design constraints

Spritz is intended to remain portable and standalone:

- no organization-specific infrastructure inside the Spritz codebase
- no hard dependency on one auth vendor, messaging platform, or edge provider
- no assumption that OpenClaw is the only supported runtime
- no requirement that gateway bots know internal owner IDs
- no backend-specific ACP logic in the core control plane beyond the protocol contract

## Repository map

- `operator/`: Kubernetes reconciliation and workload lifecycle
- `api/`: authenticated API, provisioning logic, and ACP gateway
- `ui/`: built-in web UI
- `cli/`: `spz` CLI and bundled Spritz skill
- `helm/spritz/`: standalone deployment chart
- `images/examples/openclaw/`: OpenClaw example runtime and ACP wrapper
- `images/examples/claude-code/`: Claude Code example runtime and ACP wrapper
- `docs/`: architecture and deployment documents

## Key docs

- [Simplest Spritz Deployment Spec](docs/2026-02-24-simplest-spritz-deployment-spec.md)
- [Portable Authentication and Account Architecture](docs/2026-02-24-portable-authentication-and-account-architecture.md)
- [ACP Port and Agent Chat Architecture](docs/2026-03-09-acp-port-and-agent-chat-architecture.md)
- [External Provisioner and Service Principal Architecture](docs/2026-03-11-external-provisioner-and-service-principal-architecture.md)
- [External Identity Resolution API Architecture](docs/2026-03-12-external-identity-resolution-api-architecture.md)
- [Spawn Language for Agent Instances](docs/2026-03-13-spawn-language-for-agent-instances.md)
- [OpenClaw Integration](docs/2026-03-13-openclaw-integration.md)
- [Local kind Development Guide](docs/2026-03-14-local-kind-development-guide.md)
- [Deployment-Wide UI Branding](docs/2026-03-20-ui-branding-customization.md)
