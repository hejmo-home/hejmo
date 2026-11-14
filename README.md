# Hejmo

**Your family's day in one place, under your own control.**

Hejmo (Esperanto for "home") is a self-hosted, open-source family organizer. It runs locally on a small box such as a Raspberry Pi and shows the family's day on calm, low-power E-Ink displays on the wall. Calendar, tasks, lists, meal plan and shopping live together; all data stays at home.

- No subscription, no telemetry, no mandatory cloud
- Open standards (CalDAV, iCalendar), connects to Google and iCloud
- Open REST API; open to your own AI assistant, without built-in AI
- Licensed under AGPL-3.0, developed in Europe

> **Status:** early development (pre-release 0.x). Not ready for everyday use yet.

## Documentation

- [Specification](docs/spec.md): features, data model, acceptance criteria, design
- [Build handover](docs/handover.md): original work plan and guardrails
- [Architecture decision records](docs/adr/): decisions that refine or override the specification

## Roadmap

| Release | Content |
|---|---|
| **0.x (V1.0, family core)** | Box and setup wizard, people and roles, calendar with series and DACH holidays, sync (ICS, iCloud, Google), one E-Ink display with the family view, lists, meal plan, simple tasks, waste collection calendar |
| **1.0 (V1.x)** | Stars and routines, subtasks, VTODO export, box as CalDAV/CardDAV server, Outlook, passkeys, screensaver, auto-update with rollback, MCP server, install guide |
| **V2 / V3** | School, childcare and logistics; shared-custody model and multiple households (see the specification) |

See [ADR 0003](docs/adr/0003-v1-scope-and-work-packages.md) for the work packages.

## Development

Requirements: Go (see `go.mod`), Docker.

```bash
go test ./...          # run all tests
go run ./cmd/hejmo     # start the box on http://localhost:8080
docker compose up      # run the container
```

Health check: `GET /healthz` returns `{"status":"ok"}`. Data lives in `HEJMO_DATA_DIR` (default `/data`, SQLite).

On first start the box opens the **setup wizard**: home location (town and state/canton), first parent account with password and one-time codes (TOTP from any authenticator app, for example Aegis or FreeOTP), and the parent PIN for displays.

## API

The REST API lives under `/api/v1`; its OpenAPI description is served at `/api/v1/openapi.yaml` ([source](api/openapi.yaml)). Authenticate with a bearer token (`Authorization: Bearer hejmo_…`) created via `POST /api/v1/tokens`. Tokens are limited by scopes such as `read`, `write`, `lists` or `events:read`.

Calendar endpoints (work package 3):

| Endpoint | Purpose |
|---|---|
| `GET /events?from=…&to=…[&person=…]` | Occurrences with series expanded; with `person` only what that person's view shows |
| `POST /events` | Create an event or a series (`rrule`, e.g. `FREQ=WEEKLY;BYDAY=MO`) |
| `PATCH` / `DELETE /events/{id}?occurrence=…&scope=this\|following\|all` | Change or delete occurrences of a series |
| `GET /holidays?from=…&to=…` | Public holidays of the home state or canton |
| `GET /search?q=…` | Full-text search |
| `GET/POST /groups`, `GET /calendars` | Subgroups and calendars with their audience |

Development is strictly test-driven: every acceptance criterion starts as a failing test.

## Contributing

Contributions are welcome. Please read [CONTRIBUTING.md](CONTRIBUTING.md); every commit needs a DCO sign-off.

## License

[GNU Affero General Public License v3.0](LICENSE)
