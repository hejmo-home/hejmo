# 0012. Calendar sync

- Status: Accepted, amended by [0015](0015-display-web-interface-and-everyday.md)
- Date: 14/11/2026
- Amends: [0003](0003-v1-scope-and-work-packages.md) (work package 4 and the V1.x list)

## Context

Work package 4 connects the box to outside calendars (ADR 0007). The founding family uses Google Calendar and iCloud. ADR 0003 left the box as a CalDAV server for V1.x, which would have meant building CalDAV twice: once for the family calendar coupled to outside services, once for the server. The calendar model (ADR 0011) maps 1:1 to iCalendar.

## Decision

### Direction and conflicts

- **ICS** calendars are always read-only calendars. **CalDAV** and **Google** calendars are two-way by default; a parent can make each one read-only.
- On a two-way synced calendar, creating, changing and deleting an event on the box is written back to its origin. Deletions outside remove the event from the box.
- **Sync conflict:** last change wins (`LAST-MODIFIED` outside against `updated_at` on the box, detected through `ETag`/`If-Match`). The losing version stays in the **sync log** for 30 days. No interactive resolution in V1.0.
- Removing a calendar connection removes its synced calendars' events from the box. Nothing is deleted outside.

### What the box keeps

- Per synced event the box stores the **raw iCalendar data, ETag and remote URL** (new migration `0003_sync.sql`). On write-back it changes only the fields it models and keeps everything else (attendees, `VALARM`, conference links).
- The **audience** goes out as `X-HEJMO-AUDIENCE` plus one readable line at the end of the description ("For: Mia, Sebastian", localised). The title stays unchanged. The line is stripped again on import.
- Import window: the **last 12 months and the whole future**. A series is imported whole if any occurrence falls in the window.
- Windows time zone names are mapped to IANA zones through the CLDR table (embedded). Floating times and unknown zones use the box's time zone; unknown zones are noted in the sync log.

### Connections and timing

- A **calendar connection** holds one set of credentials. For CalDAV (iCloud, Nextcloud and others) the box discovers the calendars and a parent ticks the ones to sync. A single calendar URL or ICS URL can also be added by hand.
- Each synced calendar gets its own colour and audience.
- Intervals: CalDAV and Google every **5 minutes**, ICS every **60 minutes**, both configurable per calendar. Changes on the box are pushed immediately. Parents can trigger "sync now". Delta sync through `sync-collection` (RFC 6578) where offered, else CTag/ETag. Failures back off exponentially.
- On failure the box keeps the last good data and logs the error. After **24 hours** without success, parents see a notice in the web interface and displays show an info symbol in the header pointing to the web interface (package 5); it clears itself after the next successful sync.

### Credentials

- Credentials (app-specific passwords, OAuth refresh tokens, DNS API tokens) are encrypted with **AES-256-GCM** (Go standard library).
- The key is generated on first start in the data volume (`/data/secret.key`, mode 0600). An environment variable can point to another file, such as a Docker secret.
- This protects a leaked database file or backup on its own, not a leaked data volume. That limit is documented.

### Box as CalDAV server (moved into V1.0)

- The box serves its calendars over CalDAV, **`VEVENT` only**. Tasks (`VTODO`) follow with tasks; contacts (CardDAV) stay in V2.
- The family calendar is always served. Every other calendar has a "share through the box" switch, on by default for ICS calendars and off for CalDAV and Google calendars (phones already have those).
- Phones sign in with the account's username and a per-device **API token** with token scope `calendars` as password. Each token can be revoked on its own. Parents see all events; children have no account.
- Served only over HTTPS (ADR 0014).

### Libraries (make or buy)

- `github.com/emersion/go-webdav` (MIT) for the CalDAV client and server, `github.com/emersion/go-ical` (MIT) for iCalendar. Both in the binary.
- Rejected: Radicale as a side container (a second sync between box and Radicale), and building CalDAV ourselves.

### Tests

- CalDAV against a real **Radicale** container in CI (x86_64; arm64 if Radicale runs there).
- Google against a fake HTTP server only. iCloud and Google are checked once by hand before merge, with results in the PR.

## Work package 4 tickets

One ticket per step, one pull request per ticket, in this order:

1. Sync foundation: calendar connections, credential encryption, sync log, scheduler
2. ICS subscriptions
3. CalDAV and iCloud (two-way)
4. Google (ADR 0013)
5. HTTPS and certificates (ADR 0014)
6. Box as CalDAV server

## Consequences

- "Box as CalDAV server" leaves the V1.x list in ADR 0003; VTODO and CardDAV serving stay where they were.
- Package 4 grows; the display (package 5) moves back accordingly.
- The `extra` column (ADR 0011) is not used for sync data; raw iCalendar lives in its own table.
