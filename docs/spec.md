# Hejmo (family organizer) – specification

> Translated from the original German specification (frozen as of 14/11/2026). Scope and decisions made after that date are recorded as ADRs in `docs/adr/` and take precedence over this document.

Hejmo – family life in one place, under your own control.

Families today juggle calendars, timetables, drop-offs and pick-ups, childcare, shopping and household chores across half a dozen apps, notes and group chats. Off-the-shelf family calendars only solve half of this: they depend on a vendor's cloud, charge subscription fees and know nothing about separate households.

Hejmo is an open-source family organizer that runs locally on a small box, such as a Raspberry Pi, and shows everyday life on calm, low-power E-Ink displays on the wall. Calendar, tasks with stars, lists, meal plan and shopping are connected; school, daycare, journeys, childcare and the shared-custody model between two households are planned in as well, including conflict warnings about who drops off, who picks up or who needs the car, and when.

All data stays in your own home. Hejmo speaks open standards (CalDAV, CardDAV), integrates Google, iCloud and Outlook, offers an open API and can be connected to your own AI assistants without building in AI itself. No subscription, no advertising, no walled garden, developed in Europe under AGPL-3.0.

Status: 14/11/2026. Project: Hejmo, new development (for the decision see "Technical decisions").

## Architecture

- Central box as a Docker container, runs out of the box on a Raspberry Pi (images for ARM and x86, Docker Compose setup).
- The box holds all data locally and handles sync, logic and delivery.
- Multiple displays per box are possible.
- Web view on the local network: the display is reachable over the LAN and can be embedded in any screen, E-Ink or not.
- Access on the go optionally via remote access to the box (NetBird or WireGuard, from version 2). No mandatory cloud.

## Displays

- Primary target device: tablet with an E-Ink display.
- Colour mode as default, plus a black-and-white mode.
- In black-and-white mode, people are distinguished by shapes instead of colours (circle filled/empty, square filled/empty), see draft 2b.

## Basic features (common denominator of commercial and open-source offerings)

- Calendar with day, week, month and agenda views
- Sync with Google, Apple/iCloud, Outlook, ICS feeds and CalDAV (e.g. Nextcloud, Proton)
- Family profiles with colour coding per person
- Tasks, chores and routines (tasks in the VTODO standard)
- Stars/points and rewards
- Lists (shopping, to-dos, checklists)
- Meal planning
- Weather display
- Photo/screensaver mode
- Quiet mode on a schedule
- Mobile app (on Wi-Fi, on the go via remote access)

## Privacy and security

- All data local, no telemetry
- Parent PIN for settings and administration

## Deliberately excluded

- Built-in AI features (Hejmo stays open to your own assistants via API and MCP)
- App store, learning and entertainment offerings

## Feature specification

For each feature, the original draft comes first, followed by the points decided in brainstorming. In case of contradiction, "Decided in brainstorming" applies.

### Calendar

- Views: day, week, month, agenda; default view configurable per display
- Create, edit and delete events on the display, in the web view and in the mobile app
- Recurring events (daily, weekly, monthly, yearly) with editing of "this one only / this and following / all"
- Assignment of an event to one or more people
- Reminders via push to the mobile app
- Countdowns ("X days left until …")

Decided in brainstorming:

- Weather per event: location of the event, otherwise home location as fallback; house icon (home location) or pin icon (event location); display of weather conditions and temperature at the time of the event (model: Skylight)
- Day view switchable: one column per person (model: Hearth) or a shared timeline with colours, multicoloured when several people are involved (model: Dæly)
- Public holidays for DACH, by federal state or canton (model: Dæly)
- Timetable per child (model: Dæly): as a "School" or "Daycare" block in the calendar, expandable for details; A/B weeks
- Journey times in the recurring event series: time block for the outbound and return journey; return destination selectable (home or directly to the next event)
- Drop-off/pick-up: assignment of who drops off and who picks up, or whether the child goes alone; warning if the responsible person has another event at that time
- Multiple households (model: Skylight): each household has its own box; boxes exchange only shared events. Parents see everything, grandparents only the children plus themselves. With their own box, grandparents may write, and entries land directly in the calendar. Alternative: externally reachable link with login, read-only.
- Event search on the phone and on the E-Ink display (model: Dæly)
- Multi-day events as a continuous bar (model: Dæly)
- Assign entire synced calendars to a person, e.g. a club calendar (model: Hearth)
- Multiple private calendars can be included; displayed as "busy" or with full information depending on sharing (model: Dæly)
- Assign external calendars to the whole family or to subgroups, e.g. a school holiday calendar for the school children, a daycare calendar for the daycare child
- Birthdays with age display and advance reminder
- Event types with specific additional fields: birthday party (automatic reminder one week before), sport, music lessons, other activities, doctor's appointment. Storage compatible with CalDAV (e.g. via CATEGORIES and X-properties), no walled garden.

