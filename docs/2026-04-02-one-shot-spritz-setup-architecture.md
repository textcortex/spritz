---
date: 2026-04-02
author: Onur Solmaz <onur@textcortex.com>
title: One-Shot Spritz Setup Architecture
tags: [spritz, setup, deployment, install, architecture, operations]
---

## Overview

The goal is simple:

- one environment definition
- one deploy command
- one install click
- one working message

A new operator should not need ad hoc `kubectl` fixes, hidden secret names,
staging-only knowledge, or multiple retries before the first shared-channel
message works.

This document defines the target architecture for making Spritz setup feel like
a real one-click cloud install:

1. declare one environment
2. run one deploy
3. run one blocking readiness check
4. hand out one install link
5. send one test message and get one reply

Related docs:

- [Simplest Spritz Deployment Specification](2026-02-24-simplest-spritz-deployment-spec.md)
- [Shared-Host Auth Gateway Architecture](2026-03-20-shared-host-auth-gateway-architecture.md)
- [Shared Channel Concierge Lifecycle Architecture](2026-03-31-shared-channel-concierge-lifecycle-architecture.md)
- [Channel Install Result Surface](2026-04-02-channel-install-result-surface.md)
- [Slack Channel Gateway Implementation Plan](2026-03-24-slack-channel-gateway-implementation-plan.md)

## Problem

Today, shared-channel setup still has too many friction points:

- deployment state and runtime state can drift
- staging and production can silently diverge
- install can succeed but the first message can still fail later in the flow
- some required secrets, tables, or auth settings are not validated before
  exposing the install link
- infrastructure errors can still leak through instead of landing on a product
  result page
- operators still need manual debugging knowledge to finish what should have
  been a normal install

The core issue is not only "deployment is hard". The real issue is that Spritz
does not yet define one complete installation contract from deploy to first
working message.

## Goals

- Make first-time environment setup work with the fewest possible steps.
- Make deploys fail early when required setup is incomplete.
- Make install fail with a clear product error instead of a generic proxy page.
- Make the first shared-channel message part of the installation success
  contract, not a separate manual test.
- Keep Spritz core provider- and organization-agnostic.
- Keep environment-specific wiring outside this repository.

## Non-goals

- Embedding provider-specific secrets, domains, or infrastructure values in
  this repository.
- Replacing all advanced deployment shapes with one mandatory topology.
- Hiding all operational detail from advanced operators.
- Solving every runtime-specific business policy inside Spritz core.

## Plain-English End State

In the target model, a new environment should feel like this:

1. the operator creates one environment definition
2. the operator runs one deploy command
3. Spritz runs all preflight checks automatically
4. Spritz refuses to expose install links until the environment is actually
   ready
5. the operator opens one install link
6. the provider install succeeds or fails on a Spritz-owned result page
7. the first shared-channel message works without extra operator action

If any requirement is missing, the deploy or readiness check must stop with one
clear error and one clear owner.

## Design Principles

### One source of truth

Every environment must be described from one canonical spec. Spritz should not
require operators to remember extra, undocumented side steps after the main
deploy.

### Fail before exposure

If secrets, auth, schema, routing, or runtime contracts are incomplete,
Spritz must fail before the install link is considered ready.

### Setup is not complete until the first message works

For shared-channel environments, "install succeeded" is not enough. Setup is
only complete when the first message can traverse the full path:

- provider callback
- installation finalization
- session exchange
- conversation upsert
- ACP bootstrap
- instance connect
- outbound reply

### Product-controlled errors

Expected failure modes must terminate on a Spritz-owned surface with a stable
error code and request ID.

### Core vs. deployment split

Spritz core should own reusable contracts, validation, and UX. Environment
overlays should own provider credentials, backend URLs, secret stores, cloud
resources, and branding.

## The One-Shot Model

The target operator flow should be:

### 1. Define one environment

One environment definition should describe everything Spritz needs to know at a
high level:

- public host
- routing model
- auth mode
- enabled integrations
- runtime presets
- external resolvers
- secret references
- storage profile
- required migrations or schema contracts
- health and smoke targets

This must be one canonical object, even if it is later rendered into Helm
values, secret references, and external deployment manifests.

### 2. Run one deploy

One deploy action should:

- render the environment config
- validate that config statically
- apply Spritz components
- apply shared integration components
- apply auth and routing resources
- register install surfaces
- publish readiness state only after deploy checks pass

The operator should not need separate "main deploy", "follow-up auth patch",
"temporary routing fix", and "manual runtime test" steps.

### 3. Run one blocking readiness check

Readiness must be stricter than pod liveness.

An environment is not ready just because:

- pods are running
- ingress exists
- the install page loads

It is ready only when all required setup contracts are valid.

### 4. Expose one install link

The install link should only be presented after readiness passes. If readiness
fails, the operator should get a machine-readable and human-readable report.

### 5. Verify one working message

For shared-channel installs, Spritz should support one synthetic or guided
message test that confirms the end-to-end path after install.

## Required Architectural Components

## 1. Canonical Environment Specification

Spritz needs one canonical environment spec. This should be the source for:

