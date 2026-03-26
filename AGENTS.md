# AGENTS.md

## Scope

This repository is open-source and must remain provider- and organization-agnostic.

## Agnostic Rules (Mandatory)

- Never add TextCortex-specific values to code, tests, docs, examples, or comments.
- Never include organization-specific domains, project IDs, cluster names, account IDs, emails, tokens, or credentials.
- Never use TextCortex hostnames in test fixtures.
- New or changed data structures, schemas, DTOs, CRDs, and API models must use `type` as the field name, never `kind`. If an external payload already sends `kind`, convert it at the boundary with an alias and keep the internal field named `type`.

Exception:

- Documentation `author` front matter may use a real maintainer identity, including a real email address, when the document is intentionally attributed to that maintainer.

Use neutral placeholders instead:

- domains: `example.com`, `console.example.com`
- emails: `user@example.com`
- IDs: `example-project`, `example-cluster`, `example-account`

If environment-specific wiring is required, keep it outside this repository.
