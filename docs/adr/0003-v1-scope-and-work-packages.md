# 0003. V1 scope and work packages

- Status: Accepted
- Date: 14/11/2026
- Replaces: work packages 1–7 in `docs/handover.md`

## Context

The handover defined seven work packages for V1, covering two-way sync with four providers, a CalDAV/CardDAV server, MCP, auto-update and more. The first usable display would only have arrived after most of them. The founding family's child is a toddler, so stars and child routines bring little value at home yet, while they matter for other families.

## Decision

V1 is split into **V1.0** (family core, 0.x releases) and **V1.x** (up to release 1.0).

### V1.0 work packages (order binding, one pull request per package)

| No. | Package | Content |
|---|---|---|
| 1 | Foundation | Repository, CI (ARM64/x86), Docker image and Compose, health check, i18n (DE/EN), setup wizard |
| 2 | Data model, API, sign-in | SQLite schema with migrations, people and roles, REST API with OpenAPI and scoped tokens, password + TOTP, display PIN |
| 3 | Calendar | Events, series (this / this and following / all), multi-day bars, DACH holidays, full-text search, subgroups |
| 4 | Sync | ICS read-only, iCloud via CalDAV, Google via per-install OAuth client, X-properties plus readable summary |
| 5 | Display | Pairing via QR/code, family view "Nordic Calm" (colour and black-and-white, one column per person, weather at home location), personal view with 30 s return, SSE updates, portrait/landscape, sleep mode via kiosk adapter |
| 6 | Everyday | Lists (default lists, sorting, colour/symbol), shared meal plan in the calendar, simple tasks, waste collection ICS with reminder |

### Moved to V1.x (before release 1.0)

Stars with log and child routines, subtasks, VTODO export with one-time credit, box as CalDAV/CardDAV server, Outlook sync, passkeys (when HTTPS is set up), photo screensaver, brightness control beyond "front light off at night", auto-update at 03:30 with rollback, MCP server (off by default), resource and security measurement, install guide.

### Unchanged

Everything listed under "Not in version 1" in the handover stays out. The data model must still not block V2/V3 features (shared custody, multiple households) and already models stars and routines.

## Consequences

- After package 5 a real display shows real appointments.
- Acceptance criteria of moved features are implemented in V1.x, not dropped.
- Package 1 includes a basic setup wizard; the parent PIN moves to package 2 (see ADR 0006).
