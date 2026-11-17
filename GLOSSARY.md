# Hejmo

Hejmo organizes one family's everyday life (calendar, tasks, lists, meals) on a box in their home and shows it on wall displays.

## Home and people

**Box**:
The device in a family's home that holds all data and serves the displays, web interface and API. One box serves one household.
_Avoid_: server, hub, instance

**Household**:
The family living together that one box serves. A person can later belong to more than one household (two-home model).
_Avoid_: family (as a technical term), tenant, home

**Person**:
A member of a household, adult or child, with a colour (one of eight palette colours), a black-and-white marker shape (one of eight) and a symbol. A household holds up to eight people (two parents and six children), each with their own colour and their own marker; from the ninth person on a colour or shape repeats, but no two people share the same colour and marker together.
_Avoid_: user, profile, member (alone)

**Membership**:
A person's belonging to a household, carrying their role and position in the family view.
_Avoid_: household member record

**Role**:
What a person may do in a household: **parent** (everything) or **child** (read, tick off and untick their own tasks and any list entry).
_Avoid_: permission level, user type

**Account**:
The sign-in credentials of an adult person: username, password and one-time codes. Children have no account.
_Avoid_: login, user

**Invitation**:
A one-time link (with a QR code) that a parent creates for an adult person without an account; valid once and for 7 days. The invited person opens it on their own device and chooses username and password themselves, then sets up one-time codes and recovery codes. The box stores only a hash of the link, so it is shown once; a new link replaces the old one, and a parent can withdraw it. States: open, used, expired, withdrawn.
_Avoid_: invite code, sign-up link, registration

**Parent PIN**:
A short numeric code that unlocks management on a display. It is not a way to sign in.
_Avoid_: display password, admin PIN

**Subgroup**:
A named set of people within a household, such as "School children", used to target calendars and events.
_Avoid_: group (alone), team, tag

**Audience**:
The people and subgroups an event or a calendar is meant for. No audience means the whole household.
_Avoid_: participants, attendees, visibility

**Setup wizard**:
The first-run steps on a new box: HTTPS, home location, first parent account, parent PIN, one-time code confirmation. It starts over HTTP on the LAN; no password is set before HTTPS works.
_Avoid_: onboarding, installer

**Box CA**:
The box's own certificate authority, name-constrained to the box so it cannot vouch for other sites. Devices trust it once (iOS profile, DAVx5 prompt, browser import). The fallback when no public name and certificate are used (ADR 0014).
_Avoid_: self-signed certificate, root cert

**Router name**:
The name the family's router gives the box in the home network, for example `hejmo.fritz.box`. The Box CA can cover it besides `hejmo.local` and the box's LAN address, so a phone on a VPN opens the box without a warning. Adding it later makes a new Box CA, and every device must trust the box again (ADR 0014).
_Avoid_: hostname, local name, DNS name (in user-facing text)