- chart values
- integration deployment values
- route configuration
- auth configuration
- secret references
- smoke-test expectations

At minimum, the environment spec must define:

- identity
  - environment name
  - public host
  - namespace map
- routing
  - API path prefix
  - auth path prefix
  - instance path prefix
  - install result path
- authentication
  - UI/browser auth mode
  - bearer introspection contract
  - JWT validation contract
  - trusted proxy headers, if used
  - static service principals, if used
- integrations
  - which shared providers are enabled
  - provider public base URL
  - provider callback URL base
  - install result route
- control-plane extensions
  - external owner resolution
  - runtime binding resolution
  - agent binding resolution
  - profile sync resolution
- runtime catalog
  - preset catalog
  - instance classes
  - runtime policy profile references
- storage
  - shared mounts policy
  - required buckets, remotes, or equivalents
- validation
  - required secrets
  - required internal endpoints
  - required schema version or migration identifiers
  - smoke-test targets

This spec can be provider-agnostic and deployment-agnostic by using secret
references and generic endpoint contracts rather than hardcoded vendor values.

## 2. Preflight And Readiness Engine

Spritz needs a first-class preflight engine, effectively a `doctor` step.

The preflight must block readiness if any required dependency is missing.

### Static checks

These checks should run before any deploy action:

- required environment fields are present
- route prefixes do not conflict
- auth mode combinations are valid
- enabled integrations have matching config blocks
- required secret references are declared
- preset catalog is internally consistent
- install result surface is configured

### Live dependency checks

These checks should run against the target environment:

- referenced secrets exist
- database schema or installation registry tables exist
- external resolvers are reachable
- bearer introspection endpoint is reachable
- bearer type parsing is configured correctly
- JWT issuer and audience settings match the environment contract
- shared mounts backing store is reachable when enabled
- runtime-binding and owner-resolution endpoints respond with the expected
  contract

### Shared-channel flow checks

If a shared provider is enabled, preflight must also validate:

- provider install callback route resolves correctly
- install finalizer endpoint is reachable
- channel session exchange works
- conversation upsert auth path works
- ACP bootstrap auth path works
- connect-ticket or runtime connect path works

These should not require a real end-user install to prove the environment is
deployable.

## 3. One Deploy Path

Spritz needs one supported deploy path for a normal environment.

That path must own all of the following:

- core Spritz workloads
- auth gateway resources
- route objects
- integration deployments
- secret reference objects
- environment validation
- post-deploy readiness publication

The deploy action should produce one result:

- `ready`
- `not_ready`

If `not_ready`, it should print a structured report grouped by failure domain:

- config
- secrets
- schema
- auth
- routing
- runtime
- integration

## 4. Explicit Secret Contract

Spritz should define a machine-readable secret contract for each environment.

That contract should separate:

- secrets Spritz core requires
- secrets each integration requires
- secrets each external resolver requires
- optional feature-gated secrets

Each secret definition should include:

- logical name
- owner component
- whether it is required
- expected key names
- what feature depends on it
- whether missing it should block deploy, block readiness, or only disable a
  feature

The deploy path should render missing-secret errors against this contract
before the install link is exposed.

## 5. Explicit Schema Contract

Shared-channel setup cannot depend on operators remembering to create tables
manually after the deploy.

Spritz should define a schema contract for features that require persistent
storage. That contract should say:

- what logical storage objects must exist
- what schema version is required
- whether Spritz manages it directly or expects an external migration step
- how readiness checks validate it

There are two acceptable models:

- Spritz-owned migrations
- externally managed schema with explicit readiness validation

The unacceptable model is:

- deployment succeeds
- install link is exposed
- first install fails because required storage objects were missing

## 6. One Install Contract

Spritz must define one complete install contract, not just an OAuth callback.

For a shared-channel provider, install is only complete after:

- callback state validation
- provider code exchange
- installation finalization
- installation storage write
- route resolution
- live runtime resolution or provisioning
- first message path readiness

Spritz should model install states explicitly:

- `not_started`
- `pending`
- `ready`
- `failed`
- `retryable_failure`

The install link should only advertise success when the environment can handle
the next expected action.

## 7. Install Result Surface

Spritz should always end the browser flow on a Spritz-controlled result
surface.

That result must include:

- status
- stable error code when failing
- provider
- request ID
- retryability
- next step

This is already the right direction for install UX. It should be treated as a
hard requirement for one-shot setup because operators cannot debug generic edge
errors during first install.

## 8. First-Message Smoke Test

Spritz needs one post-deploy smoke test that proves the first message path.

For shared-channel setups, the smoke must cover:

- session exchange
- conversation upsert
- ACP bootstrap
- session connect
- reply dispatch

There are two useful forms:

- providerless synthetic smoke
  - runs directly against the internal APIs and gateways
  - required for CI and deploy gates
- provider-backed smoke
  - uses a real test workspace or equivalent
  - optional but recommended for production-like environments

Setup should not be considered complete until the providerless synthetic smoke
passes.

## 9. Environment Parity Checks

Spritz needs explicit parity rules between environments.

