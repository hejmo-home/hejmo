# 0007. Calendar sync in V1.0

- Status: Accepted, amended by [0013](0013-google-calendar-connection.md), [0018](0018-path-to-0-1.md)
- Date: 14/11/2026

## Context

The founding family uses Google Calendar and iCloud. Google Calendar requires OAuth; a central OAuth client would need Google verification and a redirect service run by the project, which contradicts "no mandatory cloud".

## Decision

- V1.0 supports **ICS feeds (read-only)**, **iCloud via CalDAV** (app-specific password) and **Google Calendar via OAuth**.
- Each installation uses **its own Google OAuth client**. The setup wizard guides the family through creating it in the Google Cloud Console.
- Outlook follows in V1.x with the same per-install pattern.
- Generic CalDAV servers (Nextcloud, Proton, Fastmail) come for free with the CalDAV implementation.

## Consequences

- Google setup takes about 10 minutes and shows an "unverified app" warning; the install guide must explain this.
- The project runs no servers of its own.
