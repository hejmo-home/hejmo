# 0016. Family view header, columns and footer

- Status: Accepted, amended by [0018](0018-path-to-0-1.md), [0021](0021-home-location-from-a-list-on-the-box.md). Amended by the product owner on 15/11/2026 (see Amendment of 15/11/2026) and on 16/11/2026 (see Amendment of 16/11/2026)
- Date: 14/11/2026
- Amends: [0008](0008-design-takes-precedence-over-version-plan.md) (the header shows the current time, not the next appointment; the leave-the-house countdown stays out of V1.0 and comes back per person in V2), [0015](0015-display-web-interface-and-everyday.md) (column limits per orientation, tomorrow block faded and off by default, footer tiles, task progress, dinner tile, meal plan on displays, package split of the footer)

## Context

The product owner reviewed the first preview of the family view (PR #58, ticket 5.1). Three parts were unclear: the task tile showed one total without saying whose tasks, the dinner tile ignored other mealtimes planned in the meal plan, and the tomorrow tile did not say what happens on days with no or several waste collections. The header lacked the current time, and the preview showed a leave-the-house countdown in the header, which ADR 0008 had already left out and which would apply to nobody in particular.

## Decision

### Header

- Left: weekday and calendar week above the date (unchanged). Right: **current time** (hh:mm) and **weather**, each in a header chip. The clock takes the chip style the spec gave the leave-the-house countdown. The header shows no countdown and no next appointment (deviation from the spec's header chips and from ADR 0008's "current time or the next appointment").
- Household settings in the web interface: **time format** 24-hour (default) or 12-hour, and **temperature unit** °C (default) or °F. The time format applies to every time shown on displays and in the web interface, not only the header clock.

### Person columns

- Each person's column shows **today's task progress** as a bar with "done/total" (e.g. "3/5"), anchored to the bottom of the column. When the column's events scroll, the bar stays visible in the same place; the 18:00 tomorrow block of ADR 0015 scrolls with the events above it. A person without tasks today shows no bar.
- **Column limits:** at most **6 person columns side by side in landscape** and **4 in portrait**; each column is at least about 150 px wide. When the people do not fit within these limits, the columns scroll sideways. On the reference E-Ink tablets (1024×768) the limits apply as stated; on small screens (an iPhone in portrait shows 2 columns) the minimum width makes them scroll earlier. This refines ADR 0015 ("up to 6 people the columns get narrower; from 7 they scroll").
- **Tomorrow block** (ADR 0015: from 18:00, each person's first three timed events of the next day, and tomorrow's whole-household events at the end of the family strip): shown **faded**, so today's events stand out. Whether it is shown at all is a setting per display, **off by default**.
- **Leave time per person in V2:** once journey times exist (V2), the person's next event with a journey time shows "leave in h:mm" in their column from 60 minutes before departure (event start minus journey time); several people can each show their own. Nothing of it is built in V1.0 (ADR 0008, handover).

### Footer

The footer has **two tiles**; the task tile of ADR 0015 is dropped, because progress is in the columns. A tile without content is hidden and the other one takes the width; with both hidden the footer is hidden. Package 5 builds both tiles empty (hidden); package 6 fills them (meal tile 6.4, tomorrow tile 6.7).

- **Meal tile** (replaces the dinner tile): shows every mealtime planned for today that is not over yet, one line each, always with the mealtime ("Lunch: Pasta bake"). Each mealtime has a time from which it is no longer shown, set per household. Defaults: breakfast 10:00, lunch 13:30, snack 16:30, dinner none (shown for the rest of the day). With one line the tile is labelled with the mealtime, with several "Meals today".
- **Tomorrow tile:** one line per waste collection tomorrow (e.g. "Paper bin", "Residual waste"), shown all day, and the parent's note for tomorrow as the last line. The note keeps the timing of ADR 0015: shown from 18:00 the evening before its day until the end of that day, then deleted. At most three lines are shown: with more, all collection titles share one line, joined by ", ", and the note is never hidden (see "Tomorrow tile as built"). The tile is highlighted from 18:00 when it holds a collection (the waste collection reminder); a tile with only a note is not highlighted. Hidden when there is neither a collection nor a note.

### Column width (ticket 5.7, proposed, pending the product owner's approval of preview-27)

Proposed: the minimum column width is fixed at **144 px** (design token `column-min`), the "about 150 px" above rounded down so that six columns still fit the 1024 px reference display (968 px inside the margins). It applies to landscape as well as portrait. Landscape and portrait follow the viewport (`orientation`), so a rotated display switches by itself; there is no per-view scroll flag. The view counts 6 columns in landscape and 4 in portrait before it scrolls: 5 people on a 768x1024 portrait display show 4 columns, 4 people on a 375 px wide phone in portrait show 2.

### Tomorrow tile as built (ticket 6.7)

- A note is a **day note**: one per day, up to 120 characters, set by a parent in the web interface (card "Note on the display" on Home) or the API (`/day-notes`). Before 18:00 the tile shows the note of today, from 18:00 the note of tomorrow replaces it. Notes of past days are deleted when a display renders, when Home is shown and when the API lists the notes (`GET /day-notes`).
- Identical collection titles from several waste collection calendars are one line. Only read-only calendars can be marked as waste collection; their events stay out of the strip, columns and personal view.
- Of "at most three lines": the room is three lines minus one when there is a note. Collections that fit in the room keep one line each; when they do not, **all collection titles share one line, joined by ", "** and the note stays the last line. Nothing is cut off and no count ("+N more") replaces a title; lines wrap.
- The tile says what to do: from 18:00 with a collection the highlighted tile shows the pill "Put out tonight" next to its label (black on black-and-white displays), so the highlight does not rely on colour alone.
- Each collection line starts with a bin icon next to the word; the note is set apart below a thin rule in regular weight.
- Today's note: "Tomorrow" never heads today's note. Before 18:00 the tile shows today's note; next to a collection it starts with a bold "Today", and when the tile holds only today's note, its label is "Today".
- The day note lives on Home, not on the calendars page. An empty note is a field error (it never removes a note); a planned note opens on its own page, where it can be changed or removed after a confirmation. The waste collection is a switch on the calendar card with its state in words, and the card names the next collection (or says there is none in the next 30 days).

## Consequences

- Tickets change: 5.3 (anchored progress bar, two-tile footer), 5.8 (header clock and weather, time format and temperature unit; renamed "Weather and time in the header"), 6.3 (meal cut-off times), 6.4 (renamed "Meal tile on the display"), 6.6 (progress in the columns instead of a tile), 6.7 (tomorrow tile states).
- The header clock changes once a minute, so E-Ink displays refresh the clock chip once a minute (a partial update, no full-page reload).
- The preview of 5.1 shows the new header, the anchored progress bars and the footer states (tiles hidden, one collection, two collections with a note).
- Display settings gain "show tomorrow block" (off by default); ticket 5.3 renders it faded.
- Household settings gain time format, temperature unit and four meal cut-off times (dinner empty by default).
- The box finds the coordinates of the home once, by the home's name and country, with Open-Meteo's place search (no key) and keeps them in the household; it refreshes the weather every 15 minutes. Open-Meteo is the only outside service the weather feature talks to. When a refresh fails the chip keeps its last reading for three hours, then hides.

## Amendment of 15/11/2026 (ticket 6.6, PR #127)

The product owner approved a deviation from the person-column decision above on 15/11/2026. It replaces the progress bar with "done/total" in "Person columns".

- **Progress in words:** routine and task progress in a person's column is shown in words (e.g. "1 of 3 tasks done") instead of a done/total bar. A person without tasks today still shows nothing. The block stays anchored to the bottom of the column.
- **Block opens the personal view:** tapping a person's block opens their personal view.
- **Tasks first:** the personal view shows the person's tasks first.

Everything else in this ADR is unchanged.

## Amendment of 16/11/2026 (ticket 6.2, lists on the display)

The product owner approved the way into the lists on 16/11/2026 (preview-33).

- **Header right: Lists button, time, weather.** The family view header gets a **Lists button** (list line icon and the word "Lists", with an accent border so it reads as a button, not as a status chip) before the clock. It opens the first list in the household's order; the display does not remember a choice. It is hidden when the household has no list.
- **The footer keeps two tiles:** no third footer tile for lists, because the columns need the height. Tiles on the display come with #166.
- The button never uses a person marker, so lists stay apart from the person columns.

Everything else in this ADR is unchanged.
