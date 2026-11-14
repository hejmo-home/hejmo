# 0008. Design takes precedence over the version plan

- Status: Accepted
- Date: 14/11/2026

## Context

The "Nordic Calm" family view shows one column per person, a weather chip and a leave-the-house countdown. The version plan puts "column per person" in V2, the morning countdown in V3, and does not list weather at all.

## Decision

- The design is binding for V1.0.
- **One column per person** and **weather at the home location** (Open-Meteo, no API key) are part of V1.0. Weather per event stays in V2.
- The **leave-the-house countdown** needs travel times (V2) and stays out. Until then the header chip shows the current time or the next appointment.

## Consequences

- Package 5 implements the family view as designed, minus the countdown.
