# 0017. Web interface usability

- Status: Accepted, amended by [0022](0022-home-location.md)
- Date: 14/11/2026
- Amends: [0006](0006-authentication.md) (recovery when the one-time code device is lost), [0010](0010-households-memberships-and-credentials.md) (recovery codes as a credential), [0012](0012-calendar-sync.md) (choosing calendars with a switch, also for synced calendars, and switching one off; calendar colour; sync log wording; event count before choosing a calendar; iOS profile for CalDAV clients), [0015](0015-display-web-interface-and-everyday.md) (navigation; one display question instead of name and room; breadcrumb trail instead of back links, ticket 182)

## Context

The product owner tested the box with a real iCloud account on 14/11/2026. The management pages of the web interface were hard to use: a small checkbox decided whether a calendar is shown, all fields looked active, people were chosen with checkboxes that looked like the selection checkbox, synced calendars could not be switched off where they were chosen, "Sync now" gave no feedback, and she picked a calendar whose only event was from 2016 without noticing. A UX review proposed shared patterns and raised questions Q34–Q40, which she decided. This ADR is the record of those decisions.

## Decision

### Shared patterns for all management pages

- **Switch for "is it on":** whether something is used (a calendar on the box, sharing through the box, the tomorrow block) is a switch with its state in words. When off, the card shows no fields; when on, its fields appear.
- **Person chips:** people and subgroups are chosen as chips with the person marker and colour. A chip **"Whole family"** is selected by default and replaces the hint "leave empty for the whole family"; it stores no audience, so people added later are included.
- **One primary action per screen.** Overview pages show calm rows that open a detail page; removing, disconnecting, revoking and deleting happen only on the detail page, after a confirmation that says what will happen.
- **Feedback:** every saved change shows a success line. Form errors appear at the field and keep what was typed (except passwords).
- **Progressive disclosure:** fields with a good default sit under "More options".
- **Wording:** German texts address the user as **"du"**; "eure" only where the whole family is meant.

### Calendars

- **Choose calendars** is the single place per calendar connection: every found calendar has the same switch, also calendars already synced. Switching a synced calendar off asks for confirmation ("its events disappear from the box; nothing is deleted at the source"). Name, colour and audience stay editable.
- **Event count before choosing:** each calendar shows how many events it has in the import window (past 12 months plus all future; a series counts once), loaded after the page has rendered so a slow server cannot block it, with a timeout and an "unknown" state if the server does not answer, and "No events in the last 12 months or later" when empty. The calendars page shows the same count per synced calendar.
- **Calendar colour** (amends ADR 0012) is used only in the web interface's calendar views; displays colour by person. The box picks it automatically from a fixed set; it can be changed under "More options". Adding a calendar no longer asks for it.
- **Sync feedback and sync log** (amends ADR 0012): the sync log stays a record of problems only and is shown as "Sync problems". Successful runs show as status per calendar (last successful sync, event count, changes of the last run) on the calendars page and the calendar's detail page; the sync log page says so with a link to the calendars.
- **Event count query (ticket 5.17):** the box asks the server with one `calendar-query` with a time-range from 12 months ago, open-ended, and counts the objects it answers; the server decides about series, so a series counts once. Nothing is downloaded. The quirk corpus covers this query like listing and download.
- **Seconds and run line (product owner feedback and retro, 14/11/2026):** times of the last successful sync and of sync problems show seconds (hh:mm:ss; 12-hour: h:mm:ss a), and a relative hint may stand next to them, never instead. Every sync run writes one INFO log line without content: calendar id, listed, fetched, new, changed, removed, skipped, duration, result. Titles, descriptions and addresses of events never appear in logs.

### Navigation (amends ADR 0015 and the approved frame)

- The navigation holds everyday screens only: Home, Calendar, Tasks, Lists, Meals. Everything for setting up sits under **Settings**, a hub page: calendar connections, CalDAV clients (device tokens, share through the box), displays, phones, people, household (incl. HTTPS and API tokens for other software) and my account (password, one-time codes, recovery codes).

