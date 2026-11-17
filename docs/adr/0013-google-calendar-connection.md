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

## Notes added with the implementation (#18)

- **The state** sent to Google is `hj1.<box address, base64url>.<token>`. The box address is what lets the static relay page go back to the right box (the page knows no one else); the token is 16 random bytes and the expiry, sealed with the box's key (AES-256-GCM, so a value the box did not make, or one that was changed, does not open). Its nonce is a row of `google_login`, together with the PKCE verifier (sealed), the redirect URI and the connection that signs in again: a state works once, for 15 minutes, in the household that made it. "Unknown", "expired" and "already used" are three different answers to the parent. Nothing is created when the page "Sign in at Google" is shown, only when "Open Google" is pressed.
- **Which way** is chosen by how the browser reached the box: the relay way over `https://` (any host) or over `http://` to a private IP address (the same rule the relay page applies), else the fallback straight away, with a link to the box's address in the home network when the connection reveals one. The relay page forwards `code`, `state`, `error` and `scope` (the box checks `scope` for the calendar tick before it exchanges the code).
- **The account** of a connection is the id of the primary entry of the calendar list. A sign-in for a connection with another account is not thrown away: its refresh token is kept sealed with the used login and offered once as a new connection. A sign-in with an account that already has a connection renews that connection.
- **The refresh token** (sealed in `calendar_connections.credential`) gets an access token before every sync, kept in memory until a minute before it ends. Google's CalDAV API is `https://apidata.googleusercontent.com/caldav/v2/<calendar id>/events` with the bearer token (`caldavclient.NewBearerClient`: the token goes only to the server's own domain, like a password); sync, delta sync and write-back are those of CalDAV (ADR 0012). `invalid_grant` marks the connection (`signin_needed_at`, set once, kept until a new sign-in) and writes a failure entry per calendar to the sync log; while a connection waits for a new sign-in Google is not asked again, and queued box changes stay queued. `invalid_client` (client ID or secret refused) is a failure of the calendars but not a dead sign-in.
- **Access** comes from the `accessRole` of the calendar list: an owner or writer calendar is two-way (and a parent can make it read-only), a reader's calendar is added read-only and can never be switched to two-way (`calendars.source_read_only`); Google's birthday and holiday calendars are read-only. Google's own colours are not taken over.
- **One app registration per household** (`google_app`): the client secret is sealed like every credential and never shown again. The same client ID with a new secret keeps every sign-in; another client ID marks every Google connection "sign in again".
- **Interval:** Google calendars sync every 5 minutes like other CalDAV calendars (ADR 0012), configurable per calendar.
- **Tests** use a fake Google (token service, calendar list) and a fake CalDAV API only. The one manual check with a real Google account stays with the product owner before merge (part C of the guide, results in #44): the github.io domain at Google, "In production" without verification, the fallback.
