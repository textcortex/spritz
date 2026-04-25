# AGENTS.md

## Scope

This repository is open-source and must remain provider- and organization-agnostic.

## Agnostic Rules (Mandatory)

- Never add TextCortex-specific values to code, tests, docs, examples, or comments.
- Never include organization-specific domains, project IDs, cluster names, account IDs, emails, tokens, or credentials.
- Never use TextCortex hostnames in test fixtures.
- New or changed data structures, schemas, DTOs, CRDs, and API models must use `type` as the field name, never `kind`. If an external payload already sends `kind`, convert it at the boundary with an alias and keep the internal field named `type`.

## UI Ownership Rules (Mandatory)

- Add user-facing UI inside the Spritz React app under `ui/`.
- Do not add new server-rendered HTML pages in gateway, API, operator, or integration services.
- Integration and gateway services should expose redirects, callbacks, event ingestion, and JSON APIs for the React UI to consume.
- If a temporary server-rendered page is unavoidable for an external protocol callback, document why it cannot be served by the React app and keep it minimal.
- When touching existing server-rendered integration pages, prefer migrating the surface to React over extending Go templates.

Exception:

- Documentation `author` front matter may use a real maintainer identity, including a real email address, when the document is intentionally attributed to that maintainer.

Use neutral placeholders instead:

- domains: `example.com`, `console.example.com`
- emails: `user@example.com`
- IDs: `example-project`, `example-cluster`, `example-account`

If environment-specific wiring is required, keep it outside this repository.
