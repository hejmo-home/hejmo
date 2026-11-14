# 0017. Web interface usability

- Status: Accepted
- Date: 14/11/2026
- Amends: [0006](0006-authentication.md) (recovery when the one-time code device is lost), [0012](0012-calendar-sync.md) (calendar colour; sync log wording; event count before choosing a calendar; iOS profile for CalDAV clients), [0015](0015-display-web-interface-and-everyday.md) (navigation; one display question instead of name and room)

## Context

The product owner tested the box with a real iCloud account on 14/11/2026. The management pages of the web interface were hard to use: a small checkbox decided whether a calendar is shown, all fields looked active, people were chosen with checkboxes that looked like the selection checkbox, synced calendars could not be switched off where they were chosen, "Sync now" gave no feedback, and she picked a calendar whose only event was from 2016 without noticing. A UX review (`previews/ux-settings-review.md` outside the repo, summarised here) proposed shared patterns and raised questions Q34–Q40, which she decided.

## Decision

### Shared patterns for all management pages

- **Switch for "is it on":** whether something is used (a calendar on the box, sharing through the box, the tomorrow block) is a switch with its state in words. When off, the card shows no fields; when on, its fields appear.
- **Person chips:** people and subgroups are chosen as chips with the person marker and colour. A chip **"Whole family"** is selected by default and replaces the hint "leave empty for the whole family".
- **One primary action per screen.** Lists are calm rows that open a detail page; removing, disconnecting, revoking and deleting happen only on the detail page, after a confirmation that says what will happen.
- **Feedback:** every saved change shows a success line. Form errors appear at the field and keep what was typed (except passwords).
- **Progressive disclosure:** fields with a good default sit under "More options".
- **Wording:** German texts address the user as **"du"**; "eure" only where the whole family is meant.

### Calendars

- **Choose calendars** is the single place per calendar connection: every found calendar has the same switch, also calendars already synced. Switching a synced calendar off asks for confirmation ("its events disappear from the box; nothing is deleted at the source"). Name, colour and audience stay editable.
- **Event count before choosing:** each calendar shows how many events it has in the import window (past 12 months plus all future; a series counts once), fetched when the page opens, with an "unknown" state if the server does not answer, and "No events in the last 12 months or later" when empty. The calendars page shows the same count per synced calendar.
- **Calendar colour** (amends ADR 0012) is used only in the web interface's calendar views; displays colour by person. The box picks it automatically from a fixed set; it can be changed under "More options". Adding a calendar no longer asks for it.
- **Sync feedback and sync log** (amends ADR 0012): the sync log stays a record of problems only and is labelled "Sync problems". Successful runs show as status per calendar (last successful sync, event count, changes of the last run) on the calendars page and the calendar's detail page; the sync log page says so with a link to the calendars.

### Navigation (amends ADR 0015 and the approved frame)

- The navigation holds everyday screens only: Home, Calendar, Tasks, Lists, Meals. Everything for setting up sits under **Settings**, a hub page: calendar sources, calendar apps (CalDAV), displays, phones, family and household.

### Displays (amends ADR 0015)

- A display is described by one question, **"Where does it hang?"** (e.g. "Kitchen"), which is its name. A separate room field comes only when a feature needs it.

### iOS profile for calendar apps (amends ADR 0012)

- In V1.0 the box offers an **iOS configuration profile** that sets up the CalDAV account on an iPhone or iPad in one step. It contains the box address, the user name and a **new device token created for this profile**, with the `calendars` token scope; it is downloaded once, over HTTPS, by a signed-in parent, and the token is listed and revocable like any other. If the box runs on its own CA, the profile flow points to the CA profile first (ADR 0014).

### Recovery of one-time codes (amends ADR 0006)

- When one-time codes are set up, the box shows **10 recovery codes**, each valid once, to print or keep in a password manager.
- Another parent can reset a parent's one-time codes in the web interface.
- As the last way back, a command on the box resets an account's one-time codes; access to the box counts as proof of ownership.

### People

- The web interface gets a page to manage people (add, edit colour and marker, order); it was missing.

## Consequences

- New tickets in package 5: management pages redesign (calendars and choose calendars with switch, chips, event count, feedback), settings hub and navigation, display settings question, tasks page patterns, iOS CalDAV profile, recovery of one-time codes, people page.
- The sync engine stores per run the event count in the import window and the number of new, changed and removed events.
- Each of these tickets shows a preview before merge where it changes layouts (CLAUDE.md design rule).
