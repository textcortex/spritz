---
date: 2026-04-01
author: Onur Solmaz <onur@textcortex.com>
title: Runtime Policy Profiles Architecture
tags: [runtime-policy, instance-class, network, mounts, exposure, architecture]
---

## Overview

This document defines the long-term, production-ready model for controlling
runtime infrastructure behavior in Spritz without exposing raw Kubernetes
configuration to end users.

The target outcome is:

- one stable public control-plane concept for instance behavior
- typed infrastructure policy, not free-form pod customization
- environment-specific enforcement without environment-specific API shapes
- a model that is narrow enough to ship incrementally and durable enough to
  keep long term

## Problem

Some runtime differences are not just image differences.

Examples:

- one deployment may allow access to internal observability systems
- another deployment may allow only internal cluster services
- one runtime may expose ACP internally but not HTTP ingress
- one runtime may mount projected credentials while another may not

These differences are infrastructure policy.

They should not be encoded as:

- image names
- one-off booleans
- arbitrary labels with implicit meaning
- arbitrary user-supplied Kubernetes fields

That approach does not scale, is hard to audit, and becomes impossible to roll
out safely once multiple runtime types exist.

## Design Principles

- Public API should express intent, not raw Kubernetes objects.
- Deployments must be able to resolve the same logical runtime differently in
  different environments.
- The operator must reconcile concrete resources from typed policy input.
- Invalid or missing policy must fail closed.
- Policy changes must be versionable and rollout-friendly.

## Non-goals

- Allowing users to submit arbitrary `NetworkPolicy`, `Service`, `Volume`, or
  `securityContext` fragments.
- Encoding deployment-specific hostnames, namespaces, or credentials in the
  API.
- Making image choice the primary source of infrastructure policy.

## Core Decision

The long-term public concept should remain `InstanceClass`.

`InstanceClass` is the durable control-plane abstraction that says what a
runtime is for.

Runtime infrastructure behavior should be modeled as a typed `runtimePolicy`
attached to an instance class and resolved onto instances.

In other words:

- `InstanceClass` is the public architectural concept
- runtime policy profiles are the typed implementation mechanism

This matches the existing direction that instance classes should become
first-class, versioned resources instead of loosely related deployment config.

## Pinned V1 Contract

This section freezes the first implementation contract so the work can start
without reopening the architecture on every detail.

### Exact v1 API shape

The first version should add one typed field on the instance spec:

```yaml
spec:
  runtimePolicy:
    networkProfile: string
    mountProfile: string
    exposureProfile: string
    revision: string
```

Field rules:

- `networkProfile` is required once `runtimePolicy` is present
- `mountProfile` is required once `runtimePolicy` is present
- `exposureProfile` is required once `runtimePolicy` is present
- `revision` is required on resolved instances

The first version does not need a separate public `status.runtimePolicy`
contract. That can be added later if the operator needs to surface applied
state or drift information explicitly.

### Ownership

Ownership must be strict:

- presets or instance classes select the logical policy intent
- resolvers or provisioners write the resolved `spec.runtimePolicy`
- the operator reads `spec.runtimePolicy` and reconciles Kubernetes resources
- end users must not be allowed to set or override `spec.runtimePolicy`

This should be enforced the same way Spritz already reserves fields such as
`spec.serviceAccountName` for provisioner-owned mutation.

### Exact v1 reconciler scope

The first version should reconcile only these Kubernetes concerns:

- `NetworkPolicy`
- volume and mount wiring
- internal `Service` and exposure wiring

That means:

- network egress and ingress behavior from `networkProfile`
- `Secret`, `ConfigMap`, projected token, and shared mount wiring from
  `mountProfile`
- ACP, service-port, and optional ingress or gateway exposure behavior from
  `exposureProfile`

The first version should not include:

- raw pod security controls
- resource requests and limits
- node placement and scheduling policy

### Profile catalog location

The first version should keep the runtime policy catalog in deployment-owned
configuration, not a new API resource.

That means:

