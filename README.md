# spritz

Spritz is a Kubernetes-native control plane for ephemeral workspaces.

## What it does

- creates per-user or per-team workspace instances as `Spritz` resources
- exposes web and terminal access through the Spritz API and UI
- reserves ACP on port `2529` for agent-capable backends
- automatically discovers ACP agents and exposes them in the built-in chat UI

## Default runtime model

- one Helm install
- one public host for UI and API
- workspace home starts at `/home/dev`
- ACP is available on internal workspace port `2529`
- browser ACP traffic always flows through `spritz-api`

## Key docs

- `/Users/onur/repos/spritz/docs/2026-02-24-simplest-spritz-deployment-spec.md`
- `/Users/onur/repos/spritz/docs/2026-03-09-acp-port-and-agent-chat-architecture.md`
- `/Users/onur/repos/spritz/OPENCLAW.md`
