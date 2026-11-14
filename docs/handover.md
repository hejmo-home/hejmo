# Handover to the build agent: Hejmo version 1

> Translated from the original German specification (frozen as of 14/11/2026). Scope and decisions made after that date are recorded as ADRs in `docs/adr/` and take precedence over this document.

Your task: build version 1 of Hejmo, a self-hosted family organizer, strictly test-driven in Go, runnable as a Docker container on a Raspberry Pi. The authoritative sources are the "Specification" tab (`docs/spec.md`: features, data model, acceptance criteria for version 1) and the "Nordic Calm" design specification. Work through the packages below in order and submit a pull request for review after each milestone.

## Scope of version 1

Version 1 covers the everyday life of one household. The "Version 1" column of the version planning in the specification is binding:

- Platform: Docker (ARM64/x86), SQLite, web admin with setup wizard, parent PIN, updates at night at 03:30 with rollback, REST API with token (OpenAPI), optional MCP server (off by default), box as CalDAV and CardDAV server
- Calendar: day/week/month/agenda, series (this one / this and following / all), multi-day bars, public holidays DACH, full-text search, assignment to people and subgroups, two-way sync with CalDAV/Google/iCloud/Outlook/ICS, X-properties plus human-readable summary
- People: profiles, roles parent/child, family and personal view (return after 30 seconds)
- Tasks: tasks with subtasks, routines, emojis, stars with log, checking off on the go, export as VTODO
- Lists and meals: default lists (shopping, to-dos), sorting, colour/icon per list, shared meal plan in the calendar
- Displays: one E-Ink display plus LAN web view, pairing via QR/code, colour and black-and-white mode, portrait/landscape, quiet mode, brightness by sun position, screensaver from photo folder
- Other: waste collection calendar via ICS subscription with reminder the evening before

## Not in version 1

Do not build, not even partially, unless the data model requires it: timetable, journey times, drop-off/pick-up, childcare, shared-custody model, multiple households, contact book, recipes, Mealie, routing, remote access (NetBird/WireGuard), Matrix, Home Assistant, pocket money, push, native mobile app. However, the data model must not block these extensions. No built-in AI.

## Working rules

- TDD is mandatory: each acceptance criterion is first written as a failing test, then implemented, then refactored. No production code without a preceding red test.
- Test levels according to the test strategy: unit tests (Go), integration tests against real SQLite and Radicale as a CalDAV test server in a container, API contract tests against the OpenAPI file, end-to-end with Playwright including image comparison of the E-Ink rendering.
- Definition of done per package: all associated acceptance criteria green as tests, CI green on ARM64 and x86, OpenAPI up to date, README section for the package updated, no new dependencies without justification in the PR.
- Small, traceable commits; one pull request per milestone.

## Technical guardrails

- Go backend as a single binary; SQLite (FTS5 for full-text search); no separate DB server
- User interface rendered server-side (Go templates) with htmx; no JavaScript framework, no frontend build
- Push to displays via Server-Sent Events; displays reload only when something changes
- Own data model, translation to iCal/vCard during sync; the box is the leading data source
- Resource targets: below 150 MB RAM and below 5 % CPU at idle on a Raspberry Pi 4; API responses below 200 ms
- Security: passkeys as default, fallback password plus TOTP; credentials for external calendars encrypted; no outgoing connections except to configured calendars, the weather service (Open-Meteo) and the update server
- Texts translatable from the start, shipped in German and English
- Licence AGPL-3.0; monorepo on GitHub; CI with GitHub Actions

## Design specification

The default style is "Nordic Calm" (German original: "Nordisch ruhig") (draft 2) for colour displays and its black-and-white variant (draft 2b) on the "Hejmo – Style directions" canvas (design drafts 2 and 2b). Adopt the grid, cards, typography (Figtree), spacing and person markers; in black-and-white mode, shapes instead of colours (circle filled/empty, square filled/empty), journeys dashed. All values (colours, typography, spacing, shapes) are binding as set out in the "Specification" tab, section "Design specification 'Nordic Calm'". Design further screens in the same style. Target devices: Android E-Ink tablets with touch (e.g. Boox, Bigme); touch targets at least 44 px, no animations on E-Ink.

## Work packages for version 1

The order is binding; each package ends with a milestone PR.

| No. | Package | Content | Acceptance criteria (specification) |
|---|---|---|---|
| 1 | Foundation | Repo structure, CI (ARM64/x86), Docker image and Compose, health check, setup wizard, parent PIN, i18n scaffolding | Platform |
| 2 | Data model and API | SQLite schema with migrations, domain model (people, roles, events, tasks, lists, stars, displays), REST API with OpenAPI, tokens with rights, sign-in (passkey, TOTP) | Platform; People and permissions |
| 3 | Calendar and sync | Events, series, public holidays DACH, full-text search, subgroups; sync CalDAV/Google/iCloud/Outlook/ICS; box as CalDAV/CardDAV server; X-properties and human-readable summary | Calendar and sync |
| 4 | Displays | Pairing via QR/code, "Nordic Calm" rendering in colour and black-and-white, family and personal view, SSE updates, quiet mode, brightness, portrait/landscape, LAN web view | Displays; People and permissions |
| 5 | Tasks and stars | Tasks with subtasks, routines, emojis, stars with log, VTODO export with one-time crediting | Tasks and stars |
| 6 | Lists, meals, photos, waste collection | Default lists, sorting, colour/icon, shared meal plan in the calendar, screensaver, waste collection ICS with reminder | Lists and meals; Photos and waste collection |
| 7 | Hardening and release | MCP server (off by default), updates at 03:30 with rollback, resource and security measurement, installation guide, release 1.0 | Non-functional criteria; Platform |

## Decision framework

You decide yourself and document it in the PR:

- Details of the API (endpoints, fields, error codes) within the scope of the specification
- Layouts of further screens and of the web interface in the "Nordic Calm" style
- Internal structure, package layout and libraries, as long as they comply with the guardrails

You submit beforehand and wait for approval:

- Any deviation from scope, guardrails or acceptance criteria
- New external services, network connections or dependencies with a licence that is not AGPL-compatible
- Changes to the data model that would make later versions (shared-custody model, multiple households) harder

## Open points and assumptions

- Trademark check "Hejmo" (DPMA/EUIPO, classes 9 and 42) is pending; domain still open (suggestions: hejmo-project.org, gethejmo.org). Until then, use the name only in the repo.
- Assumed target values, adjust if needed: 150 MB RAM and 5 % CPU at idle, 200 ms API response time, 10 seconds until a change is shown on E-Ink, 60 seconds from start until the web interface is reachable, waste collection reminder at 18:00, sync interval 5 minutes.
- Specific reference tablet for testing not yet decided; until then, a resolution of 1024×768 in landscape as the reference.