### Contacts

- Contact book with birthdays, phone numbers, email etc.
- Assign contacts to one or more family members (e.g. playmates, doctors)
- Link children's contacts to their legal guardians; for a playdate it is visible which of your own children is meeting which child, with quick access to the parents
- Sync via CardDAV, vCard format (relationships e.g. via RELATED), so that contacts can be transferred to other systems
- Items to bring to an event (e.g. "gym bag" for sport)
- Sport in the timetable automatically creates a task in the respective person's morning routine

### Childcare

- Can be enabled per child: lunchtime and/or evening childcare
- When active, each block defines who provides childcare and how long the block lasts; open blocks are flagged
- Childcare can be provided by parents, grandparents and other contacts (e.g. nanny, neighbours); separate contact types for this
- Monthly planning: childcare providers enter their availability, an algorithm (rule-based, no AI) proposes the allocation
- Multiple children: the algorithm checks whether childcare can be combined or must be separate, including whether a pick-up is feasible during another child's childcare (journey times taken into account)

### Calendar sync

- Two-way sync with Google, iCloud, Outlook and any CalDAV server (Nextcloud, Proton, Fastmail)
- ICS feeds read-only (e.g. school, club, public holidays)
- Delta sync via ETag/CTag, interval configurable (default 5 minutes)
- Conflict resolution: last change wins, conflicts are logged
- Credentials encrypted on the box

### People and profiles

- Profile per family member: name, colour, icon, optional avatar
- Colour mode: colour per person; black-and-white mode: shape per person (circle filled/empty, square filled/empty)
- Filter by person in all views
- Roles: parent (full rights) and child (check off, no administration)

### Tasks, chores and routines

- One-off, daily and weekly tasks per person
- Routines as groups (morning, evening) with large checkboxes
- Storage in the VTODO standard, sync via CalDAV
- Progress indicator per person and day

Decided in brainstorming:

- Large emojis on tasks (model: Dæly)
- General task list without fixed assignment (model: Dæly); in weekly planning, tasks from it can be assigned to people
- Recurring general tasks with a history of who last took them on
- Suggestion algorithm (rule-based): suitability configurable per task (children, adults, everyone, or by attribute such as hobby); based on suitability and history, it suggests who takes the next round
- Parents can confirm or check off children's tasks on the go (model: Hearth)
- Templates for routines, adoptable with one tap (model: Cozyla)
- Weekly templates for A/B weeks in the shared-custody model: children change home (e.g. mum/dad, each with or without a new partner) or the parents change while the children stay in the same home (nesting model)
- Shared-custody model as a setting:
  - Nesting model (same household): depending on the week, it changes which adults are shown
  - Two-home model (two households, each with its own box/tenant): children move between the tenants, the children are the only overlap; their events are always visible and up to date in both households, even in weeks when they are not present; events of a parent and a new partner remain private in the respective tenant
- Handover day in the shared-custody model freely configurable (e.g. Wednesday instead of the weekend), including time or occasion (e.g. after school); the handover appears as its own event with drop-off/pick-up

### Points and rewards

- Completed tasks earn stars; number per task configurable
- Reward catalogue, maintained by parents
- Children redeem rewards on the display, parents confirm
- Traceable points log

Decided in brainstorming:

- Avatars per child
- Reward feedback depending on the display type: on E-Ink a large icon or sticker, on normal displays additionally animations and emoji celebrations (model: Skylight); can be switched off in the low-stimulus profile

### Lists

