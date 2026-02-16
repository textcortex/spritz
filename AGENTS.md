# AGENTS.md

## Scope

This repository is open-source and must remain provider- and organization-agnostic.

## Agnostic Rules (Mandatory)

- Never add TextCortex-specific values to code, tests, docs, examples, or comments.
- Never include organization-specific domains, project IDs, cluster names, account IDs, emails, tokens, or credentials.
- Never use TextCortex hostnames in test fixtures.

Use neutral placeholders instead:

- domains: `example.com`, `console.example.com`
- emails: `user@example.com`
- IDs: `example-project`, `example-cluster`, `example-account`

If environment-specific wiring is required, keep it outside this repository.
