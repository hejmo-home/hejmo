# 0015. Display, web interface and everyday features

- Status: Accepted
- Date: 14/11/2026
- Amends: [0003](0003-v1-scope-and-work-packages.md) (phone web app moves into V1.0), [0008](0008-design-takes-precedence-over-version-plan.md) (footer tiles)

## Context

Work packages 5 (display) and 6 (everyday) were sliced from `docs/spec.md` without a grilling session. The grilling on 14/11/2026 (wayfinder ticket #41) settled the open points. Research on the same day found that images bring no benefit on touch E-Ink tablets (refresh and ghosting are set per app by the device) and that browser-less e-paper devices (e.g. TRMNL) would need a headless-browser renderer.

## Decision

### Technology

- Display and web interface are **server-rendered HTML** (Go templates) with a **small own JavaScript**, no htmx or other framework (make or buy: the needed behaviour, SSE-triggered partial updates and timers, is small).
- The view is built from a **view model** (the data for one screen) that the HTML renderer turns into markup, so other renderers can be added later.
- **All displays have a touch screen and a browser.** Server-side image rendering (PNG) is **out of scope** for V1.0; the research (headless Chromium as optional container, TRMNL BYOS API) is kept for a later decision.
- E-Ink displays get a full re-render once per hour during quiet mode against ghosting.

### Display types and themes

- Three display types: **E-Ink black-and-white**, **E-Ink colour**, **normal screen** (touch tablet or monitor). They differ in palette, default theme and refresh behaviour only.
- Every display and the web interface have a **theme**: light, dark or automatic. Automatic switches to dark at sunset and to light at sunrise, each shifted by an offset of **−180 to +180 minutes** set per display; sunrise and sunset are computed locally from the home location. Defaults: E-Ink light, normal screen automatic, web interface follows the device (`prefers-color-scheme`). **Quiet mode** takes precedence over the theme.
- The **dark palette** is derived from "Nordic Calm" (muted person colours, contrast at least 4.5:1, no shadows) and recorded in `DESIGN.md`. The product owner approves a preview (light, dark, black-and-white) before it is merged; this is an exception to the auto-merge rule.

### Family view

- Shows **today**. Events for the whole household (no audience), all-day events and public holidays appear once in a **family strip** above the person columns; the columns hold events with an audience.
- From 18:00 a small **tomorrow** block shows the first events of the next day.
- Up to 6 people the columns get narrower (with a minimum width); from 7 people the columns scroll. Choosing people per display comes with free layouts in V1.x.
- Footer tiles are fixed in V1.0: **today's task progress per person** (instead of routines until V1.x), **dinner**, **tomorrow** (tomorrow's waste collection, highlighted from 18:00, plus a free-text note a parent sets in the web interface that disappears after its day).
- **Quiet mode** shows a calm night page (tomorrow's date, first event tomorrow, waste collection tomorrow) and switches the front light off; a normal screen goes dark.

### Personal view and display rights

- The personal view shows the person's events for today and the next 6 days and their tasks for today with large checkboxes; it returns to the family view after 30 seconds without interaction.
- Without the parent PIN a display may show everything, tick off tasks and list entries, and add list entries. Creating or changing events happens in the web interface. In packages 5–6 the parent PIN is needed only to delete whole lists.

### Web interface and phones

- The web interface gets a frame (navigation, "Nordic Calm" tokens, responsive) and a **calendar** screen: week view, create, edit and delete events with edit scopes, audience and search.
- **Phone web app in V1.0** (moved from V1.x): the web interface is an installable PWA. A signed-in parent shows a QR code; the phone opens it and the parent chooses an **account** or a **child** for it. The phone gets a long-lived, revocable **device session** listed with the other devices. A child's phone acts with the child's role (read, tick off own tasks and list entries); the child cannot sign in.
- Offline the phone shows the last loaded view **read-only** with "as of hh:mm, not connected"; no offline changes.
- With the box's own CA (ADR 0014) the QR flow points iPhones to the profile first.

### Everyday features

- **Lists** belong to the household, have colour and black-and-white symbol, no audience. An entry is text only (no quantity, category or person). Default lists "Shopping" and "To-dos".
- **Meal plan:** its own model (day × breakfast, lunch, dinner, snack, free text). It appears in the calendar as a generated, read-only calendar "Meal plan" that can be shared through the box; on displays it shows only in the dinner tile, not in person columns.
- **Simple tasks:** title, emoji, person, repetition (once with a date, daily, weekly on chosen weekdays), optionally morning or evening. Children may tick off their own tasks. Stored so that VTODO export (V1.x) needs no remodelling. Not in V1.0: stars, routines, subtasks, tasks without a person, suggestions who takes a task.
- **Waste collection:** an ICS subscription marked as waste collection feeds the tomorrow tile and the evening reminder at 18:00.

## Consequences

- Package 5 gains five tickets: web interface frame, calendar in the web interface, phone web app with QR pairing, children's phones, themes by sun position.
- Phones in V1.0 increase the number of device sessions to manage; the device list in the web interface covers displays, phones and CalDAV tokens.
- Image rendering needs a new decision if a touch-less or browser-less display is ever wanted.
