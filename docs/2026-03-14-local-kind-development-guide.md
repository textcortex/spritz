---
date: 2026-03-14
author: Onur Solmaz <onur@textcortex.com>
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
- `spritz-operator`: the Kubernetes reconciler that creates instance resources
- instance images: the actual per-instance containers such as Claude Code or OpenClaw

In this guide:

- the control plane runs in namespace `spritz-system`
- instances run in namespace `spritz`
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
instances.

Install the matching CRDs:

```bash
kubectl apply -f https://github.com/kubernetes-sigs/gateway-api/releases/download/v1.1.0/standard-install.yaml
```

## 4. Create the Spritz namespaces

The instance namespace is not created automatically by the current local path.

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

## 6. Build the Claude Code instance image

If you want a chat-capable instance locally, build the Claude Code example image.

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

Use a non-`latest` tag such as `:local`. For instance images, Kubernetes will
normally try to pull `:latest` from a registry, which leads to `ImagePullBackOff`
even if the image was loaded into kind.

## 7. Create the Anthropic API key secret

Export the key in your shell:

```bash
export ANTHROPIC_API_KEY='replace-me'
```

Create the secret in the instance namespace:

```bash
kubectl create secret generic anthropic-api-key \
  -n spritz \
  --from-literal=ANTHROPIC_API_KEY="$ANTHROPIC_API_KEY" \
  --dry-run=client -o yaml | kubectl apply -f -
```

## 8. Configure shared mounts

Shared mounts let instances persist and sync files (such as `~/.config`) across
disposable instance restarts for the same owner. The syncer sidecar uses rclone
for storage. For local kind, we use the `local` filesystem type inside the API pod.

### Generate a shared bearer token

The operator and API authenticate syncer traffic with a shared token:

```bash
SHARED_TOKEN=$(openssl rand -hex 32)
```

### Create secrets in both namespaces

The token must exist in `spritz-system` (for the API) and `spritz` (for the
syncer sidecar running inside instance pods):

```bash
kubectl create secret generic spritz-shared-mounts-token \
  -n spritz-system \
  --from-literal=token="$SHARED_TOKEN" \
  --dry-run=client -o yaml | kubectl apply -f -

kubectl create secret generic spritz-shared-mounts-token \
  -n spritz \
  --from-literal=token="$SHARED_TOKEN" \
  --dry-run=client -o yaml | kubectl apply -f -

kubectl create secret generic spritz-api-shared-mounts-token \
  -n spritz-system \
  --from-literal=token="$SHARED_TOKEN" \
  --dry-run=client -o yaml | kubectl apply -f -
```

### Create the rclone config secret

For local kind, a `local` rclone remote stores data inside the API pod at
`/tmp/spritz-shared`:

```bash
cat <<'EOF' | kubectl create secret generic spritz-rclone-config \
  -n spritz-system \
  --from-file=rclone.conf=/dev/stdin \
  --dry-run=client -o yaml | kubectl apply -f -
[local]
type = local
EOF
```

## 9. Create the local Helm values file

Create `local-kind.values.yaml`:

```yaml
global:
  host: console.example.com
  ingress:
    className: nginx
    tls:
      enabled: false
      secretName: ""

x-claude-code-preset: &claude_code_preset
  id: claude-code
  name: Claude Code
  image: spritz-claude-code:local
  description: Claude Code via ACP
  env:
    - name: ANTHROPIC_API_KEY
      valueFrom:
        secretKeyRef:
          name: anthropic-api-key
          key: ANTHROPIC_API_KEY

ui:
  ownerId: local-user
  presets:
    - *claude_code_preset

operator:
  sharedMounts:
    enabled: true
    mounts:
      - name: config
        mountPath: /home/dev/.config
        scope: owner
        mode: snapshot
        syncMode: poll
    apiUrl: http://spritz-api.spritz-system:8080
    tokenSecret:
      name: spritz-shared-mounts-token
      key: token
    syncerImage: spritz-api:latest
    syncerImagePullPolicy: IfNotPresent

api:
  presets:
    - *claude_code_preset
  sharedMounts:
    enabled: true
    mounts:
      - name: config
        mountPath: /home/dev/.config
        scope: owner
        mode: snapshot
        syncMode: poll
    prefix: spritz-shared
    rclone:
      remote: local
      bucket: /tmp/spritz-shared
      configSecret:
        name: spritz-rclone-config
        key: rclone.conf
    internalTokenSecret:
      name: spritz-api-shared-mounts-token
      key: token
  provisioners:
    allowCustomImage: true
  defaultIngress:
    mode: ingress
    hostTemplate: console.example.com
    path: /i/{name}
    className: nginx
```

Important local choices:

- `tls.enabled: false` keeps the setup on plain `http`
- `ui.ownerId: local-user` gives instances an owner in auth-disabled local mode
- `api.presets` is the real preset catalog and injects the Anthropic key via a Kubernetes secret
- `ui.presets` mirrors the same preset entry for the current UI until the UI reads `GET /api/presets` directly
- `defaultIngress` gives each instance a browser route
- `operator.sharedMounts` and `api.sharedMounts` enable owner-scoped config
  persistence using the local filesystem via rclone
- `syncerImage: spritz-api:latest` reuses the API image which bundles the
  `spritz-shared-syncer` binary

## 10. Install Spritz

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

## 11. Make the hostname resolve locally

Map the host used in `global.host` to `127.0.0.1`:

```bash
echo "127.0.0.1 console.example.com" | sudo tee -a /etc/hosts
```

Verify the UI route:

```bash
curl -I http://console.example.com
```

It should return `HTTP/1.1 200 OK`.

## 12. Open the UI

Open:

```text
http://console.example.com
```

This local guide intentionally uses `http`, not `https`.

## 13. Create an instance

In the UI:

- choose the `Claude Code` preset
- create the instance

Then inspect it:

```bash
kubectl get spritzes -n spritz
kubectl get pods,svc,ingress -n spritz
```

## Useful inspection commands

These are the fastest commands to understand where an instance is stuck:

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

The instance spec includes shared mounts but the operator does not have the
shared mount backend configured. Follow step 8 to create the required secrets
and add the `operator.sharedMounts` and `api.sharedMounts` sections to your
Helm values file, then upgrade the release.

### `Init:CreateContainerConfigError` — secret not found in instance namespace

The syncer init container runs inside the instance pod in the `spritz`
namespace. The token secret must exist in both `spritz-system` (for the API)
and `spritz` (for instance pods):

```bash
kubectl create secret generic spritz-shared-mounts-token \
  -n spritz \
  --from-literal=token="$SHARED_TOKEN" \
  --dry-run=client -o yaml | kubectl apply -f -
```

### Instance is stuck at `waiting for deployment`

Inspect the pod:

```bash
kubectl describe pod <pod-name> -n spritz
```

One common cause is using an image that does not satisfy the Spritz runtime contract.
Spritz expects the instance to answer ACP health on port `2529`.

For example, a plain image such as `nginx:alpine` will run as a container, but it
will never pass the ACP readiness checks required for chat.

### `ImagePullBackOff` for a locally built instance image

Do not use `:latest` for an instance image loaded into kind.

Use a non-`latest` tag such as:

```text
spritz-claude-code:local
```

Then load that exact tag into kind:

```bash
kind load docker-image spritz-claude-code:local --name spritz
```

### Generated instance URL uses `https`

The simplest local path in this guide is HTTP-only.

If a generated instance URL uses `https://console.example.com/...`, switch it to
`http://console.example.com/...` in the browser.

## Cleanup

Delete an instance:

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
