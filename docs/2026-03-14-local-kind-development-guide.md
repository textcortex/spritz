---
date: 2026-03-14
author: Spritz Contributors <user@example.com>
title: Local kind Development Guide
tags: [spritz, kind, kubernetes, local-development]
---

## Overview

This guide documents the working local Kubernetes setup for Spritz on a laptop
using `kind`.

It covers:

- creating a local cluster
- installing ingress
- installing the Gateway API CRDs required by the operator
- building and loading local images
- configuring a simple HTTP-only local install
- creating a Claude Code preset backed by a Kubernetes secret
- debugging the failure modes hit during the first working setup

The goal is a copyable workflow that a teammate can follow without rediscovering
cluster-specific details from terminal history.

## What is running

A local Spritz install has four main parts:

- `spritz-ui`: the web UI
- `spritz-api`: the backend
- `spritz-operator`: the Kubernetes reconciler that creates workspace resources
- workspace images: the actual per-workspace containers such as Claude Code or OpenClaw

In this guide:

- the control plane runs in namespace `spritz-system`
- workspaces run in namespace `spritz`
- the public host is `console.example.com`
- traffic is served over plain `http` for simplicity

## Prerequisites

Install and verify:

- Docker Desktop or another working local Docker runtime
- `kubectl`
- `kind`
- `helm`

Optional but useful:

- `jq`

## 1. Create the kind cluster

Create a cluster config that exposes host port `80` to the kind control-plane node:

```yaml
# kind-config.yaml
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
nodes:
  - role: control-plane
    extraPortMappings:
      - containerPort: 80
        hostPort: 80
        listenAddress: "127.0.0.1"
        protocol: TCP
```

Create the cluster:

```bash
kind create cluster --name spritz --config kind-config.yaml
kubectl config use-context kind-spritz
```

## 2. Install ingress-nginx in hostPort mode

For local kind development, the default ingress-nginx chart install is not
enough by itself. The controller should run as a `DaemonSet` with `hostPort`
enabled so the kind node actually listens on host port `80`.

Install it like this:

```bash
helm upgrade --install ingress-nginx ingress-nginx \
  --repo https://kubernetes.github.io/ingress-nginx \
  --namespace ingress-nginx \
  --create-namespace \
  --set controller.kind=DaemonSet \
  --set controller.hostPort.enabled=true
```

Wait for it to become ready:

```bash
kubectl rollout status -n ingress-nginx daemonset/ingress-nginx-controller --timeout=180s
```

## 3. Install Gateway API CRDs

The operator watches `HTTPRoute` resources at startup. Without the Gateway API
CRDs, the operator can fail during cache sync before it reconciles any
workspaces.

Install the matching CRDs:

```bash
kubectl apply -f https://github.com/kubernetes-sigs/gateway-api/releases/download/v1.1.0/standard-install.yaml
```

## 4. Create the Spritz namespaces

The workspace namespace is not created automatically by the current local path.

```bash
kubectl create namespace spritz-system --dry-run=client -o yaml | kubectl apply -f -
kubectl create namespace spritz --dry-run=client -o yaml | kubectl apply -f -
```

## 5. Build the control-plane images

From the repository root:

```bash
docker build -f operator/Dockerfile -t spritz-operator:latest operator
docker build -f api/Dockerfile -t spritz-api:latest .
docker build -f ui/Dockerfile -t spritz-ui:latest ui
```

Load them into kind:

```bash
kind load docker-image spritz-operator:latest --name spritz
kind load docker-image spritz-api:latest --name spritz
kind load docker-image spritz-ui:latest --name spritz
```

## 6. Build the Claude Code workspace image

If you want a chat-capable workspace locally, build the Claude Code example image.

Build from `images/`:

```bash
cd images
docker build -f examples/claude-code/Dockerfile -t spritz-claude-code:local .
cd ..
```

Load it into kind:

```bash
kind load docker-image spritz-claude-code:local --name spritz
```

Use a non-`latest` tag such as `:local`. For workspace images, Kubernetes will
normally try to pull `:latest` from a registry, which leads to `ImagePullBackOff`
even if the image was loaded into kind.

## 7. Create the Anthropic API key secret

Export the key in your shell:

```bash
export ANTHROPIC_API_KEY='replace-me'
```

Create the secret in the workspace namespace:

```bash
kubectl create secret generic anthropic-api-key \
  -n spritz \
  --from-literal=ANTHROPIC_API_KEY="$ANTHROPIC_API_KEY" \
  --dry-run=client -o yaml | kubectl apply -f -
```

## 8. Create the local Helm values file

Create `local-kind.values.yaml`:

```yaml
global:
  host: console.example.com
  ingress:
    className: nginx
    tls:
      enabled: false
      secretName: ""

ui:
  ownerId: local-user
  presets:
    - id: claude-code
      name: Claude Code
      image: spritz-claude-code:local
      description: Claude Code via ACP
      env:
        - name: ANTHROPIC_API_KEY
          valueFrom:
            secretKeyRef:
              name: anthropic-api-key
              key: ANTHROPIC_API_KEY

api:
  provisioners:
    allowCustomImage: true
  defaultIngress:
    mode: ingress
    hostTemplate: console.example.com
    path: /w/{name}
    className: nginx
```

Important local choices:

- `tls.enabled: false` keeps the setup on plain `http`
- `ui.ownerId: local-user` gives workspaces an owner in auth-disabled local mode
- `ui.presets` injects the Anthropic key via a Kubernetes secret
- `defaultIngress` gives each workspace a browser route

## 9. Install Spritz

```bash
helm upgrade --install spritz ./helm/spritz \
  --namespace spritz-system \
  --create-namespace \
  -f local-kind.values.yaml
```

Wait for the control plane:

```bash
kubectl get pods -n spritz-system -w
```

You want:

- `spritz-api` running
- `spritz-ui` running
- `spritz-operator` running

## 10. Make the hostname resolve locally

Map the host used in `global.host` to `127.0.0.1`:

```bash
echo "127.0.0.1 console.example.com" | sudo tee -a /etc/hosts
```

Verify the UI route:

```bash
curl -I http://console.example.com
```

It should return `HTTP/1.1 200 OK`.

## 11. Open the UI

Open:

```text
http://console.example.com
```

This local guide intentionally uses `http`, not `https`.

## 12. Create a workspace

In the UI:

- choose the `Claude Code` preset
- create the workspace

Then inspect it:

```bash
kubectl get spritzes -n spritz
kubectl get pods,svc,ingress -n spritz
```

## Useful inspection commands

These are the fastest commands to understand where a workspace is stuck:

```bash
kubectl get spritzes -n spritz
kubectl describe spritz <name> -n spritz
kubectl get pods,svc,ingress -n spritz
kubectl describe pod <pod-name> -n spritz
kubectl logs <pod-name> -n spritz --all-containers --tail=200
kubectl logs -n spritz-system deploy/spritz-operator --tail=100
kubectl logs -n spritz-system deploy/spritz-api --tail=100
```

## Troubleshooting

### `chart "ingress-nginx/ingress-nginx" not found`

When using `--repo`, pass the chart name only:

```bash
helm upgrade --install ingress-nginx ingress-nginx \
  --repo https://kubernetes.github.io/ingress-nginx \
  --namespace ingress-nginx \
  --create-namespace
```

### UI opens but host does not resolve

If `curl` says it cannot resolve the host, add it to `/etc/hosts`:

```bash
echo "127.0.0.1 console.example.com" | sudo tee -a /etc/hosts
```

### UI still not reachable even though ingress exists

Make sure ingress-nginx was installed with:

- `controller.kind=DaemonSet`
- `controller.hostPort.enabled=true`

Without that, the controller may exist in-cluster but the kind node will not
actually listen on host port `80`.

### Operator crashes on startup with missing `HTTPRoute`

Install the Gateway API CRDs before or immediately after installing the control plane:

```bash
kubectl apply -f https://github.com/kubernetes-sigs/gateway-api/releases/download/v1.1.0/standard-install.yaml
kubectl rollout restart -n spritz-system deployment/spritz-operator
```

### `spec.owner.id is required`

In auth-disabled local mode, the UI needs a fallback owner id:

```yaml
ui:
  ownerId: local-user
```

### `shared mounts requested but operator is not configured`

This usually means the browser form still contains saved `userConfig` with
`sharedMounts`.

Fix it by:

- clearing the `User config` field in the UI

Then recreate the workspace.

### Workspace is stuck at `waiting for deployment`

Inspect the pod:

```bash
kubectl describe pod <pod-name> -n spritz
```

One common cause is using an image that does not satisfy the Spritz runtime contract.
Spritz expects the workspace to answer ACP health on port `2529`.

For example, a plain image such as `nginx:alpine` will run as a container, but it
will never pass the ACP readiness checks required for chat.

### `ImagePullBackOff` for a locally built workspace image

Do not use `:latest` for a workspace image loaded into kind.

Use a non-`latest` tag such as:

```text
spritz-claude-code:local
```

Then load that exact tag into kind:

```bash
kind load docker-image spritz-claude-code:local --name spritz
```

### Generated workspace URL uses `https`

The simplest local path in this guide is HTTP-only.

If a generated workspace URL uses `https://console.example.com/...`, switch it to
`http://console.example.com/...` in the browser.

## Cleanup

Delete a workspace:

```bash
kubectl delete spritz <name> -n spritz
```

Uninstall Spritz:

```bash
helm uninstall spritz -n spritz-system
```

Delete the kind cluster:

```bash
kind delete cluster --name spritz
```