- Any number of lists (shopping, to-dos, packing lists)
- Check off, hide or delete completed entries
- Editing on the display, in the web view and in the mobile app

Decided in brainstorming:

- Sorting manual or alphabetical (model: Dæly)
- Pre-installed default lists, e.g. shopping and to-dos (model: Skylight)
- Consumables with a restock calculator: consumption per day and person (e.g. X nappies/day), stock and pack size; calculates when to reorder and puts the item on the shopping list in good time
- The calculator takes into account presence according to the shared-custody model and absences (only days on which the child is in the household)
- Same calculator for recurring groceries for meals that are not individually planned (e.g. breakfast: bread, milk, cereal), also presence-dependent
- Own colour per list, own icon in black-and-white mode (model: Hearthboard)

### Meal planning

- Weekly plan with breakfast, lunch, dinner, snack
- Optionally per person
- Save recipes and transfer ingredients to the shopping list
- Optional integration with Mealie

Decided in brainstorming:

- Recipe collection (upload or enter manually); meals appear in the calendar (model: Skylight)
- Shared family meal plan as default, plus a meal plan per person, e.g. when a child eats lunch at daycare (model: Hearthboard)
- Optional integration with Mealie (self-hosted open-source recipe manager, Docker) for recipes, import from websites and shopping lists
- Ingredients from the meal plan automatically land on the shopping list; portions are based on who is present (shared-custody model, absences)

### Weather

- Current weather plus forecast (e.g. via Open-Meteo, without an API key)
- Location and unit (°C/°F) configurable

### Photo mode

- Screensaver with family photos after an idle time
- Source: uploaded photos or a folder on the box
- Active by default only in colour mode; can be switched off in black-and-white mode

Decided in brainstorming:

- No photo album feature (no flashbacks, no gallery); photos only for the screensaver
- Upload photos; albums as source, albums assigned per device or globally
- Screensaver can be enabled per device, with settings (e.g. idle time, change interval, clock overlay, time window)
- Image processing per display type: black-and-white E-Ink (greyscale/dithering), colour E-Ink (reduced palette), normal screen (full colour)

### Dashboard and display

