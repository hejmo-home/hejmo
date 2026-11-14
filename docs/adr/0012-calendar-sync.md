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
- **Delta sync decision (Q24, 14/11/2026, ticket 16):** the first CalDAV version lists ETags (depth-1 PROPFIND) each interval and downloads only new or changed objects, because go-webdav v0.7.0 has no `sync-collection` and no CTag. The product owner accepted this; `sync-collection` and a CTag short-circuit are follow-up #54.
- **Hosts of one server (Q25, ticket 16):** CalDAV servers such as iCloud answer from several hosts (`caldav.icloud.com` names `pNN-caldav.icloud.com` as the calendar home). The client follows redirects and principal / calendar-home hrefs to other hosts only within the same registrable domain as the configured server (public suffix list), over the same scheme. Any other host is refused with a clear error in the sync log, and credentials are never sent to it.
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

#### Notes added with the implementation (#19)

- **Addresses:** `/dav/{person id}/calendars/{calendar id}/{event id}.ics`, with `/.well-known/caldav` redirecting to `/dav/`. The person's id, not the username, is in the path, so any username is safe there.
- **One resource per series:** an event, or a series with its changed occurrences, is one `.ics` resource. The event's id is its name and `UID`; a name the phone chooses for a new event (letters, digits and `. _ @ = + -`, at most 128 characters) becomes its id.
- **ETag** is a hash of the rendered iCalendar, so it changes exactly when the content does. `If-Match`/`If-None-Match` on `PUT` and `DELETE` answer 412 when they do not hold; CalDAV writes are serialised among themselves (an edit through the REST API at the same moment is not detected).
- **A phone's save replaces the series.** Changed occurrences it leaves out are deleted. The audience (who an event is for) is not shown to phones and is kept from the stored event; new events are for the whole family.
- **Time zones** go out as `VTIMEZONE` definitions generated from the box's zone data (to 2037), so clients need no zone database. Events a phone writes are never cut off by age.
- **Read-only and unshared calendars:** subscribed ICS calendars can be read but not written (403); unshared calendars do not exist for phones (404).
- **Names:** the `UID` a phone sends is replaced by the resource name it chose, which is the event's id.
- **Credentials:** HTTP Basic, username plus device token with a token scope `calendars` (`calendars:read` syncs, `calendars:write` writes, deleting needs `calendars:delete` or the whole `calendars` scope); plain `read` and `write` tokens do not open CalDAV. Parents only: a token held by a child is refused. Browser sessions are no credential. There is no per-user lockout: tokens are 256-bit random values.

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