### Displays (amends ADR 0015)

- A display is described by one question, **"Where does it hang?"** (e.g. "Kitchen"), which is its name. A separate room field comes only when a feature needs it.

### iOS profile for CalDAV clients (amends ADR 0012)

- In V1.0 the box offers an **iOS configuration profile** that sets up a CalDAV client on an iPhone or iPad in one step. A signed-in parent creates it for **their own account only**. It contains the box address, their username and a **new device token created for this profile** with the `calendars` token scope, named after the device the parent enters. The box creates the profile when asked, sends it once over HTTPS as the answer to that request, and keeps no copy; to download it again you make a new profile, which creates a new token. The page recommends opening it directly on the iPhone or iPad, because the file holds the token in plain text. The token is listed and revocable like any other device token. Removing the profile on the device does not revoke the token. If the box uses its own CA, the flow first points to the CA profile (ADR 0014).

### Recovery of one-time codes (amends ADR 0006 and 0010)

- When one-time codes are set up (also in the setup wizard), the box shows **10 recovery codes**, each valid once, to print or keep in a password manager. A recovery code replaces the one-time code at sign-in, never the password. Recovery codes are hashed with Argon2id like passwords (ADR 0010) and are shown only once; a parent can create a new set, which voids the old one.
- Another parent can reset a parent's one-time codes in the web interface, after confirming with their own password and one-time code. A parent cannot reset their own codes this way.
- As the last way back, a command run inside the box's container (`hejmo reset-otp <username>`) resets an account's one-time codes; access to the box's shell counts as proof of ownership. It is not reachable over the network.
- A reset removes the account's one-time code secret and its recovery codes and ends its browser sessions. At the next sign-in the password is followed straight away by setting up new one-time codes (as in the setup wizard); nothing else is reachable until that is confirmed. Device sessions and device tokens stay; the reset confirmation reminds the parent to revoke them on the phones and CalDAV clients pages if the lost device was paired.

### People

- The web interface gets a page to manage people (add, edit colour and marker, order); it was missing.
- Ticket 5.21 builds it. **Colour** of a person is one of the eight palette colours of "Nordic Calm" (slot 1 to 8, DESIGN.md), **marker** one of the eight black-and-white shapes. The product owner decided at the preview review on 15/11/2026 that households of up to eight people (two parents and six children) get distinct colours and distinct shapes; the palette and shapes grew from six to eight for that (slots 7 and 8, cross and star). The displays, chips and markers take the slot from the person's colour, so changing the colour changes it everywhere, including the dark palette. No two people may have the same colour **and** the same marker; the box refuses the second one (web form and API) and gives new people the first free combination. People are ordered with up and down buttons (also through `PUT /people/order`). A subgroup can be added, renamed, given other members and deleted; one that still decides who sees a calendar or an event is not deleted, because its audience would silently become the whole household. Adding an adult here adds the person only; their account (username, password) is a separate step.

### Overviews and detail pages (ticket 111, product owner 15/11/2026)