- Freely arrangeable tiles per display (today's events, week, tasks, lists, meals, weather)
- Layouts can be saved per display, editing behind the parent PIN
- E-Ink-optimised: partial refresh, no animations, high contrast
- Quiet mode on a schedule

Decided in brainstorming:

- One page per display, no multi-page dashboards
- Brightness: use the light sensor if present; fallback via sunrise and sunset, calculated locally from the home location (as for the weather)
- Portrait and landscape, with an adapted layout per orientation (model: LOOFII)

### Mobile app and remote access

- Web app (installable) or native app
- On Wi-Fi directly via the box, pairing via QR code
- On the go via remote access to the box (NetBird or WireGuard, from version 2)
- Devices can be unpaired individually

Decided in brainstorming:

- Home screen widgets on the phone for calendar, tasks, lists, shopping (model: Dæly); wanted, but low priority
- Full-featured web interface for the desktop to manage all features (customer request at Dæly, model: FamilyWall)

### Box and operation

- Docker image for ARM (Raspberry Pi) and x86, started via Docker Compose
- Local database (e.g. SQLite), backup and restore as an export
- Automatic updates optional
- Web admin interface for setup, behind the parent PIN

Decided in brainstorming:

- Updates configurable: default automatic at night at 03:30 (model: OpenSkyLight), alternatively only after confirmation by the parents

## Additional features (brainstorming, part 2)

### Absence

- Absence types: sick (child at home, needs childcare all day, an open childcare block is created automatically), holiday or day off with external childcare, not present (routines, journeys and childcare blocks are dropped)
- If a childcare provider is sick, they drop out; the plan shows the gaps

### Emergency card

- Per child: allergies, medication, blood type, paediatrician, emergency contacts
- Accessible with one tap on the display or phone, can be shared selectively with childcare providers

### Weekly preview

- Every Sunday evening: events, drop-off/pick-up, open childcare gaps, resource conflicts

### Packing list generator

- Reusable templates (holiday, sleepover, school trip), generate a checklist per child, linked to the absence event

### Deadlines and letters to parents

- Deadlines with due date, responsible person and reminder (e.g. consent form, money for a trip, registration deadlines)

### Children's view and accessible profiles

- Own children's view on the display: their own day with large icons, what comes next, who picks them up, stars collected
- Profiles per child, combinable:
  - Not yet reading: only icons and photos, time as a clock face or progress bar, optional read-aloud
  - Low-stimulus: no gradients, no animations, muted colours, few elements
  - Red-green colour deficiency: colour-blind-friendly palette, important information additionally shown by icon
  - ADHD: only the next step large, visual timer, routines in small steps, clear feedback when checking off
- The default is the family view; tapping on your own person switches to the personal view (with the respective profile). Without interaction for a configurable time (default 30 seconds), the display automatically returns to the family view.

### Transport and resources

- Transport mode per journey: car, public transport, bicycle (extensible)
- Journey times as a default value per route and mode or via local route calculation (e.g. Valhalla/OSRM with OpenStreetMap; public transport via GTFS, otherwise the default value as fallback)
- Resources such as car, bicycle or bicycle trailer with their own booking schedule: when booked, for which activity
- Resource conflicts are shown immediately
- Childcare providers enter their availability for childcare themselves (e.g. via a link), or the parents do it for them

### Homework and learning

- Overview per school child: homework and upcoming class tests
- Study plan per class test (e.g. 15 minutes daily in the week before); study sessions automatically appear in the afternoon routine

### Home Assistant integration

- Optional and purely local: the box provides events (e.g. "pick-up in 15 minutes", "routine done"), for example via MQTT or webhook
- Examples: the light in the children's room switches on when the routine starts, a lamp flashes when someone needs to leave

## Technical decisions

- New build instead of a fork: OpenSkyLight is a Windows Electron app without an identifiable licence file, Hearthboard a pure Android app (GPL-3.0); neither fits the box architecture. Ideas are adopted, no code.
- Backend in Go: one compact binary, low RAM, ARM-capable
- SQLite as the database, no separate DB server
- Server-side rendering: lean HTML without heavy frameworks; for E-Ink optionally pre-rendered images from the box
- Event-driven: delta sync, displays are only updated when something changes
- Heavy components (e.g. route calculation) as optional separate containers
- Target hardware: Raspberry Pi 4 with little RAM
- User interface: server-side rendered HTML (Go templates) with htmx for displays and the web interface, as lightweight as possible; a separate solution only for the later native mobile app
- Display target platform: Android E-Ink tablets with touch (e.g. Boox, Bigme), in black-and-white and colour E-Ink variants; touch is mandatory. Jailbroken Kindles at most as an unofficial hobbyist variant (display only, no touch)
- Adult sign-in: passkeys as default; fallback password plus one-time code (TOTP from an authenticator app)
- Remote access: open source and EU-based where possible. Default NetBird (Berlin, self-hosted, WireGuard-based) as an optional container; plain WireGuard as a lean alternative for technically minded users. Headscale/Tailscale rejected (not EU, Tailscale clients or proprietary control plane)
- Grandparent link (read-only, with login) via reverse proxy with its own domain and HTTPS
- Setup wizard in the web interface: enable remote access with one click, check reachability (domain/DynDNS, port forwarding, with concrete instructions e.g. for Fritzbox), add devices via QR code (NetBird setup keys or WireGuard configuration), device overview with one-click access revocation

### Framework conditions

- Project licence: AGPL-3.0
- Languages: start with German and English; all texts translatable from the start (i18n), further languages from the community; language per profile from version 3
- Project name (fixed): Hejmo (Esperanto for "home"). Short domains (.de/.com/.org/.eu) are taken, so a compound domain is needed (e.g. hejmo-project.org, gethejmo.org). Open: trademark check at DPMA/EUIPO in classes 9 and 42; known name proximity: festival "San Hejmo" (Parookaville GmbH), smart home brand "Hejhome". Rejected: Nestplan, Heimtakt (taken), "Nest" names (proximity to NestBoard, Nestify)
- Repository: one monorepo on GitHub with folders for the Go core, UI templates (htmx), Docker, documentation and tests
- CI (GitHub Actions): on every pull request, all tests on ARM and x86; merge only with green tests
- Design: drafts on the "Hejmo – Style directions" canvas. Default style from version 1: "Nordic Calm" (2), in black-and-white mode with shapes instead of colours as person markers. "Playful" (4) comes as an alternative template in version 3

### Box architecture

- Building blocks: core service (Go, API, permissions), sync module (CalDAV/CardDAV/ICS, two-way), render module (HTML or images for E-Ink), push channel (e.g. Server-Sent Events), scheduled planner (childcare, task suggestions, restocking), optional containers (routing, Mealie, NetBird/WireGuard, Matrix)
- The box is the leading data source; external sources (Google, iCloud, Outlook, Nextcloud, ICS) are integrated and synchronised
- The box is itself a CalDAV and CardDAV server: family members subscribe to events, tasks and contacts directly in their normal phone apps, without a dedicated app
- REST API with OpenAPI description as the only interface for the web interface, displays, phone and integrations; access tokens with restrictable rights (e.g. read-only, lists only)
- AI-open, without its own AI: optional MCP server based on the API, off by default; users connect their own assistant (local or cloud) as needed and use a token to define what it may see and change

### Permissions and role model

- Parents: all rights (view, edit, manage, configure)
- Children: own area and family view; check off, redeem rewards; no administration
- Grandparents: children plus themselves; with their own box, write access; via link, read-only
- Childcare providers (nanny, neighbours): only the children they look after and the times, emergency card, their own availability
- Displays (device role): only display and check off, no administration
- Rights configurable in more detail per person (e.g. an older child may create events)

### Display pairing

- On first start, a new display shows a QR code or a six-digit code
- A parent confirms in the web interface and sets name, room, layout and display type (black-and-white E-Ink, colour E-Ink, normal screen)
- The display then receives its own access token, revocable at any time

### Messages and calls

- No forwarding to third-party apps (WhatsApp, Signal etc.), no complete in-house build
- Matrix as an optional container (lean server, e.g. Conduit); end-to-end encrypted
- Integration into our own app via SDKs (matrix-rust-sdk for iOS/Android, matrix-js-sdk for web, mautrix-go in the backend); Matrix stays invisible to users, the box creates accounts automatically
- Voice and text messages to approved contacts; children can only reach approved contacts
- Only one-to-one video and voice calls: directly via WebRTC, no LiveKit needed; small TURN server (coturn) as fallback
- Calls only on devices with a camera and a normal screen, not on E-Ink

### Data model

- Own data model in SQLite; translation into iCal (CalDAV) and vCard (CardDAV) during sync
- Additional information (event type, journey times, drop-off/pick-up etc.) as custom X-properties for machines; other box instances read them in full, normal apps ignore them
- Additionally a human-readable summary in the event description (e.g. "Drop-off: Dad, pick-up: Grandma")
- Optionally adapted titles (e.g. "Journey to daycare (Dad)"), off by default
- Journey times as separate events linked to the main event (visible in normal calendar apps, responsible person as attendee); own event type "Journey", shown visually distinct in the box UI (e.g. hatched or narrower)
- Timetable: one "School" or "Daycare" event per day, individual lessons as additional information (expandable in the box); selected subjects (e.g. sport) can additionally be output as separate events
- Tasks exposed externally as VTODO (can also be checked off externally, stars are still credited); stars, rewards and points log only in the box
- With linked households (multiple boxes), stars and points log per child are synchronised between the boxes
- Star balance per child visible in all linked households; each household has its own reward catalogue and decides for itself what stars can be redeemed for
- Configurable whether the star account belongs to the child (one account across all households) or to the household (separate account per household); default: the child
- Conflicts between boxes: in principle the last change wins; for drop-off/pick-up no silent overwrite, but a notice for confirmation by a parent

## Development approach and acceptance criteria for version 1 (draft)

Development strictly test-driven (TDD): each criterion is first written as a failing test, then implemented. Criteria in the format Given / When / Then.

### Test strategy

- Unit tests in Go for domain logic (series, permissions, stars, iCal/vCard translation)
- Integration tests against a real SQLite database and a CalDAV test server (e.g. Radicale in a container)
- API contract tests against the OpenAPI specification
- End-to-end tests for the display and the web interface (e.g. Playwright), including image comparison for E-Ink renderings
- CI: all tests on ARM and x86 before every merge

### Platform

- Given a fresh Raspberry Pi with Docker, when `docker compose up` is run, then the web interface is reachable within 60 seconds.
- Given a running box without configuration, when the web interface is opened, then the setup wizard starts (home location, first parent account, parent PIN).
- Given automatic updates are active, when an update is available at 03:30, then it is installed and the box is reachable again afterwards; if the start fails, it rolls back to the previous version.
- Given an API token with the "read-only" right, when it is used to create an event, then the API responds with 403.
- Given MCP is switched off (default), when an MCP client connects, then the connection is rejected.

### Calendar and sync

- Given a connected CalDAV calendar, when an event is created externally, then it appears on the box after the next sync interval.
- Given an event on the box, when it is edited, then the change is visible in the connected external calendar after the next sync.
- Given the box as a CalDAV server, when a phone subscribes to the family calendar, then it sees all events its role is allowed to see, and no others.
- Given an event series, when "this and following" is changed, then earlier occurrences remain unchanged.
- Given an event spanning three days, when the week view is shown, then it appears as one continuous bar.
- Given the home location Bavaria, when the calendar is shown, then Bavarian public holidays are included and none from other federal states.
- Given an external calendar assigned to the subgroup "School children", when the daycare child's view is shown, then its events do not appear.
- Given an event with additional fields, when it is exported via CalDAV, then it contains the X-properties and a human-readable summary in the description; the title remains unchanged as long as title adaptation is off.

### People and permissions

- Given a child profile, when it tries to delete an event, then this is refused.
- Given a tap on a profile on the display, when no interaction follows for 30 seconds, then the display returns to the family view.
- Given black-and-white mode, when an event with two people is shown, then both people are distinguishable by their shape (circle/square, filled/empty), without colour.

### Tasks and stars

- Given a task worth 3 stars, when the child checks it off on the display, then their star balance increases by 3 and a log entry is created.
- Given a task as a VTODO in an external app, when it is checked off there, then the stars are credited after the next sync, exactly once.
- Given a checked-off task, when the check mark is undone, then the stars are deducted again.
- Given a morning routine with four steps, when all are checked off, then the progress indicator shows 100 %.
- Given a parent on the go, when they confirm a child's task in the web app, then it is immediately visible as done on the display.

### Lists and meals

- Given a new box, when setup is complete, then the lists "Shopping" and "To-dos" exist.
- Given a list with alphabetical sorting, when an entry is added, then it appears in the correct alphabetical position.
- Given a checked-off entry, when "Delete completed" is selected, then only the checked-off entries disappear.
- Given a shared meal plan, when a dinner is entered for Tuesday, then it appears in the calendar on Tuesday.

### Displays

- Given an unpaired display, when it starts, then it shows a QR code and a six-digit code.
- Given a paired display, when its token is revoked in the web interface, then it shows the pairing screen again within one minute.
- Given an E-Ink display, when nothing changes, then no new image is transmitted.
- Given an E-Ink display, when an event is changed, then the change is visible within 10 seconds.
- Given quiet mode from 22:00 to 06:00, when it turns 22:00, then the display switches to quiet mode.
- Given no light sensor, when the sun sets at the home location, then the brightness is lowered.
- Given portrait orientation, when the display is rotated, then the layout switches to landscape.

### Photos and waste collection

- Given a photo folder and 5 minutes of idle time, when the screensaver is active, then the photos change at the configured interval.
- Given a waste collection ICS subscription, when there is a collection the next day, then a reminder appears at 18:00 the evening before.

### Non-functional criteria

- Idle RAM of the core box below 150 MB, idle CPU below 5 % on a Raspberry Pi 4.
- API response time for standard queries below 200 ms.
- Credentials for external calendars are stored encrypted; not readable in a plain-text dump of the database.
- No outgoing connections except to configured calendars, the weather service and the update server.

## Design specification "Nordic Calm" (German original: "Nordisch ruhig")

Default style from version 1: calm, plenty of white space, white cards on a warm background, one muted colour per person; in black-and-white mode, shapes instead of colours. The reference is draft 2 (colour) and draft 2b (black-and-white) on the "Hejmo – Style directions" canvas; the values below are binding if the canvas is not accessible.

### Layout of the family view (reference 1024×768, landscape)

- Header: on the left, weekday and calendar week (CW) (small) above the date (large); on the right, two white chips: leave-the-house countdown with a clock icon, and weather with temperature, cloud icon and a house or pin icon for the location
- Main area: one white column per person in a 4-column grid; at the top, person marker and name, below that event cards in chronological order
- Footer: three white tiles – routines with progress bar and stars, dinner, note for tomorrow (e.g. bins)

### Colours

| Role | Colour mode | Black-and-white mode |
|---|---|---|
| Background | #F6F3EE | #EDEDEA |
| Columns, tiles, chips | #FFFFFF | #FFFFFF |
| Text | #2A2A28 | #111111 |
| Secondary text (labels) | #5C5A55 | #3A3A38 |
| Event card | Person tint (see below) | #E4E4E1 |
| Progress bar | Person colour on person tint | #111111 on #D6D6D3 |

| Person (example) | Person colour | Tint (card surface) | Text on tint | Black-and-white marker |
|---|---|---|---|---|
| Mama | #B4583A | #F7E7E0 | #5C3A2C | Circle filled |
| Papa | #2F5E7A | #E1EBF1 | #24485E | Circle empty |
| Sebastian | #4E7D52 | #E3EEE3 | #2F4D31 | Square filled |
| Mia | #9A7420 | #F3EAD3 | #5A4513 | Square empty |

Additional people get colours of the same lightness and saturation; in black-and-white mode, additional shapes (e.g. triangle, diamond). Person colours must also differ in lightness; text reaches a contrast of at least 4.5:1.

### Typography

| Element | Typeface | Size | Weight |
|---|---|---|---|
| Date (header) | Figtree | 36 px | 700 |
| Key figures (countdown, temperature, dinner) | Figtree | 22 px | 700 |
| Person name (column header) | Figtree | 19 px | 700 |
| Event title | Figtree | 16 px | 600 (black-and-white: 700) |
| Body text, tile content | Figtree | 16 px | 400 |
| Labels (CW, tile titles) | Figtree | 14–15 px | 400 |
| Time, additional info on event | Figtree | 13 px | 400 |

### Shapes, spacing, elements

- Corners: columns and tiles 22 px, header chips 18 px, event cards 14 px, progress bar 4 px (height 8 px)
- Spacing: outer margin 28 px, gap between columns and tiles 14 px, gap between header/main area/footer 20 px, column inner padding 16 px, event card 10×12 px, gap between cards 10 px
- Person marker: circle 14 px in the person colour; in black-and-white mode, shape 16 px in the column header and 9 px on each event
- Events: filled card in the person tint, time at the top, title below, optionally one additional line (e.g. "pick-up: Dad")
- Journeys: no fill, 1.5 px dashed border in the person colour (black-and-white: #111111), label "Journey" after the time
- Icons: line icons with 1.8 px stroke width in Papa blue (black-and-white: #111111); no gradients, no shadows
- Touch targets at least 44 px; no animations on E-Ink

## Version planning

Version 1 covers the everyday life of one household; version 2 brings school, childcare and logistics; version 3 automation and multiple households.

| Cluster | Version 1 | Version 2 | Version 3 |
|---|---|---|---|
| Platform | Docker (ARM/x86), local DB, web admin, parent PIN, updates at 03:30, REST API with token, optional MCP server, box as CalDAV/CardDAV server | Encrypted nightly backup (USB/NAS) | – |
| Displays | One E-Ink display + LAN web view, colour/B&W mode, portrait/landscape, quiet mode, brightness by sun position | Multiple displays, light sensor, animations per display type | – |
| Calendar and sync | Views, CalDAV/Google/iCloud/Outlook/ICS, series, multi-day bars, public holidays DACH, full-text search across everything, assignment to people/subgroups | Column per person, private calendars (busy/full), event types, weather per event, email inbox for .ics invitations | Morning countdown |
| People and contacts | Profiles, roles parent/child | Contact book with CardDAV, assignment to people, birthdays | Playdate linking with legal guardians |
| Tasks and rewards | Tasks with subtasks, routines, emojis, stars, checking off on the go | General tasks, suggestion algorithm, rotation, routine templates, reward catalogue, streaks and badges | Pocket money, savings goals, screen time |
| Lists, meals, shopping | Default lists, sorting, colour/icon, shared meal plan | Meal plan per person, recipes, ingredients to shopping list, Mealie | Restock calculator, pantry stock |
| School, daycare, logistics | – | Timetable with A/B weeks, journey times as default value, drop-off/pick-up with conflict warning, sport in morning routine, items to bring | Local route calculation, resource booking, homework/study plan |
| Childcare and absence | – | Manual childcare blocks, absence types | Monthly planning algorithm, availability via link |
| Multiple households | – | Nesting model, weekly templates, freely selectable handover day | Box-to-box sync, two-home model, external link with login |
| Phone and remote access | Web app on Wi-Fi with QR pairing | Desktop web interface, remote access via NetBird/WireGuard with setup wizard, push | Home screen widgets |
| Views and accessibility | Family/personal view (30 s return) | Children's view, low-stimulus, red-green palette | Non-reader profile, ADHD profile, multilingual support, alternative design template "Playful" |
| Photos | Screensaver from folder | Albums per device, image processing per display type | – |
| Organisation and health | Waste collection calendar via ICS | Emergency card, deadlines, weekly preview, packing lists | Medication, vaccinations, maintenance dates, household budget book, lent items, plants/pets, family council, pinboard, PDF printing, carpools |
| Integrations | – | – | Home Assistant (local), Matrix: messages, voice messages, 1:1 calls |

## Feature roadmap (backlog, accepted)

- Morning countdown until leaving the house (from the first event and journey time)
- Convert stars into pocket money or other rewards
- Medication plan with reminders and checking off
- Vaccinations and preventive check-ups (U check-ups) per child with reminders
- Rotation schedule for chores between the children
- Pantry stock with automatic entry on the shopping list
- Family pinboard for short messages
- Waste collection calendar via the municipality's ICS subscription, reminder the evening before
- Lightweight household budget book for shared expenses
- Maintenance and expiry dates (TÜV vehicle inspection, tyres, passports, insurance)
- Plant and pet care on a rotating basis
- Screen time: buy media time with stars, remaining time on the display
- Family council: votes on the display
- Lent items list (lent out and borrowed)
- Print the weekly plan as a PDF
- Carpools with other families
- Multilingual support per profile
- Encrypted nightly backup to a USB stick or NAS

### Further accepted suggestions (round 3)

- Night light mode: shows children at night whether they are allowed to get up (moon/sun)
- Bedtime countdown for children
- Gift ideas list per person, hidden from the person concerned
- Children's wish lists, linkable to stars
- Mood check-in in the evening via icon
- Store of outings and activities, optionally weather-dependent
- Clothing tip based on the weather in the morning routine
- Holiday planner: align school holidays, parents' leave and holiday childcare, show gaps
- Club and course management with costs, term, notice period
- Reading tracker with stars
- Practice tracker for an instrument or sport
- Shared family goals with stars
- Public transport departure board, only where an open interface exists
- Arriving home: the child checks off on the display that they have arrived when no parents are home; parents are notified (not for school)

### Adopted from NestBoard and Nestify (round 4, without AI features)

- Email inbox: the box fetches a family mailbox and automatically adopts invitations with an .ics attachment; follow-up emails update the event instead of duplicating it (model: NestBoard, without AI)
- Streaks and badges for tasks (e.g. 7 days of morning routine in a row)
- Savings goals: goal per child with a progress bar from pocket money, plus shared family savings goals
- Subtasks, individually checkable
- Full-text search across everything: events, tasks, lists, recipes, contacts, notes

Not adopted: voice input, photo and email import with AI analysis, built-in AI assistant.

Later (not in versions 1–3, possibly in a larger expansion stage): clothing sizes, growth chart, illness diary, family diary, guest mode.

Rejected in principle: device/warranty archive, Wi-Fi guest QR code, emergency plan.

The assignment of roadmap items to versions is given in the version planning.

## Open points

- Trademark check "Hejmo" (DPMA/EUIPO, classes 9 and 42) and decide on the domain
- Decide on the reference tablet and check the assumed target values (see the "Handover to the build agent" tab, `docs/handover.md`)
