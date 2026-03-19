---
date: 2026-03-13
author: Onur Solmaz <onur@textcortex.com>
title: ACP Smoke Harness Contract
tags: [spritz, smoke-tests, acp, e2e]
---

## Overview

This document defines the contract for the Spritz ACP smoke harness.

The smoke harness is the production-facing regression check for the core external provisioner and ACP workflow:

- create an instance through the service-principal `spz` path
- wait for the instance to reach `Ready`
- wait for ACP to reach `ready`
- open a real ACP session against the instance
- send a prompt and verify a real assistant reply
- verify that the service principal is still denied on post-create operations

The harness is intentionally narrow. It exists to prove the platform-critical path, not to replace broader integration testing.

## Scope

The harness currently targets example ACP-capable presets such as:

- `openclaw`
- `claude-code`

The runner lives in:

- `e2e/acp-smoke.mjs`

Supporting owners live in:

- `e2e/acp-smoke-lib.mjs`
- `e2e/acp-client.mjs`
- `e2e/workspace-waiter.mjs`

## Required Inputs

The smoke runner must fail fast unless these inputs are provided explicitly:

- `SPRITZ_SMOKE_API_URL`
- `SPRITZ_SMOKE_BEARER_TOKEN`
- `--owner-id`
- `--presets`

Optional inputs:

- `--namespace`
- `--timeout-seconds`
- `--prompt`
- `--idempotency-prefix`
- `--keep`

The runner must not depend on ambient `spz` profile state or shell auth.

## Required Cluster Assumptions

The caller must have:

- `kubectl` access to the target cluster
- permission to port-forward to instance services
- permission to read `Spritz` resources and instance pods in the target namespace

The service-principal bearer token used by the smoke must have enough scope to:

- create an instance for the target owner

It must not have permission to:

- list all instances
- delete the created instance

Those denials are part of the contract and are asserted by the smoke.

## What The Harness Proves

The smoke is considered successful only if all of the following are true for each tested preset:

- the service-principal create path succeeds
- the create response includes canonical URLs
- the returned instance name is stable under idempotent replay
- a mismatched replay with the same idempotency key fails
- the service principal is denied on `list`
- the service principal is denied on `delete`
- the instance reaches `Ready`
- ACP reaches `ready`
- the instance advertises a usable ACP endpoint
- a real ACP prompt produces real assistant output containing the expected smoke token

## What The Harness Does Not Prove

The harness does not attempt to prove:

- browser login or browser routing behavior
- visual UI state transitions
- terminal or SSH behavior
- long-lived conversation restore
- multi-thread ACP behavior
- autoscaling behavior
- organization-specific auth or networking overlays

Those need separate tests.

## Execution Contract

### Local or operator-driven execution

Example shape:

```bash
SPRITZ_SMOKE_API_URL=http://127.0.0.1:18083/api \
SPRITZ_SMOKE_BEARER_TOKEN=example-token \
node e2e/acp-smoke.mjs \
  --owner-id example-owner-id \
  --namespace example-namespace \
  --presets openclaw,claude-code
```

### CI-oriented execution

CI usage must follow the same contract:

- the API URL must be provided explicitly
- the bearer token must be provided explicitly
- the presets must be provided explicitly
- cleanup must remain enabled unless `--keep` is deliberately requested

The smoke output is newline-delimited JSON, one object per tested preset, emitted to stdout.

Errors and failure explanations are emitted to stderr.

## Cleanup Rules

Default behavior:

- every created instance is deleted during normal completion
- every created instance is also deleted during failure unwind when possible

`--keep` is opt-in only and exists for manual debugging.

CI or operator workflows should not rely on `--keep`.

## Ownership Rules

The harness enforces a strict owner and actor model:

- the created instance owner must be the requested human owner
- the actor must remain a service principal
- create authority must not imply post-create control

This is why the smoke includes explicit denial checks after creation.

## Invariants

The smoke contract must not silently weaken these invariants:

- create must go through the service-principal path
- canonical URLs must be returned by the API
- idempotent replay must return the same instance
- mismatched replay must fail
- service principal list must fail
- service principal delete must fail
- instance readiness must be real, not inferred
- ACP readiness must be real, not inferred
- prompt completion must include assistant output containing the expected token

If any future change needs to alter one of these rules, update this document and the smoke implementation together.

## Validation

At minimum, changes to the smoke harness should run:

```bash
node --test e2e/acp-smoke-lib.test.mjs \
  e2e/acp-client.test.mjs \
  e2e/workspace-waiter.test.mjs

node --check e2e/acp-smoke-lib.mjs \
  e2e/acp-client.mjs \
  e2e/workspace-waiter.mjs \
  e2e/acp-smoke.mjs
```

For behavior changes, rerun at least one real smoke against a live cluster after the refactor.

## References

- `docs/2026-03-09-acp-port-and-agent-chat-architecture.md`
- `docs/2026-03-10-acp-conversation-storage-and-replay-model.md`
- `docs/2026-03-11-external-provisioner-and-service-principal-architecture.md`