- Displays, device tokens, calendar connections, subscribed calendars and phones are listed as **calm rows** that open a **detail page**; the overview has no switch, form, "Sync now" or remove, only one primary action (add or pair). A detail page has a breadcrumb trail through its overview (not only a link back; see "Breadcrumbs" below), the name as heading, a facts card, one form with one primary action and then the closed red "danger" box. Its confirmation names the thing on the red button, says what disappears (a connection lists its calendars by name) and offers "No, change nothing".
- **State in words:** last contact, last use, last sync with seconds. A display that has not been in contact for **15 minutes** gets a warning ("Check power and Wi-Fi"). A token never used, a phone session that ran out, a refused login and a sync problem are flags with what to do.
- **Display detail** holds name and type (renaming and retyping work through the API as well), the tomorrow block, light/dark/auto (for every type: a dark theme on a black-and-white E-Ink display takes the dark palette, ADR 0015), quiet mode (times only while it is on) and, under More options, sun offsets and the kiosk app.
- **New device token:** the answer to creating it is its own page that shows the token once (not stored, not redirected) with a copy button, the server address, user name and the primary link to the token's detail page. Reloading that page asks the browser to send the creation again and makes a **second** token; the page says "Fertig" so that nobody needs to reload, and the surplus token is revoked on its detail page.
- **New password for a connection** (also `PUT /calendar-connections/{id}/credentials`): checked against the server like a new connection, stored encrypted, the connection's calendars sync again at once, one after another within 30 seconds (what is left waits for its next turn). Without it a revoked app-specific password forced disconnecting and reconnecting, which loses names, colours and audiences.
- **Subscribed calendar detail** also holds the switch for the waste collection calendar (ticket 6.7) and its colour; the list is not grouped by person any more, each row names its audience. The calendars of a connection have no page of their own, so the waste switch for the read-only calendars of a connection sits on the connection page, in a section of its own below the choice of calendars (also `waste_collection` on `PATCH /calendars/{id}`).
- **Success lines name the thing** after removing it ("“Küche” unpaired."); the name travels in the one-time flash cookie as base64 text and is shown escaped. A success line a page shows itself is never replaced by a waiting one.

### Breadcrumbs (ticket 182, product owner 16/11/2026, Q93; preview `docs/design/preview-182.html`)

- **Every page below Settings starts with a breadcrumb trail** that replaces the back link: `Settings › <hub entry> › … › <this page>`, for example `Settings › People › Papa › Invitation for Papa`. Every part except the last is a link; the last is the page's own heading in plain text with `aria-current="page"`. The Settings hub is the top and has no trail. The trail is a `nav` with `aria-label` "Breadcrumb" (German "Seitenpfad") holding an `ol`.
- **The side navigation stays on every sub-page** with Settings filled: `aria-current="page"` on the hub, `aria-current="true"` on every page below it.
- **Words match what you tapped:** the trail word of an overview is its hub entry, and the overview's heading says the same. The page "Calendars" is therefore called **"Calendar connections"** (German "Kalender-Verbindungen"), so it is not mixed up with the screen Calendar. "Choose calendars" is a section of the connection page, not a part of the trail.
- **Names are never cut:** parts wrap between words, only words of 12 or more letters are hyphenated, a single token longer than the line (an e-mail address) breaks at the line end as a last resort. Hyphenation depends on browser support for `hyphenate-limit-chars` (Chrome and Edge); Safari and Firefox never hyphenate the trail, so there a word of 12 or more letters wider than the line breaks at the line end without a hyphen. Below 600 px the last part is only read out, because the heading right below says it.
- **Order:** trail, then the success or error line, then the heading. After a field error, trail and heading keep the saved name.
- **Everyday screens (Q93):** a page one level below a navigation screen (event, task, note, list, templates overview) keeps its single back link. From two levels deep the same trail is shown, for example `Meals › Meal plan templates › Weekend` and `Tasks › Routine templates › Morning routine › <step>`.

### Setup of the one-time code after an invitation (ticket 145)

- A parent who accepted an invitation and stopped before confirming the one-time code signs in with the password only and is led into code setup, as after a reset; this holds for any account that has not confirmed its code on a configured box, not only after a reset.

## Consequences

- New tickets in package 5: management pages redesign (calendars and choose calendars with switch, chips, event count, feedback), settings hub and navigation, display settings question, tasks page patterns, iOS profile for CalDAV clients, recovery of one-time codes, people page.
- The sync engine stores per run the event count in the import window and the number of new, changed and removed events. Note (#17, write-back): the event count shown for a synced calendar (`sync.event_count`, calendar page, flash text) is the number of events the calendar holds now, events written on the box included and not limited to the import window; the count before choosing a calendar still uses the import window.
- Each of these tickets shows a preview before merge where it changes layouts (CLAUDE.md design rule).
