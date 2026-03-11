# Spritz

<p align="center">
  <img src="docs/assets/agentol.svg" alt="Spritz" width="128">
</p>

<p align="center">
  <strong>Programmable open-source agent orchestrator</strong>
</p>

Spritz is a Kubernetes-native control plane for creating, exposing, discovering, and talking to agent workloads.

It is built around one simple idea: the agent runtime is the workload, and Spritz is the orchestrator around it. Spritz provisions the workload, gives it a consistent runtime shape, discovers whether it speaks ACP, and exposes a single UI and API for operators and clients to use it.

The backend inside a workspace is pluggable. OpenClaw is one example runtime. Any workload that exposes ACP on the Spritz contract can participate.

> Spritz is in active development and should be treated as alpha software. APIs, CRDs, Helm values, and UI details may still change while the deployment model is being hardened.

[Deployment Spec](docs/2026-02-24-simplest-spritz-deployment-spec.md) · [ACP Architecture](docs/2026-03-09-acp-port-and-agent-chat-architecture.md) · [Portable Auth](docs/2026-02-24-portable-authentication-and-account-architecture.md) · [External Provisioners](docs/2026-03-11-external-provisioner-and-service-principal-architecture.md) · [OpenClaw Integration](OPENCLAW.md)

## Vision

Spritz is meant to be:

- a programmable open-source orchestrator for agent workloads
- straightforward to deploy with one Helm chart and one host
- ACP-native, so backends can be discovered and reached through one contract
- backend-agnostic, so the workload can be OpenClaw or any other ACP-speaking runtime
- useful both as an operator UI and as a control plane that other ACP clients can sit behind

The long-term goal is not "a special OpenClaw console." The goal is a general control plane for ACP-capable agents running in Kubernetes.

## What Spritz does

Today Spritz provides:

- a Kubernetes operator that reconciles `Spritz` resources into running workloads
- a Spritz API that owns auth, workspace access, ACP metadata, and ACP gatewaying
- a Spritz UI for creating workloads, opening them, and testing ACP-capable agents
- a Helm chart for the simplest standalone deployment model
- a standard workspace home at `/home/dev`
- ACP discovery written into `Spritz.status.acp`
- a built-in ACP conversation surface for talking to ready agents through Spritz

## Core model

### Deployment model

The default install path is intentionally simple:

- one Helm release
- one public hostname
- one ingress surface
- `/` -> `spritz-ui`
- `/api` -> `spritz-api`
- `/oauth2` -> auth gateway when OIDC forward-auth is enabled

That keeps the default deployment understandable and avoids path-mounting tricks, split frontend hosting, or provider-specific edge logic.

### ACP model

Spritz reserves one internal ACP endpoint for each workload:

- port: `2529`
- transport: WebSocket
- path: `/`
- protocol: ACP JSON-RPC

If something inside the workload listens there and answers ACP `initialize`, Spritz treats that workload as ACP-capable.

The operator owns discovery. The browser never connects directly to workload ACP ports; ACP traffic always flows through `spritz-api`.

### Backend model

Spritz itself must stay backend-agnostic.

A workload participates in the ACP control plane if it:

- listens on `2529`
- speaks ACP over WebSocket
- returns a valid ACP `initialize` response
- supports the ACP session methods required by the client flow

OpenClaw is currently the main example backend in this repo, but it is not the protocol owner and it is not the only intended runtime.

## How it works

```text
Browser or ACP client
        |
        v
+-----------------------+
|     Spritz UI/API     |
| auth, ACP gatewaying, |
| metadata, open flow   |
+-----------+-----------+
            |
            v
+-----------------------+
|    Spritz operator    |
| provisioning, status, |
| ACP discovery         |
+-----------+-----------+
            |
            v
+-----------------------+
|   Spritz workload     |
| OpenClaw or any ACP   |
| backend on ws://:2529 |
+-----------------------+
```

The UI is the built-in operator client. The API is the stable control-plane surface. The workload can be swapped as long as it honors the ACP contract.

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

For an authenticated install, enable the in-cluster auth gateway and provide OIDC values. The example values file lives at:

- `helm/spritz/examples/portable-oidc-auth.values.yaml`

After install:

1. open the configured host
2. create a workload from the UI
3. open the workload directly or talk to it through the ACP chat surface if it exposes ACP on `2529`

## What exists today

Current Spritz includes:

- standalone-host deployment defaults in the Helm chart
- optional in-cluster OIDC auth gateway packaging
- workspace creation and access through the UI and API
- browser terminal access routed through `spritz-api`
- ACP discovery, readiness reporting, and API proxying
- conversation metadata stored as Kubernetes resources
- an OpenClaw example image and runtime wrapper for ACP-backed workloads

## What is still moving

Spritz is usable for development and system testing, but it is still alpha. The main surfaces still being hardened are:

- Helm defaults and install ergonomics
- auth packaging and default identity flows
- ACP UI behavior and conversation lifecycle details
- preset/workload packaging for different runtimes
- operational hardening for larger-scale deployments

## Design constraints

Spritz is intended to remain portable and standalone:

- no TextCortex-specific infrastructure inside the Spritz codebase
- no hard dependency on a single edge provider or auth vendor
- no backend-specific ACP logic in the Spritz control plane
- no assumption that OpenClaw is the only supported runtime
- no transcript storage in Kubernetes resources

## Repository map

- `operator/`: Kubernetes reconciliation and workload lifecycle
- `api/`: authenticated API and ACP gateway
- `ui/`: built-in web UI
- `helm/spritz/`: standalone deployment chart
- `images/examples/openclaw/`: OpenClaw example runtime and ACP adapter
- `docs/`: architecture and deployment documents

## Key docs

- `docs/2026-02-24-simplest-spritz-deployment-spec.md`
- `docs/2026-02-24-portable-authentication-and-account-architecture.md`
- `docs/2026-03-09-acp-port-and-agent-chat-architecture.md`
- `docs/2026-03-11-external-provisioner-and-service-principal-architecture.md`
- `OPENCLAW.md`
