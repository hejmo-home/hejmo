# 0015. Display, web interface and everyday features

- Status: Accepted
- Date: 14/11/2026
- Amends: [0003](0003-v1-scope-and-work-packages.md) (adds the phone web app to package 5), [0005](0005-display-client-and-kiosk-adapter.md) (sleep mode becomes quiet mode with a night page), [0006](0006-authentication.md) (children's phones without an account), [0008](0008-design-takes-precedence-over-version-plan.md) (footer tiles), [0010](0010-households-memberships-and-credentials.md) (child rights on phones)

## Context

Work packages 5 (display) and 6 (everyday) were sliced from `docs/spec.md` without a grilling session. The grilling on 14/11/2026 (wayfinder ticket #41) settled the open points. Research on the same day found that images bring no benefit on touch E-Ink tablets (refresh and ghosting are set per app by the device) and that browser-less e-paper devices (e.g. TRMNL) would need a headless-browser renderer.

## Decision

### Technology

- Display and web interface are **server-rendered HTML** (Go templates) with a **small own JavaScript**, no htmx or other framework (make or buy: the needed behaviour, SSE-triggered partial updates and timers, is small).
- The view is built from a **view model** (the data for one screen) that the HTML renderer turns into markup, so other renderers can be added later.
- **All displays have a touch screen and a browser.** Server-side image rendering (PNG) is **out of scope** for V1.0; the research (headless Chromium as optional container, TRMNL BYOS API) is kept for a later decision.
- Against ghosting, E-Ink displays reload the whole page from the box once per hour, **only during quiet mode**. This deviates from the spec criterion "when nothing changes, then no new image is transmitted" for quiet mode only.

### Display types and themes

- Three display types: **E-Ink black-and-white**, **E-Ink colour**, **normal screen** (touch tablet or monitor). They differ in palette, default theme and refresh behaviour only.
- Every display has a **theme**: light, dark or automatic. Automatic switches to dark at sunset and to light at sunrise, with **two offsets per display** (one for sunrise, one for sunset), each from **−180 to +180 minutes**; sunrise and sunset are computed locally from the home location. Defaults: E-Ink light, normal screen automatic. **Quiet mode** takes precedence over the theme.
- The web interface offers light, dark, automatic or **follow the device** (`prefers-color-scheme`, default), stored per browser or phone.
- The **dark palette** is derived from "Nordic Calm" (muted person colours, contrast at least 4.5:1, no shadows) and recorded in `DESIGN.md`. The product owner approves a preview (light, dark, black-and-white) before it is merged; this is an exception to the auto-merge rule.

### Family view

- Shows **today**. The **family strip** above the person columns holds, once each: events whose effective audience (calendar and event, ADR 0011) is the whole household, all all-day and multi-day events, and public holidays. All-day events with an audience carry the markers of their people. Timed events with an audience appear in the column of each person in their audience.
- From 18:00 a small **tomorrow** block at the end of each column shows that person's first events of the next day.
- Up to 6 people the columns get narrower (with a minimum width); from 7 people the columns scroll. Choosing people per display comes with free layouts in V1.x.
- Footer tiles are fixed in V1.0: **today's task progress per person** (instead of routines until V1.x), **dinner**, **tomorrow** (tomorrow's waste collection, highlighted from 18:00, plus a free-text note a parent sets in the web interface for a given day, shown the evening before and removed after that day). The highlight at 18:00 is the waste collection reminder; waste collection events do not appear in the family strip.
- **Quiet mode** shows a calm night page (tomorrow's date, first event tomorrow, waste collection tomorrow) and switches the front light off; a normal screen goes dark.

### Personal view and display rights

- The personal view shows the person's events for today and the next 6 days and their tasks for today with large checkboxes; it returns to the family view after 30 seconds without interaction.
- Without the parent PIN a display may show everything, tick off and untick tasks and list entries, add list entries, delete single entries and delete completed entries. Creating lists and creating or changing events happen in the web interface. In packages 5–6 the parent PIN is needed only to delete whole lists on a display.

### Web interface and phones

- The web interface gets a frame (navigation, "Nordic Calm" tokens, responsive) and a **calendar** screen: week view, create, edit and delete events with edit scopes, audience and search.
- **Phone web app in V1.0** (moved from V1.x): the web interface is an installable PWA. A signed-in parent shows a QR code; the phone opens it and the parent chooses an **account** or a **child** for it. The phone gets a long-lived, revocable **device session** listed with the other devices. A child's phone acts with the child's role: read, tick off the child's own tasks and any list entry; it cannot add entries or change anything else. The child cannot sign in. Displays keep their own display token (pairing), CalDAV clients their API token (ADR 0012).
- Offline the phone shows the last loaded view **read-only** with "as of hh:mm, not connected"; no offline changes.
- With the box's own CA (ADR 0014) the QR flow points iPhones to the profile first.

### Everyday features

- **Lists** belong to the household, have colour and black-and-white symbol, no audience, and are sorted manually or alphabetically; completed entries can be deleted together. An entry is text only (no quantity, category or person). Default lists "Shopping" and "To-dos".
- **Meal plan:** its own model, one free-text entry per day and mealtime (breakfast, lunch, dinner, snack). It appears in the calendar as a calendar **generated by the box**, read-only, with one all-day event per entry titled "<Mealtime>: <text>". "Share through the box" is on by default. On displays it shows only in the dinner tile, not in the family strip or person columns.
- **Simple tasks:** title, emoji, person, repetition (once with a date, daily, weekly on chosen weekdays), optionally morning or evening. Children may tick off their own tasks. Stored so that VTODO export (V1.x) needs no remodelling. Not in V1.0: stars, routines, subtasks, tasks without a person, suggestions who takes a task.
- **Waste collection:** a read-only calendar (usually an ICS subscription) marked as waste collection feeds the tomorrow tile, the 18:00 reminder and the night page.

### Web interface screens per package

- Package 4: connections, sync log, device tokens for CalDAV.
- Package 5: frame and navigation, calendar, displays (pairing, name, room, type, theme and offsets, quiet mode), phones (pairing, revoking).
- Package 6: lists, meal plan, tasks, waste collection marking and the note for tomorrow.

## Consequences

- Package 5 gains five tickets: web interface frame, calendar in the web interface, phone web app with QR pairing, children's phones, themes by sun position.
- Phones in V1.0 increase the number of device sessions to manage; the device list in the web interface covers displays, phones and CalDAV tokens.
- Image rendering needs a new decision if a touch-less or browser-less display is ever wanted.