- the Spritz API shape is stable
- deployments provide the catalog and environment-specific resolution
- the operator consumes resolved instance policy, not deployment-specific
  authoring syntax

This keeps v1 small while preserving the long-term path toward first-class
`InstanceClass` resources.

### Revision semantics

`spec.runtimePolicy.revision` must be deterministic.

It should be computed from canonical JSON of the fully resolved runtime policy
content:

- resolved `networkProfile`
- resolved `mountProfile`
- resolved `exposureProfile`

Canonicalization rules:

- sort object keys recursively
- preserve array order
- trim insignificant surrounding whitespace in string-valued config inputs
- emit UTF-8 JSON without pretty-print formatting

The revision format should be:

```text
sha256:<hex>
```

No hidden deployment inputs should affect the revision outside the resolved
typed content.

### Failure behavior

The first version must fail closed.

If any referenced profile is:

- missing
- invalid
- unsupported by the current deployment
- inconsistent with the resolved instance class

then create or reconcile must fail.

It must not:

- silently fall back to a broader default
- drop the unknown field and continue
- substitute some deployment-wide catch-all policy

### Naming

The first version should freeze the names:

- `runtimePolicy`
- `networkProfile`
- `mountProfile`
- `exposureProfile`

Do not introduce aliases or parallel terms in v1.

## Public Model

### 1. Presets select an instance class

Presets should continue to select a named `InstanceClass`.

Example:

```yaml
presets:
  - id: internal-dev
    image: registry.example.com/spritz-codex-dev:latest
    instanceClass: privileged-dev
```

Presets should not carry raw Kubernetes networking or mount definitions.

### 2. InstanceClass owns a runtime policy contract

The class definition should carry a typed `runtimePolicy` section.

Example long-term shape:

```yaml
apiVersion: spritz.dev/v1alpha1
type: InstanceClass
metadata:
  name: privileged-dev
spec:
  creation:
    requireOwner: true
    requiredResolvedFields: [serviceAccountName]
  runtimePolicy:
    networkProfile: internal-dev
    mountProfile: internal-dev
    exposureProfile: internal-acp
```

The first version does not need `InstanceClass` to be a separate CRD yet. It
can still live in deployment config temporarily. The important point is that
the public model is class-based and typed.

### 3. Resolved runtime policy is materialized on the instance

When an instance is created, Spritz should materialize the resolved runtime
policy onto the instance spec.

Example:

```yaml
spec:
  image: registry.example.com/spritz-codex-dev:latest
  serviceAccountName: privileged-dev-a1b2c3
  runtimePolicy:
    revision: sha256:example
    networkProfile: internal-dev-production
    mountProfile: internal-dev-production
    exposureProfile: internal-acp
```

This field should be reserved for provisioner and resolver output. End users
should not be allowed to set it directly.

## Runtime Policy Facets

The first production-ready scope should stay narrow.

### Required in the first version

- `networkProfile`
- `mountProfile`
- `exposureProfile`

These three cover the main infrastructure boundary for privileged runtimes:

- what they can reach
- what credentials or config are mounted
- how they are exposed inside or outside the cluster

### Intentionally out of scope for the first version

- raw `securityContext`
- raw resource requirements
- raw node selectors and tolerations

Those may become typed policy facets later if needed, but they should not be
part of the initial runtime-policy surface.

## Deployment-Owned Policy Catalog

Profile names in Spritz should be logical, not environment-specific.

Examples:

- `internal-dev`
- `internal-acp`
- `restricted-runtime`

Deployments should map those logical profiles to concrete environment-specific
realizations outside this repository.

For example, a deployment may resolve:

- `internal-dev` in staging to one network and mount shape
- `internal-dev` in production to a stricter or broader shape

That keeps the API stable while allowing deployers to enforce different network
and identity boundaries per environment.

## Operator Reconciliation Model

The operator should be the only component that turns runtime policy into
concrete Kubernetes resources.

At minimum, the operator should reconcile:

### Network profile

