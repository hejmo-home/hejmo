# 0011. Calendar model

- Status: Accepted
- Date: 14/11/2026

## Context

Work package 3 adds events, series, holidays, search and subgroups. Package 4 will sync with iCalendar-based services, so the model should translate to iCalendar without loss.

## Decision

- **Local wall-clock time plus time zone** per event (like iCalendar `DTSTART;TZID=`). A weekly 08:00 event stays at 08:00 across daylight-saving changes. All-day events store dates with an exclusive end. Time zone data is embedded in the binary.
- **Series** use RFC 5545 `RRULE` values, expanded with `github.com/teambition/rrule-go` (MIT, a port of python-dateutil). Cancelled occurrences are `EXDATE`s; changed occurrences are separate events with `series_id` and `recurrence_id` (like `RECURRENCE-ID`).
- **Editing scopes:**
  - *this*: creates or updates a changed occurrence.
  - *this and following*: ends the old series before the occurrence (`UNTIL`, or splits a `COUNT`) and starts a new series. Later changed occurrences and cancellations move to the new series, shifted with it.
  - *all*: changes the master; a time change shifts the whole series and its changed occurrences.
- **Audience:** an event and a whole calendar can be meant for people and/or subgroups. A person's view shows an event only if both the calendar's and the event's audience (when set) include the person. No audience = whole family.
- **Holidays** are computed in the box per region (ISO 3166-2), from rules researched against official sources on 14/11/2026 (state and cantonal laws, SECO list for Switzerland):
  - Statutory, region-wide holidays count, including Swiss cantonal rest days that are not Sunday-equivalent under the Labour Act.
  - Holidays in **most** municipalities of a region are shown with `partial = true` (15 August in Bavaria: 1,708 of 2,056 municipalities).
  - Holidays in only a few places are left out (Augsburg's peace festival, Corpus Christi in parts of Saxony and Thuringia, district-based holidays in Aargau and Fribourg), as are half days (1 May afternoon in Solothurn).
  - Conditional and one-off rules are supported (Neuchâtel's 2 January and 26 December only on Mondays; Berlin's 17 June 2028).
- **Search** uses SQLite FTS5 with prefix matching, case- and accent-insensitive.
- **Week view logic** (multi-day bars with lanes) lives in the domain package; rendering follows with the display in package 5.

## Consequences

- Package 4 maps events 1:1 to `VEVENT` with `RRULE`, `EXDATE` and `RECURRENCE-ID`.
- An `extra` JSON column is reserved for event types and X-properties (V2).
