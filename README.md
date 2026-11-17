# Hejmo

**Your household's day in one place, under your own control.**

Hejmo (Esperanto for "home") is a self-hosted, open-source family organizer. A small **box** at home, written in Go with SQLite, collects the household's calendars, tasks, lists and meal plan and shows today on **displays**: wall-mounted E-Ink or normal screens, plus phones. The **family view** has one column per person, so everyone sees at a glance what is on, what is done and what comes tomorrow. All data stays at home.

![The family view on a wall display: date and time, all-day events, one column per person with today's events and task progress, the dinner plan and tomorrow's waste collection](docs/images/family-light.png)

<sub>The family view (start screen of every display), landscape. Design preview with sample data.</sub>

> **Status (16/11/2026): pre-release.** No version has been published yet. Until the first public release, **0.1**, builds from `main` are previews: not for everyday use, and data may need to be set up again. See the [roadmap](#roadmap) and the [status notes](#status-notes).

## What works in `main` today

- **Family view and displays:** one column per person with today's events, task progress, the family strip for all-day events and public holidays, weather and time, and tiles for today's meals and tomorrow's waste collection. Live updates, landscape and portrait, black-and-white mode with marker shapes, themes by sunrise and sunset, a sync notice and a personal view per person. Displays are paired by QR code.
- **Quiet mode and brightness:** a night page, and through the kiosk adapter (FreeKiosk, [setup](docs/freekiosk.md)) brightness by sunrise and sunset, off or dimmed at night, per display. Front light on E-Ink is experimental, see [status notes](#status-notes).
- **Calendar:** events and series with the edit scopes this, this and following, all; multi-day events, public holidays for Germany, Austria and Switzerland, search, subgroups and audiences. The web interface has a week calendar with editing and a page to choose calendars.
- **Calendar sync:** ICS subscriptions and iCloud or any CalDAV server (read only; write-back is open, see below). The box is itself a CalDAV server for your calendar apps, with an iOS profile and one task list per person (VTODO). Nothing syncs through a cloud of ours.
- **Tasks:** simple tasks, routines with ready-made routine templates, subtasks, stars with a star log, and task progress per person on the display.
- **Lists and meals:** shared lists in the web interface and on the display; diet and allergies per person; a shared meal plan with meal plan templates; waste collection reminder; day notes.
- **People and phones:** parents and children, roles, invitations for a second parent, phones paired by QR code (children get their own simple view), parents can end a lost phone's session. German and English interface.
- **Sign-in and HTTPS:** setup wizard, HTTPS with the Box CA or a free public name (deSEC, Let's Encrypt), passkeys, password with one-time codes and recovery codes, parent PIN for displays.
- **Open interfaces:** REST API under `/api/v1` with scoped tokens ([OpenAPI](api/openapi.yaml)), and an MCP server for your own AI assistant, off by default ([guide](docs/mcp.md)). Hejmo has no built-in AI.
- **Operations:** Docker image for x86_64 and arm64, install script for Raspberry Pi OS, release pipeline with signed images, auto-update at night with rollback, security checks and resource measurement in CI.

<table>
<tr>
<td width="50%"><img src="docs/images/family-bw.png" alt="The same family view in black and white for E-Ink displays, with filled and empty circles and squares as person markers"></td>
<td width="50%"><img src="docs/images/web-calendar.png" alt="The week calendar in the web interface with multi-day events and events coloured by person"></td>
</tr>
<tr>
<td><sub>Black and white for E-Ink displays</sub></td>
<td><sub>Week calendar in the web interface</sub></td>
</tr>
</table>

## Roadmap

The plan is in [ADR 0002](docs/adr/0002-goals-and-release-model.md) (release model), [ADR 0018](docs/adr/0018-path-to-0-1.md) (path to 0.1), [ADR 0019](docs/adr/0019-lists-article-library-and-diets.md) (lists) and [ADR 0020](docs/adr/0020-versions-0-2-and-0-3.md) (0.2 and 0.3). All tickets: [milestones](https://github.com/hejmo-home/hejmo/milestones).

### 0.1: first public release

[Milestone 0.1](https://github.com/hejmo-home/hejmo/milestone/1): 33 tickets done, 10 open.

**Done:** everything listed above except the open items below, including the install script and [install guide](docs/install.md), release pipeline, auto-update with rollback, passkeys, routines, stars, subtasks, VTODO export, MCP server and diet and allergies per person.

**Still open:**

- Calendar sync write-back: two-way CalDAV and iCloud ([#17](https://github.com/hejmo-home/hejmo/issues/17)), Google Calendar connection ([#18](https://github.com/hejmo-home/hejmo/issues/18)).
- Home location with coordinates, which weather, sunrise and sunset themes and the quiet mode sun rule need ([#86](https://github.com/hejmo-home/hejmo/issues/86)).
- Lists: article library with own articles and pictures ([#164](https://github.com/hejmo-home/hejmo/issues/164)), suggestions and quantities ([#165](https://github.com/hejmo-home/hejmo/issues/165)), tile view ([#166](https://github.com/hejmo-home/hejmo/issues/166)).
- Recovery codes as PDF download with a required "saved" checkbox ([#176](https://github.com/hejmo-home/hejmo/issues/176)).
- Polish: settings breadcrumbs and side navigation ([#182](https://github.com/hejmo-home/hejmo/issues/182)), a hyphenated name breaking inside the word at 375 px ([#175](https://github.com/hejmo-home/hejmo/issues/175)), a child's phone "Sign out" ([#197](https://github.com/hejmo-home/hejmo/issues/197)).

### 0.2

[Milestone 0.2](https://github.com/hejmo-home/hejmo/milestone/2), contents per ADR 0020:

- CalDAV: sync-collection and `.well-known` discovery ([#54](https://github.com/hejmo-home/hejmo/issues/54), merged)
- Public name: more DNS providers, Cloudflare, INWX and Hetzner ([#57](https://github.com/hejmo-home/hejmo/issues/57))
- API access for displays: open a pairing and read its state ([#61](https://github.com/hejmo-home/hejmo/issues/61), merged)
- Theme chooser for children's phones ([#125](https://github.com/hejmo-home/hejmo/issues/125))
- Sync lists with Apple Reminders ([#167](https://github.com/hejmo-home/hejmo/issues/167))
- Articles always on the list ([#168](https://github.com/hejmo-home/hejmo/issues/168))
- Outlook calendar sync ([#177](https://github.com/hejmo-home/hejmo/issues/177))
- Photo screensaver for displays ([#178](https://github.com/hejmo-home/hejmo/issues/178))
- Display brightness control ([#179](https://github.com/hejmo-home/hejmo/issues/179))
- Choose people and blocks per display, full layouts ([#180](https://github.com/hejmo-home/hejmo/issues/180))

### 0.3

[Milestone 0.3](https://github.com/hejmo-home/hejmo/milestone/3), contents per ADR 0020; the details are decided before building:

- Recipes linked to the meal plan and shopping list ([#193](https://github.com/hejmo-home/hejmo/issues/193))
- Star rewards: catalogue, streaks and badges ([#194](https://github.com/hejmo-home/hejmo/issues/194))
- Box as address book, a CardDAV server ([#195](https://github.com/hejmo-home/hejmo/issues/195))

Later: Immich as a photo source for the screensaver (V3).

## Status notes

- **Raspberry Pi: tested in emulation only.** CI runs the box in a container limited like a Raspberry Pi (1 CPU, 1 GB) on an arm64 machine. Nobody has confirmed it on real hardware yet ([measurements](docs/install/hardware.md)).
- **E-Ink and kiosk functions are experimental, not yet tested on E-Ink devices.** Black and white on real E-Ink, the ghosting reload and the front light in quiet mode are built and tested against a fake kiosk adapter. Everything also works on a normal screen such as an iPad.
- **Install via Docker Compose** on any Linux (`compose.yaml` in this repository), or with the install script on Raspberry Pi OS. The [install guide](docs/install.md) ([Deutsch](docs/install.de.md)) is a draft until 0.1; auto-update installs published versions only, never previews.

## Documentation

- Install guide: [English](docs/install.md), [Deutsch](docs/install.de.md) (draft until 0.1); [hardware and measurements](docs/install/hardware.md)
- [AI assistants (MCP)](docs/mcp.md) and [threat model](docs/security/threat-model.md)
- [Specification](docs/spec.md): features, data model, acceptance criteria, design
- [Architecture decision records](docs/adr/): decisions that refine or override the specification
- [Design system](DESIGN.md) and [glossary](GLOSSARY.md)

## Development

Requirements: Go (see `go.mod`), Docker.

```bash
go test ./...          # run all tests
go run ./cmd/hejmo     # start the box on port 8080
docker compose up      # run the container
```

The box answers at [localhost:8080](http://localhost:8080) (HTTP) when run this way. Health check: `GET /healthz` returns `{"status":"ok"}`. Data lives in `HEJMO_DATA_DIR` (default `/data`, SQLite).

**HTTPS** (ADR 0014): the box serves everything over HTTPS; HTTP only redirects. The setup wizard starts over HTTP, creates the box's own certificate authority (name-constrained to the box) and only then asks for a password. Download the iOS profile or certificate at `/ca`. Ports are configurable in Compose through `HEJMO_HTTP_PORT` (default 80) and `HEJMO_HTTPS_PORT` (default 443); keys live in `HEJMO_DATA_DIR/tls`. The wizard can instead set up a **public name**: a free deSEC `dedyn.io` name, or your own domain at Cloudflare, deSEC, Hetzner or INWX, pointing to the box's LAN address, with a publicly trusted certificate (Let's Encrypt, or Actalis) obtained and renewed by the box through DNS-01, so phones need no CA installed. The DNS credentials (a token, or for INWX user name, password and the two-factor secret if you use one) are stored encrypted; its key is `HEJMO_DATA_DIR/secret.key`, or the file named by `HEJMO_SECRET_KEY_FILE` such as a Docker secret (back it up with the database; if it is lost, the box keeps serving the stored certificate over HTTPS and warns you until it expires; restore the key file from your backup). If your router blocks the name (FRITZ!Box DNS rebind protection), the wizard shows how to allow it, or lets you use the box's own certificate authority instead. Without Docker, `go run ./cmd/hejmo` listens on HTTP 8080 and HTTPS 8443 (`HEJMO_ADDR`, `HEJMO_HTTPS_ADDR`, `HEJMO_PUBLIC_HTTPS_PORT`). `HEJMO_PUBLIC_HTTPS_PORT` is the port clients reach HTTPS on, used in redirects and links; if unset it is the port of `HEJMO_HTTPS_ADDR`. Set it only when a port mapping differs (Compose sets it from `HEJMO_HTTPS_PORT`).

Calendar passwords (iCloud app-specific passwords and other CalDAV logins) are stored encrypted with AES-256-GCM. The key is generated on first start in `/data/secret.key` (mode 0600); `HEJMO_SECRET_KEY_FILE` points to another file, such as a Docker secret. This protects a leaked database file or backup on its own. It does **not** protect a leaked data volume, because the key sits next to the database unless you move it. Keep the key with your backup if you want to restore connections; without it, connect the calendars again.

On first start the box opens the **setup wizard**: HTTPS, home location (town and state/canton), first parent account with password and one-time codes (TOTP from any authenticator app, for example Aegis or FreeOTP), and the parent PIN for displays. The box then shows 10 recovery codes, each valid once in place of a one-time code.

**Lost your one-time codes?** Use a recovery code, or ask another parent to reset them under *My account* in the settings. As the last way back, run this on the box; it resets the account's one-time codes and signs it out of browsers, and the next sign-in sets up new ones:

```bash
docker compose exec hejmo /hejmo reset-otp <username>
```

The reset is logged as `one-time codes reset` in the box's log (`docker compose logs hejmo`).

## API

The REST API lives under `/api/v1`; its OpenAPI description is served at `/api/v1/openapi.yaml` ([source](api/openapi.yaml)). Authenticate with a bearer token (`Authorization: Bearer hejmo_…`) created via `POST /api/v1/tokens`. Tokens are limited by scopes such as `read`, `write`, `lists` or `events:read`. `read` and `write` exclude health data (diet and allergies); that needs the explicit scope `health` or `health:read`.

Calendar endpoints:

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
