# 0004. Open-source-only runtime

- Status: Accepted
- Date: 14/11/2026

## Context

Hejmo is licensed under AGPL-3.0. A proprietary kiosk browser (Fully Kiosk) was considered for the displays. Requiring proprietary software to run Hejmo contradicts the project's purpose.

## Decision

- **Runtime:** everything a user must install to run Hejmo (box, display app, optional containers) is open source under an OSI-approved, AGPL-compatible license.
- **Integrations** with proprietary services (Google, iCloud, Outlook) count as interoperability. They are allowed but never required.
- **Development tools** may be proprietary (for example Docker Desktop); open-source tools are preferred.

## Consequences

- Fully Kiosk is not used or recommended (see ADR 0005).
- Every new runtime dependency must state its license in the pull request.
