# 0002. Goals and release model

- Status: Accepted, amended by [0018](0018-path-to-0-1.md)
- Date: 14/11/2026

## Context

Hejmo serves two goals: the founding family wants to use it at home soon, and it is meant to become a public open-source project that other families install. The original V1 scope was too large to reach either goal quickly.

## Decision

- Both goals carry equal weight.
- **V1.0 (family core)** is published as public pre-releases **0.x**. The founding family runs it at home.
- **Release 1.0** follows once **V1.x** is complete, including install guide, auto-update with rollback and the remaining V1 features (see ADR 0003).
- The repository is public from the start.

## Consequences

- Early adopters see the project but should not expect stability before 1.0.
- Setup wizard and documentation must be good enough for strangers by 1.0, not by 0.x.
