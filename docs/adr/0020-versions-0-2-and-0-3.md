# 0020. Versions 0.2 and 0.3

- Status: Accepted
- Date: 16/11/2026
- Amends: [0005](0005-display-client-and-kiosk-adapter.md) (only open-source kiosk apps), [0007](0007-calendar-sync-in-v1.md) (how Outlook is connected), [0012](0012-calendar-sync.md) (CalDAV sync-collection and discovery; the CardDAV address book moves from V2 to 0.3), [0015](0015-display-web-interface-and-everyday.md) (full display layouts with blocks), [0018](0018-path-to-0-1.md) (contents of 0.2; 0.3 added; box as CardDAV server brought forward from V2), [0019](0019-lists-article-library-and-diets.md) (details of Reminders sync and "always on the list")

## Context

ADR 0018 named what comes after 0.1 only as "0.2 and later". On 15/11/2026 and 16/11/2026 the product owner set the contents of 0.2 and 0.3 and decided the open questions on each ticket (grilling questions Q24 to Q45 and the decisions on #54, #57, #61, #125, #167, #168 and #177 to #180). This ADR records those decisions. Where a ticket still has open points, it says so.

## Decision

### Versions

- **0.2** holds ten tickets:
  - #54 CalDAV: sync-collection and `.well-known` discovery
  - #57 public name: more DNS providers
  - #61 API access for displays: open a pairing and read its state
  - #125 theme chooser for children's phones
  - #167 sync lists with Apple Reminders
  - #168 articles always on the list
  - #177 Outlook calendar sync
  - #178 photo screensaver for displays
  - #179 display brightness control
  - #180 choose people per display (full layouts)
- **0.3** holds three tickets (Q31, 16/11/2026):
  - #193 recipes linked to the meal plan and the shopping list
  - #194 star rewards: catalogue, streaks and badges
  - #195 box as address book (CardDAV server), brought forward from V2
- **V3:** Immich as a photo source for the screensaver.
- The grilling rounds on the details of 0.3 (where recipes come from, how rewards are redeemed, what a contact is) take place before building and are not decided here.

### Outlook calendar sync (#177)

- **Accounts:** personal Microsoft accounts first. Work and school accounts are supported with a note: strict tenants need admin consent, and a family's own app registration cannot be publisher-verified.
- **Sign-in:** by **device code** on the phone (public client, no secret). The family registers the app themselves, as with Google (ADR 0013).
- **Interface:** Microsoft Graph v1.0 through `net/http` and `golang.org/x/oauth2` with hand-written types, not `msgraph-sdk-go` (48 modules, about 6 minutes cold build).
- **Attendees:** events with attendees are **read-only** on the box, since editing would send updates to every attendee. All other events are two-way.
- **Reconnect notice:** when the sign-in expires (90 days without use, password change, revoked), the calendars page and the parents' home screen show "Reconnect Outlook" with one click to sign in again.

### Photo screensaver (#178)

- The screensaver is a **page the box serves**, so it runs on every display, including a plain browser. It starts after a configurable idle time and never in quiet hours.
- **Sources in 0.2:** upload to the box and a folder on the box. iPhone HEIC photos are converted in the browser on upload. **Immich albums come in V3.** No iCloud shared albums (no official interface).
- **Privacy:** the box keeps only a copy resized to display size, with location data removed. Per photo a switch "don't show on displays". No face recognition.

### Brightness (#179)

- Brightness **follows the sun at the home location** (#86), dimmed or screen off in quiet hours, switchable per display.
- It goes through the kiosk adapter (FreeKiosk REST: `/api/brightness`, `/api/screen/off`); a plain browser shows a black page at night instead. The E-Ink front light stays experimental until tested on a device (ADR 0018).
- **Only open-source kiosk apps** (FreeKiosk). There is no adapter for Fully Kiosk or any other proprietary kiosk app.

### Display layouts (#180)

- Per display a parent chooses the people, their order and **which blocks** it shows (calendar, tasks, meals, weather, lists and so on). Full layouts, not only people and order, are in 0.2.
- The layout model is grilled before building; its details are not decided here.

### Public name: DNS providers (#57)

- Besides deSEC, the own-domain way offers **Cloudflare, INWX and Hetzner**, each after a make-or-buy check per libdns provider.
- **Cloudflare:** `libdns/cloudflare` (MIT, no extra dependencies); a zone-scoped token (Zone:Read and Zone.DNS:Write, or the dual-token setup).
- **INWX:** `libdns/inwx` (MIT, adds `pquerna/otp`); username, password and an optional 2FA shared secret. The wizard explains why the secret is needed and that a scoped token does not exist. Tests cover set, delete and cleanup of stale ACME TXT records.
- **Hetzner:** no `libdns/hetzner/v2` (+4.6 MB, about 45 modules). Hejmo writes its **own thin libdns provider** over the Hetzner Cloud DNS REST API with `net/http` only, tested against a fake server. Never the deprecated DNS Console API.
- The box never overwrites a name that already points to a public address.

### CalDAV (#54)

- Both are built: `sync-collection` (RFC 6578) with a getctag short-circuit, falling back to the ETag listing for servers that do not offer it, and `.well-known/caldav` discovery with redirect handling for PROPFIND.

### Lists in Apple Reminders (#167)

- The work starts after the lists package (#163 to #166) is in `main`.
- The quantity is in the reminder title ("Flour · 1.5 kg"). Ticking in Reminders ticks in Hejmo and back.
- Unlike tasks (ADR 0018), **creating and deleting entries from Reminders is allowed**, since a list entry needs no person. A new entry is matched to an article by name, otherwise it stays free text.
- **Only parents' devices** get lists in Reminders in 0.2, through their own profile. Old device tokens are not widened silently; a new profile is needed.
- A quantity typed in Reminders is read only if the text ends in a number plus an active unit; a bare number uses the article's first default unit. A duplicate adds up by the quantity rule of ADR 0019.
- **Open (Q42):** whether notes written in Reminders are kept. Until decided, notes, dates and flags set in Reminders are not kept.

### Articles always on the list (#168)

- Per article a rhythm like the meal plan templates (every week up to every 52nd week), **one weekday** and a target list.
- Hejmo adds the article at **03:00** on that day; the time is a **configurable household setting**. If the chosen day is today when saving, the rule starts at the next time.
- If the article is already open on the list (for example from a recipe with a set quantity), Hejmo **adds the rule's quantity** and shows a **note behind the entry** that part of it is already planned (for example "500 g for Pancakes + 1 kg always"), because a recipe's quantity is not free to use.
- **Holiday mode** replaces "skip once": household-wide, with from and to dates, it pauses all such rules; the list and the settings say so while it is on.
- The rule keeps running when the article is hidden by hand or for an allergy; the entry carries the allergy warning (ADR 0019).

### Smaller 0.2 tickets

- **#125 theme chooser and #61 display pairing:** moved to 0.2 on 15/11/2026; the design of #125 is decided on its ticket.

## Consequences

- The ten 0.2 tickets keep their numbers; the 0.3 tickets #193 to #195 need a grilling round each before design.
- Moving the CardDAV address book to 0.3 brings people and contacts into the data model earlier than ADR 0018 planned.
- Graph, two more DNS providers, Immich later and FreeKiosk add outbound connections and dependencies; the threat model gets an entry for each, and the make-or-buy notes of #57 and #177 stay on the tickets.
- The 0.1 plan of ADR 0018 does not change.
