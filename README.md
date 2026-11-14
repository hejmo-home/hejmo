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

Health check: `GET /healthz` returns `{"status":"ok"}`.

Development is strictly test-driven: every acceptance criterion starts as a failing test.

## Contributing

Contributions are welcome. Please read [CONTRIBUTING.md](CONTRIBUTING.md); every commit needs a DCO sign-off.

## License

[GNU Affero General Public License v3.0](LICENSE)