- labels or selectors on pods
- `NetworkPolicy`
- optional gateway-only egress wiring
- optional internal-only service reachability

### Mount profile

- `Secret`, `ConfigMap`, and projected-token volume mounts
- shared mount declarations when enabled
- deterministic mount paths and read-only modes

### Exposure profile

- ACP port exposure
- internal `Service` ports
- optional HTTP ingress or gateway configuration
- internal-only vs externally routable exposure

The operator should stamp deterministic labels that reflect the resolved policy,
for example:

- `spritz.sh/network-profile=internal-dev-production`
- `spritz.sh/mount-profile=internal-dev-production`
- `spritz.sh/exposure-profile=internal-acp`

Those labels are implementation detail, not the primary API.

## Versioning and Rollouts

Runtime policy must be revisioned.

Each resolved policy should produce a deterministic revision hash from the
resolved typed content, for example:

- network profile id and resolved parameters
- mount profile id and resolved parameters
- exposure profile id and resolved parameters

Why this matters:

- policy changes become explicit rollout inputs
- operators can detect drift
- deployments can recreate or update instances intentionally
- audit trails can record which runtime had which resolved policy revision

Recommended fields:

```yaml
spec:
  runtimePolicy:
    revision: sha256:example
    networkProfile: internal-dev-production
    mountProfile: internal-dev-production
    exposureProfile: internal-acp

status:
  runtimePolicy:
    observedRevision: sha256:example
    appliedRevision: sha256:example
    conditions: []
```

## Failure Model

Runtime policy must fail closed.

If any referenced profile is:

- missing
- invalid
- not supported by the current deployment

then create or reconcile must fail instead of silently falling back to a more
permissive default.

This is especially important for network and mount behavior. Silent fallback is
how privileged access leaks across environments.

## Why This Is Better Than Free-Form Customization

The alternative would be to let presets or instances carry arbitrary Kubernetes
fields such as:

- raw `NetworkPolicy` snippets
- raw `volumes` and `volumeMounts`
- raw ingress or service fragments

That is not a durable platform API.

It creates:

- non-portable presets
- unbounded security review surface
- no stable reconciliation contract
- poor auditability
- weak rollout semantics

Typed runtime policy profiles avoid all of those problems.

## Narrow First Implementation

The first implementation should follow the pinned v1 contract above exactly:

1. add `spec.runtimePolicy`
2. support only:
   - `networkProfile`
   - `mountProfile`
   - `exposureProfile`
   - `revision`
3. reserve the field for resolver and provisioner output
4. reconcile only network, mounts, and exposure
5. compute deterministic revisions from resolved typed content
6. fail closed on missing or invalid policy

This is small enough to implement now and broad enough to avoid a temporary
hack.

## Evolution Path

### Phase 1

- config-defined instance classes
- config-defined runtime policy profile catalog
- resolved `spec.runtimePolicy`
- operator reconciliation for network, mounts, and exposure

### Phase 2

- make `InstanceClass` a first-class versioned resource
- let presets reference class ID and version explicitly
- keep the same `runtimePolicy` resolution and reconciliation contract

### Phase 3

- optionally add new typed policy facets such as:
  - `securityProfile`
  - `resourceProfile`
  - `schedulingProfile`
- only after real requirements appear

This path keeps the first version compatible with the long-term model.

## Decision

The most elegant long-term solution is:

- keep `InstanceClass` as the public behavioral concept
- add typed `runtimePolicy` facets for infrastructure behavior
- let deployers resolve logical profiles differently per environment
- let the operator reconcile concrete Kubernetes resources
- version resolved policy and fail closed on invalid input

That gives Spritz a production-ready infrastructure policy model without
turning the public API into arbitrary Kubernetes passthrough.

## References

- `docs/2026-03-19-unified-extension-framework-architecture.md`
- `docs/2026-03-31-shared-channel-concierge-lifecycle-architecture.md`
- `operator/api/v1/spritz_types.go`
- `crd/spritz.sh_spritzes.yaml`
