# 0016. Family view header, columns and footer

- Status: Accepted
- Date: 14/11/2026
- Amends: [0015](0015-display-web-interface-and-everyday.md) (footer tiles, task progress, dinner tile), and the "Nordic Calm" layout in `docs/spec.md` (header chips, footer)

## Context

The product owner reviewed the first preview of the family view (PR #58, ticket 5.1). Three parts were unclear: the task tile showed one total without saying whose tasks, the dinner tile ignored other mealtimes planned in the meal plan, and the tomorrow tile did not say what happens on days with no or several waste collections. The header lacked the current time, and the "leave the house" countdown in the header applied to nobody in particular.

## Decision

### Header

- Left: weekday and calendar week above the date (unchanged). Right: **current time** (hh:mm) and **weather**. The header no longer shows a leave-the-house countdown.
- Household settings in the web interface: **time format** 24-hour (default) or 12-hour, and **temperature unit** °C (default) or °F. The time format applies to every time shown on displays and in the web interface, not only the header clock.

### Person columns

- Each person's column shows **today's task progress** as a bar with "done/total" (e.g. "3/5"), anchored to the bottom of the column. When the column's events scroll, the bar stays visible in the same place. A person without tasks today shows no bar.
- **Leave time per person:** the person's next event with a journey time shows "leave in h:mm" from 60 minutes before departure. Several people can each show their own. (Spec: one leave-the-house chip in the header.)

### Footer

The footer has **two tiles**; the task tile of ADR 0015 is dropped, because progress is in the columns. A tile without content is hidden and the other one takes the width; with both hidden the footer is hidden.

- **Meal tile** (replaces the dinner tile): shows every mealtime planned for today that is not over yet, one line each ("Lunch: Pasta bake"). Each mealtime has a time from which it is no longer shown, set per household: breakfast 10:00, lunch 13:30, snack 16:30, dinner until quiet mode starts (defaults). With one line the tile is labelled with the mealtime, with several "Meals today".
- **Tomorrow tile:** one line per waste collection tomorrow (e.g. "Paper bin", "Residual waste") and the parent's note for tomorrow below; from four lines on, the rest shows as "+N more". Highlighted from 18:00. Hidden when there is neither a collection nor a note.

## Consequences

- Tickets change: 5.3 (columns, footer, header clock), 5.8 (header without countdown, leave time in the column, time format and temperature unit), 6.3 (meal cut-off times), 6.4 (meal tile), 6.6 (progress in the columns instead of a tile), 6.7 (tomorrow tile states).
- The preview of 5.1 shows the new header, the anchored progress bars and the footer states (tiles hidden, one collection, two collections with a note).
- Household settings gain time format, temperature unit and four meal cut-off times.
