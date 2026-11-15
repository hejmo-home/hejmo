# Hejmo

**Your family's day in one place, under your own control.**

Hejmo (Esperanto for "home") is a self-hosted, open-source family organizer. It runs locally on a small box such as a Raspberry Pi and shows the family's day on calm, low-power E-Ink displays on the wall. Calendar, tasks, lists, meal plan and shopping live together; all data stays at home.

- No subscription, no telemetry, no mandatory cloud
- Open standards (CalDAV, iCalendar), connects to Google and iCloud
- Open REST API; open to your own AI assistant, without built-in AI
- Licensed under AGPL-3.0, developed in Europe

> **Status:** early development (pre-release 0.x). Not ready for everyday use yet.

## Documentation

- Install guide: [English](docs/install.md), [Deutsch](docs/install.de.md) (Raspberry Pi OS: one command)
- [Specification](docs/spec.md): features, data model, acceptance criteria, design
- [Build handover](docs/handover.md): original work plan and guardrails
- [Architecture decision records](docs/adr/): decisions that refine or override the specification

## Roadmap

| Release | Content |
|---|---|
| **0.1 (first public version)** | Box and setup wizard, people and roles, calendar with series and DACH holidays, sync (ICS, iCloud, CalDAV, Google), box as CalDAV server, displays and phones, lists, meal plan, tasks with stars, routines and subtasks, VTODO export, passkeys, MCP server, install guide, auto-update with rollback |
| **0.2 and later** | Outlook, photo screensaver, brightness control, free display layouts |
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

**HTTPS** (ADR 0014): the box serves everything over HTTPS; HTTP only redirects. The setup wizard starts over HTTP, creates the box's own certificate authority (name-constrained to the box) and only then asks for a password. Download the iOS profile or certificate at `/ca`. Ports are configurable in Compose through `HEJMO_HTTP_PORT` (default 80) and `HEJMO_HTTPS_PORT` (default 443); keys live in `HEJMO_DATA_DIR/tls`. The wizard can instead set up a **public name**: a free deSEC `dedyn.io` name (or an own domain at deSEC) pointing to the box's LAN address, with a publicly trusted certificate (Let's Encrypt, or Actalis) obtained and renewed by the box through DNS-01, so phones need no CA installed. The DNS token is stored encrypted; its key is `HEJMO_DATA_DIR/secret.key`, or the file named by `HEJMO_SECRET_KEY_FILE` such as a Docker secret (back it up with the database; if it is lost, the box keeps serving the stored certificate over HTTPS and warns you until it expires; restore the key file from your backup). If your router blocks the name (FRITZ!Box DNS rebind protection), the wizard shows how to allow it, or lets you use the box's own certificate authority instead. Without Docker, `go run ./cmd/hejmo` listens on HTTP 8080 and HTTPS 8443 (`HEJMO_ADDR`, `HEJMO_HTTPS_ADDR`, `HEJMO_PUBLIC_HTTPS_PORT`).

Calendar passwords (iCloud app-specific passwords and other CalDAV logins) are stored encrypted with AES-256-GCM. The key is generated on first start in `/data/secret.key` (mode 0600); `HEJMO_SECRET_KEY_FILE` points to another file, such as a Docker secret. This protects a leaked database file or backup on its own. It does **not** protect a leaked data volume, because the key sits next to the database unless you move it. Keep the key with your backup if you want to restore connections; without it, connect the calendars again.

On first start the box opens the **setup wizard**: HTTPS, home location (town and state/canton), first parent account with password and one-time codes (TOTP from any authenticator app, for example Aegis or FreeOTP), and the parent PIN for displays.

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
