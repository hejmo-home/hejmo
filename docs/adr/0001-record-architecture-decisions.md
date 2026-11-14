# 0001. Record architecture decisions

- Status: Accepted
- Date: 14/11/2026

## Context

The specification was written in a shared document owned by another account. A design review on 14/11/2026 changed scope and several technical choices. Contributors need one versioned place for these decisions.

## Decision

- The specification moves into the repository (`docs/spec.md`, `docs/handover.md`), translated into English and frozen as of 14/11/2026.
- Every later decision that changes scope, guardrails, data model or project rules is recorded as an ADR in `docs/adr/`.
- ADRs take precedence over the frozen specification.

## Consequences

- The original document is no longer the source of truth; it should point to this repository.
- Changes to the specification happen through pull requests with an ADR.