This does not mean every environment must be identical. It means that for any
feature marked as shared, the system must detect accidental drift in:

- auth config
- route config
- integration config
- preset config
- readiness expectations

Examples of parity assertions:

- a bearer token `type` path configured in one environment but missing in
  another
- shared-provider callback routes present in one environment but not another
- install result surface enabled in one environment but not another

Parity checks should run in CI against the canonical environment specs.

## 10. Runtime Readiness As A First-Class Contract

Shared-channel installs depend on a live runtime, not just a saved record.

Spritz should already treat live routing as a contract:

- session exchange must not claim success unless the runtime path is usable
- stale bindings must be invalidated automatically
- provisioning and recovery must be serialized

To make setup one-shot, this needs one more rule:

- if the runtime is not ready to handle the first message, install success
  should degrade to a controlled pending or unavailable state, never a generic
  later failure

## 11. Auth Model That Matches The Full Flow

One-shot setup depends on auth being consistent across every hop.

Spritz must define, and validate, the expected caller shape for each
integration action:

- install finalization
- route resolve
- session exchange
- conversation upsert
- ACP bootstrap
- runtime connect
- outbound reply

For each step, the contract must specify:

- which caller type is allowed
  - human
  - service
  - admin
  - scoped channel caller
- which scope is required
- which bearer fields must be preserved
- whether the caller is allowed to act on behalf of an owner

This must be validated in both tests and readiness checks. Shared-channel setup
breaks quickly when one step assumes `human` and the next step assumes
`service`.

## 12. Operator-Facing Commands

The target user experience should collapse to three top-level commands:

### `spritz doctor`

Runs:

- static config validation
- secret existence checks
- auth contract checks
- schema checks
- route checks
- shared integration readiness checks

Output:

- one pass/fail summary
- structured failure groups
- exact remediation targets

### `spritz deploy`

Runs:

- render
- apply
- wait
- doctor
- publish readiness state

Output:

- deploy summary
- readiness summary
- install URL only when ready

### `spritz smoke`

Runs:

- core API smoke
- shared-provider flow smoke
- first-message smoke

Output:

- one pass/fail result
- request IDs and step-local errors

These names are illustrative. The important part is the shape: one preflight,
one deploy, one smoke.

## Responsibility Split

## Spritz core should own

- canonical environment schema
- config validation
- readiness engine
- install result surface
- shared integration contracts
- first-message smoke contract
- parity checks
- typed error taxonomy
- provider-agnostic install lifecycle

## Environment overlays outside this repo should own

- real domains
- secret stores and secret values
- cluster names
- cloud resources
- backend endpoints
- branding
- provider app credentials
- migration execution when schema is not Spritz-managed

The core repo should define the contract. Environment overlays should satisfy
that contract.

## Release And Deploy Gates

To make setup one-shot in practice, deploy pipelines should not be allowed to
publish a "successful" environment unless:

- deploy succeeded
- doctor passed
- required schema was validated
- auth contract checks passed
- install result route is reachable
- first-message providerless smoke passed

Optional stronger gate:

- provider-backed smoke passed in a dedicated test tenant

## Error Model

Every setup stage should return typed errors.

Recommended top-level setup error families:

- `config_invalid`
- `secret_missing`
- `schema_unavailable`
- `schema_version_invalid`
- `auth_contract_invalid`
- `route_contract_invalid`
- `resolver_unavailable`
- `install_finalizer_unavailable`
- `runtime_unavailable`
- `smoke_failed`

Every error should include:

- code
- summary
- component owner
- blocking level
- remediation hint
- request ID or check ID when applicable

## Validation Checklist

An environment can be called "one-shot ready" only if all of the following are
true:

- one canonical environment definition exists
- one deploy action can render and apply the environment
- all required secrets are declared and validated
- all required schema objects are declared and validated
- auth contracts for all shared-provider steps are validated
- route contracts are validated
- install result surface is enabled
- provider callback no longer falls through to infrastructure error pages
- session exchange, conversation upsert, ACP bootstrap, and connect can all be
  validated as one chain
- a providerless first-message smoke passes
- parity checks protect shared features across environments

## Recommended Implementation Phases

### Phase 1: Canonical spec and doctor

- define the canonical environment schema
- add static validation
- add live secret/auth/schema checks
- add a single readiness report

### Phase 2: Deploy owns readiness

- make deploy call doctor automatically
- publish install URLs only after readiness passes
- fail deploy summaries clearly when readiness blocks

### Phase 3: Full install contract

- make install success dependent on a complete shared-channel setup contract
- keep all expected failures on the install result surface
- normalize typed install errors

### Phase 4: First-message smoke

- add providerless synthetic smoke
- gate readiness or release on that smoke
- add optional provider-backed smoke

### Phase 5: Parity and regression protection

- add environment parity checks
- add auth contract regression tests
- add route and integration parity snapshots where useful

## What Success Looks Like

When this architecture is done, a new operator should be able to say:

- I filled out one environment definition.
- I ran one deploy.
- The system told me whether it was ready.
- I got one install link.
- I sent one test message.
- It worked without a manual fix.

That is the standard Spritz should aim for.

