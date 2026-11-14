# 0013. Google Calendar connection

- Status: Accepted
- Date: 14/11/2026
- Amends: [0007](0007-calendar-sync-in-v1.md)

## Context

ADR 0007 decided on a per-install Google OAuth client. Research on 14/11/2026 against Google's documentation found:

- Redirect URIs must be HTTPS with a public domain; raw IPs are not allowed except loopback (`127.0.0.1`, `localhost`). A box at `192.168.x.x` cannot be a redirect target.
- The device flow ("TVs and limited input devices") does not allow any Calendar scope.
- The out-of-band copy/paste flow was shut down in January 2023.
- Apps left in "Testing" status get refresh tokens that expire after 7 days. Apps "In production" for personal use (under 100 users) need no verification but show an "unverified app" warning.
- Google offers a documented CalDAV API (OAuth only) with `sync-collection`, CTag and `If-Match`.
- Home Assistant solves the redirect with a static relay page (`my.home-assistant.io`).

## Decision

- **Events sync over Google's CalDAV API**, using the same sync engine as iCloud and other CalDAV servers (ADR 0012). The calendar list comes from one REST call (`calendarList`) with the same token. Google's own event colours are not synced.
- Each family creates **one OAuth client of type "Web application"** with two redirect URIs:
  - `https://hejmo-home.github.io/oauth/`: a **static relay page** in its own repository `hejmo-home/hejmo-home.github.io`. It forwards `code`, `state` and `error` with `location.replace()` to the box's callback.
  - `http://127.0.0.1:<fixed port>/`: **fallback**. The browser shows "cannot connect"; the parent copies the address from the address bar into the box.
- **The relay page** has no third-party code, a strict CSP and `no-referrer`. It forwards only to the path `/connections/google/callback`: on any host over `https`, over `http` only to private IP addresses. The installation guide states that every Hejmo installation may use it (Google requires redirect domains that the app owner owns or may use). Changes to it need the same review as the main repository.
- **Security rests on the box**, not the page: PKCE (S256), and a `state` signed by the box that is short-lived and single-use. A pasted fallback URL is accepted only with a matching `state`.
- The setup guide walks the family to publishing status **"In production" without verification** and explains the warning screen with screenshots. Both the CalDAV API and the Calendar API must be enabled.
- If Google rejects the refresh token (`invalid_grant`), the connection is marked **"sign in again"**, logged in the sync log and shown to parents in the web interface.

## Consequences

- The project hosts one static page on GitHub Pages; it runs no server of its own.
- Whether Google accepts `hejmo-home.github.io` as an authorised domain is not documented. The product owner tests it once with a real Google account before the Google ticket is merged, together with "In production" and the fallback. If Google refuses the domain, the copy-paste fallback becomes the default and this ADR is updated.
- The copy-paste fallback is undocumented by Google and may stop working.