**Public name**:
A DNS name of the box (a free deSEC `dedyn.io` name, or a name under the family's own domain at Cloudflare, deSEC, Hetzner or INWX) that points to the box's LAN address, with a publicly trusted certificate obtained through DNS-01. The default way to get HTTPS; the Box CA is the fallback (ADR 0014).
_Avoid_: domain, DynDNS

**Passkey**:
A WebAuthn credential of an adult's account, kept on a device or in a password manager, that replaces password and one-time code at sign-in and counts as both factors (ADR 0018). It is valid only for the box name it was created under; password and one-time code stay the way back.
_Avoid_: security key, FIDO key, WebAuthn credential (in user-facing text)

**Box name**:
The name the box is served under: its public name or the Box CA's DNS name (e.g. `hejmo.local`), never an IP address. It is the relying-party ID of passkeys.
_Avoid_: hostname, domain, relying party (in user-facing text)

**Home region**:
The German state, Austrian state or Swiss canton of the household, which decides its public holidays.
_Avoid_: location, country, Bundesland (in code)

**Place list**:
The list of towns, villages and districts of Germany, Austria and Switzerland that ships inside the box (GeoNames extract, CC BY 4.0, ADR 0023). The parent picks the home location from it while typing; nothing is looked up on the internet.
_Avoid_: geocoding, address search

**Home location**:
The latitude and longitude of the household's home, found from a place the parent picks in the list of places on the box (ADR 0023, which replaces the one-time lookup of ADR 0022). The household keeps the place's id, so the box can show its region in the page language. Sunrise and sunset (front light, themes) and the weather use it; the region does not (too coarse).
_Avoid_: position, address

## Calendar

**Calendar**:
A source of events: the household's own family calendar, one synced from outside (ICS, CalDAV, Google), or one generated by the box (meal plan).
_Avoid_: feed, account, source (alone)

**Event**:
Something planned at a time: a single event, or the master of a series.
_Avoid_: appointment, entry, termin

**Series**:
An event that repeats by a rule, together with its changed and cancelled occurrences.
_Avoid_: recurring event, repeat

**Occurrence**:
One concrete appearance of an event in time. A single event has exactly one; a series has many.
_Avoid_: instance, date

**Changed occurrence**:
An occurrence of a series that differs from the rule (moved, retitled), stored as its own event linked to the series.
_Avoid_: exception, override (in prose)

**Cancelled occurrence**:
An occurrence of a series that does not take place.
_Avoid_: exdate (in prose), skipped event

**Edit scope**:
Which occurrences of a series a change or deletion touches: **this**, **this and following**, or **all**.
_Avoid_: scope (alone; clashes with token scope), mode

**Public holiday**:
A statutory day off in the home region. A **partial holiday** applies in most, but not all, municipalities of the region.
_Avoid_: feast day, bank holiday

**Family calendar**:
The household's own calendar, held by the box rather than by an outside service.
_Avoid_: local calendar, box calendar, default calendar

## Sync

**Calendar connection**:
The link from the box to an outside calendar service or to a single calendar address, together with its credentials.
_Avoid_: account, integration, provider (alone)

**App-specific password**:
A password created at the calendar service (Apple "app-specific password", Nextcloud "app password") for Hejmo alone and used in a calendar connection instead of the account's real password. The family can revoke it at the service at any time; Hejmo never asks for the real password. Not the same as a device token, which the box creates for CalDAV clients.
_Avoid_: Apple ID password, iCloud password

**Service sign-in**:
Allowing a calendar connection by signing in at the calendar service itself (Microsoft, Google) instead of entering an app-specific password. The box holds a permission the family can revoke at the service at any time; it never learns the password.
_Avoid_: OAuth login, token login

**App registration**:
Hejmo's entry at a calendar service that makes service sign-ins possible at all. Every box uses the Hejmo project's registration unless the family enters its own.
_Avoid_: client, Azure app

**Synced calendar**:
A calendar whose events come from, and may be written back to, a calendar outside the box through a calendar connection.
_Avoid_: external calendar, remote calendar, feed

**Read-only calendar**:
A synced calendar whose events the box shows but never changes outside. ICS subscriptions are always read-only; a parent can switch a CalDAV calendar between read-only and two-way (the default).
_Avoid_: subscribed calendar, imported calendar

**Sync conflict**:
The same event changed both on the box and outside since the last sync. The later change wins; the other version stays in the sync log for 30 days.
_Avoid_: collision, clash

**Sync log**:
The record, visible to parents, of sync failures and sync conflicts, including the version that lost. Shown as "Sync problems". Successful runs are not in it: they show per calendar (last successful sync with seconds, event count, new/changed/removed of the last run).
_Avoid_: history, audit log

## Views and devices

**Display**:
A paired wall screen (usually an E-Ink tablet) showing the household's day.
_Avoid_: screen, tablet, device (alone), dashboard

**Family view**:
The display's default view: one column per person, the whole household's day.
_Avoid_: home screen, overview

**Personal view**:
One person's view on a display, opened by tapping them; returns to the family view after 30 seconds without interaction.
_Avoid_: profile view, child view (that is a later, separate view)

**Kiosk adapter**:
The box's connection to the kiosk app on a display that sets its brightness (on E-Ink the front light) and switches its screen, by time of day and in quiet mode.
_Avoid_: display driver, device control

**Web interface**:
The box's pages for managing the household in a browser or as an app on a phone.
_Avoid_: web UI, admin, dashboard

**Display type**:
How a display shows things: **E-Ink black-and-white**, **E-Ink colour** or **normal screen**.
_Avoid_: display mode, device type

**Family strip**:
The row above the person columns in the family view for events meant for the whole household, all-day and multi-day events, and public holidays.
_Avoid_: banner, header events, all-day row

**Layout**:
What one display shows: the chosen people, their order and the blocks.
_Avoid_: template (alone), view (alone)

**Layout block**:
One part of a display's layout, such as calendar, tasks, meals, weather or lists, which a parent switches on or off per display.
_Avoid_: widget, tile, module

**Theme**:
Whether a display or the web interface is light or dark, switches automatically by sunrise and sunset, or follows the device.
_Avoid_: dark mode (alone), skin, colour scheme

**Quiet mode**:
The scheduled night state of a display: a calm night page about the coming morning with the front light off (or dimmed), or a black page on a normal screen without a kiosk app.
_Avoid_: sleep mode (in prose), night mode, standby

**Night page**:
What an E-Ink display shows in quiet mode: the date of the coming morning, the household's first timed event of that day and its waste collection. A normal screen shows a black page instead.
_Avoid_: sleep screen, standby page

**Screensaver**:
The photos a display shows after nobody has touched it for a while, one at a time, with the time and date small in a corner; a touch brings back the day at once. It is off during quiet mode. Without photos it shows a hint where adults add them.
_Avoid_: slideshow, photo frame, diashow

**Album**:
A named collection of screensaver photos; each photo is in exactly one album, and each display shows all albums or the ones chosen for it. A subfolder of the box's photo folder becomes an album by itself; only that source on the box is called a folder.
_Avoid_: folder (for an album), gallery, collection

**Phone**:
A smartphone paired with the box through a QR code that runs the web interface as an app for one person, adult or child. A phone's calendar app syncing over CalDAV is a CalDAV client, not a phone.
_Avoid_: mobile, client (alone), handset

**Browser session**:
The sign-in of one person in one browser with username, password and one-time code; lasts 30 days. Ended by signing out in that browser or by a parent under "Signed-in devices"; a paired phone's access is a device session, not a browser session.
_Avoid_: login (as a noun), cookie, session (alone)

**Device session**:
The long-lived, revocable access of a paired phone, which acts for one person. A browser that held one keeps a harmless marker cookie; when its device session has ended, `/login` shows "This device is no longer paired" instead of the sign-in, until a sign-in or a sign-out forgets the marker; a new pairing sets the marker again.
_Avoid_: remember-me, phone token

**Overview and detail page**:
An overview lists the things of one kind (displays, device tokens, connections, subscribed calendars, phones) as calm rows; each row opens the thing's detail page. Changing a thing and removing it, revoking it or disconnecting it happen only on its detail page, after a confirmation that names it.
_Avoid_: list page (for the overview), settings page (for the detail page)

**Device token**:
The password a calendar app or CalDAV client on a phone uses, one per device and revocable on its own; shown once when created. It is a token of scopes `calendars` and `tasks`, unlike an API token for other software.
_Avoid_: app password, phone token (a phone has a device session)

## Everyday

**List**:
A named collection of entries belonging to the household, such as "Shopping" or "To-dos".
_Avoid_: checklist, note

**List entry**:
One line of text on a list that can be ticked off.
_Avoid_: item (alone), list item

**Article**:
A thing that can be put on a list, from the article library: name, symbol or picture, department, the diets it fits and the allergens it contains.
_Avoid_: product, item (alone)

**Article library**:
The household's articles: the ones Hejmo ships plus own articles, managed by parents.
_Avoid_: catalogue, database

**Own article**:
An article a parent made for the household, with one name for every language, a department, what is in it, allergens, one or two default units and optionally a symbol, a photo or a picture from the web. Shipped articles cannot be changed; "Eigene Fassung anlegen" copies one as an own article.
_Avoid_: custom article, user article

**Department**:
The part of the shop an article belongs to (13, such as fruit and vegetables or drugstore). It sorts the library and decides which symbols are suggested first.
_Avoid_: category, aisle

**Hiding (of articles)**:
The experimental household setting that leaves articles with an allergen of chosen people out of the suggestions and the tile picker. Off by default; it needs a parent's confirmation of the notice (no guarantee, no liability). A shipped article can also be hidden by hand.
_Avoid_: filtering, blocking

**Quantity**:
Number and unit of a list entry, such as 500 g or 1 dozen.
_Avoid_: amount

**Tile view**:
A list shown as tiles with symbol or picture, name and tick; the default for children. The choice (tiles or list) is one per person for all lists and every device. A display is not a person and keeps its own view, tiles until someone switches it. An entry that names no article shows its first letter on a dashed ring instead of a picture.
_Avoid_: grid, gallery

**Diet**:
What a person eats: omnivore, vegetarian, pescatarian or vegan.
_Avoid_: nutrition type

**Allergy**:
An allergen or intolerance of a person: one of the 14 main EU allergens or free text, which cannot be checked automatically.
_Avoid_: intolerance (alone)

**Meal plan**:
The household's meals per day and mealtime: one line of free text each. Every entry also shows in the calendar as an all-day event of the generated, read-only meal-plan calendar.
_Avoid_: menu, food plan

**Mealtime**:
One of breakfast, lunch, snack or dinner, in the order of the day.
_Avoid_: meal type, course

**Meal plan template**:
A week pattern from Monday to Sunday that fills one mealtime or all mealtimes of the meal plan by itself, every X weeks (1 to 52) from a start week. Two templates may never fill the same mealtime on the same day of the same week. Edited only in the Templates menu; its changes reach today and later, never days before today.
_Avoid_: template (alone; clashes with routine template), preset, default plan

**Adjusted meal**:
A meal plan entry changed for one week where a meal plan template would fill it (also emptied). It keeps its change when the template changes, until "Back to template".
_Avoid_: override (in prose), exception

**Recipe**:
A dish with portions, ingredients and steps that can be put on the meal plan; entered by a parent, imported from a web page or from Hejmo's starter collection, always with its source.
_Avoid_: dish (alone), cooking instructions

**Ingredient**:
One line of a recipe: an article, optionally with a variety, or free text, with a quantity for the recipe's portions.
_Avoid_: component, item

**Variety**:
The more exact kind of an article, such as "Type 405" for flour, kept with an ingredient or list entry. Quantities add up only for the same variety.
_Avoid_: variant, subtype

**Pantry article**:
An article usually at home, such as salt or oil, left out by default when a recipe's ingredients go onto a list.
_Avoid_: staple, stock item

**Cooking view**:
A recipe shown for cooking on a device: large text, ingredients to tick and one step at a time, with the screen kept on.
_Avoid_: kitchen mode, cook mode

**Cut-off time**:
The time of day from which a mealtime is no longer shown on displays, set per mealtime in the household settings. Empty means the mealtime is shown for the rest of the day.
_Avoid_: deadline, expiry

**Holiday mode**:
A household-wide pause, with from and to dates, of all articles that are put on a list by themselves. The lists and settings say so while it is on.
_Avoid_: vacation mode, skip

**Task**:
Something to be done once or repeatedly, ticked off when done; may belong to a routine, carry a star value and be split into subtasks.
_Avoid_: chore, todo, job

**Task list**:
One person's tasks as a CalDAV client sees them (VTODO), one per person, opened by the token scope `tasks`. A once task shows as it is, a repeating task as today's occurrence. Clients may change title, notes, the day of a once task and completion; creating and deleting happen only in Hejmo.
_Avoid_: reminders list

**Routine**:
The ordered sequence of one person's tasks for a part of the day, such as Mia's morning. A person has at most one routine per part of the day, and a task is in at most one routine.
_Avoid_: checklist, schedule, habit

**Part of the day**:
When in the day a task or routine belongs: morning, after school, any time or evening. A task in a routine takes it from the routine; "after school" exists only in routines.
_Avoid_: time slot, daypart

**Routine template**:
A reusable routine without a person that a parent copies into a routine for someone. Hejmo ships some in German and English; parents add their own by saving a routine as a template. Using a template makes an independent copy: changing one never changes the other.
_Avoid_: preset, blueprint

**Subtask**:
One of the items a task is split into, each ticked off on its own; the task is done when all its subtasks are done.
_Avoid_: step (alone), child task

**Star**:
A unit a person earns by ticking off a task worth stars, or that a parent books by hand; every change is recorded in the person's star log.
_Avoid_: point, coin, reward (a reward is what stars are redeemed for)

**Star log**:
The record of every star booking of a person, with task or note, time and who booked it.
_Avoid_: points log, history

**Reward**:
Something a person can redeem stars for, from the household's reward catalogue kept by parents: a name, an emoji or picture, a star price, who it is for and an optional limit such as once a week.
_Avoid_: prize, shop item

**Redemption**:
Exchanging stars for a reward. A child asks and a parent confirms, or a parent redeems directly; the stars leave the person's star log only on confirmation, and never below zero.
_Avoid_: purchase, payout

**Savings goal**:
The one reward a person has chosen to save for; their personal view and the family view show the progress towards its star price.
_Avoid_: wish, target

**Streak**:
The days in a row on which a person completed a routine, counting only the days the routine applies and skipping pause days. Shown with the week's count ("5 of 7") and the best streak so far.
_Avoid_: chain, combo

**Pause day**:
A day a parent marks for a person (ill, holiday) on which routines do not count, so no streak breaks.
_Avoid_: skip day, excuse

**Badge**:
A mark a person earns once for a milestone, such as a first complete routine or a 7-day streak; it is never taken away.
_Avoid_: achievement, trophy, medal

**Waste collection calendar**:
A read-only calendar marked as listing the household's waste collection days.
_Avoid_: bin calendar, garbage feed

**Day note**:
A parent's free-text note (up to 120 characters) for one day, shown as the last line of the tomorrow tile from 18:00 the evening before until the end of that day, then deleted. A day has one note. A parent sets it on Home (card "Note on the display"); a planned note is changed or removed on its own page.
_Avoid_: reminder (that is the waste collection highlight), memo

## Contacts

**Contact**:
An entry in the household's address book: every person of the household, plus others such as grandparents, the daycare or the paediatrician. Served to parents' phones and kept in the box.
_Avoid_: address (alone), vCard (alone), person (a person is a household member)

**Hejmo name**:
The name Hejmo and the displays show for a contact, such as "Oma Gerda", next to the full name "Gerda Müller". One per contact for the whole household.
_Avoid_: alias, display name

**Contact group**:
A category a contact belongs to: family, childcare and school, doctors, emergency, other, or one a parent adds.
_Avoid_: tag, label, category

**Relation**:
The link of a contact to one or more people of the household, with what they are to them, such as "friend of Mia" or "paediatrician".
_Avoid_: connection, relationship type

## Access

**API token**:
A secret that lets software act on behalf of a person through the API, limited by its token scopes.
_Avoid_: API key, access key

**Device token**:
An API token with the token scopes `calendars` and `tasks`, created by a parent for one CalDAV client (for example "iPhone Mama") and used as the password next to the person's username. `calendars` opens the calendars, `tasks` the task lists (one per person); older device tokens with `calendars` alone still work and see no task lists. Revoked on its own.
_Avoid_: app password, CalDAV password

**MCP token**:
An API token created by a parent for one AI assistant on one device (for example "Claude on Mama's laptop"), shown once when created. Per area (events, lists, tasks with routines, templates and stars, meal plan) it holds no right, read only (`<area>:read`) or read and change (`<area>`); it never holds `read`, `write`, `tokens` or `settings`. It works only at the MCP server, and no other API token does; it acts as the parent who made it. Revoked on its own.
_Avoid_: assistant key, AI token

**MCP server**:
The box's endpoint for AI assistants (Model Context Protocol, `/mcp`). Off by default, served over HTTPS only, switched on by a parent on the page "AI assistants (MCP)". Its tools are the REST functions of the areas a token holds, except managing and syncing calendars; tokens, settings, people, displays and phones are never tools.
_Avoid_: AI mode, assistant API

**Share through the box**:
The switch on a calendar that makes the box serve it to CalDAV clients. The family calendar is always shared; on by default for ICS calendars, off for CalDAV and Google calendars.
_Avoid_: publish, export

**Recovery code**:
A one-time code shown once when one-time codes are set up, which replaces the one-time code at sign-in when the device with the authenticator app is lost.
_Avoid_: backup code, reset code

**Token scope**:
A right granted to an API token: `read`, `write`, a resource (e.g. `lists`) or a resource with an action (e.g. `events:read`).
_Avoid_: scope (alone; clashes with edit scope), permission
