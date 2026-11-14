# 0010. Households, memberships and credentials

- Status: Accepted, amended by [0015](0015-display-web-interface-and-everyday.md)
- Date: 14/11/2026

## Context

V1 serves one household per box. V3 adds the two-home model: children belong to two households (two boxes), and a new partner can be a parent in one household and unknown in the other. The data model must not block this (handover, decision framework).

## Decision

- **Households and people are separate.** `household_members` links them and carries the **role** (`parent`, `child`) and display order. A person can later be a member of several households with different roles.
- **All ids are random UUIDs**, so records from different boxes never collide when boxes sync in V3.
- **Credentials:** passwords and the display PIN are hashed with **Argon2id** (19 MiB, 2 passes, OWASP minimum, sized for a Raspberry Pi 4). TOTP follows RFC 6238 (SHA-1, 6 digits, 30 s, ±1 step) and is implemented in the box, tested against the RFC vectors. Session and API tokens are 256-bit random values, stored as SHA-256 hashes.
- **Permissions** = role × token scopes. Parents may do everything; children may read and tick off tasks and list items. A browser session carries the person's full role; an API token is further limited by its scopes.
- **Setup is complete** once the first parent has confirmed TOTP. A half-finished setup can be restarted.

## Consequences

- Queries always go through a household; V1 uses the box's single home household.
- Grandparents, carers and display devices (later roles) extend the role list and `RoleAllows`.
