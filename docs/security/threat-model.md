# Threat model

Status: 0.1, written for ticket #97 (ADR 0018, Measurement). No external penetration test happens before 0.1; this document and the CI checks below are the security work for it.

## Scope and assumptions

- **What is protected:** the family's calendar, tasks, stars and the credentials the box holds (password hashes, TOTP secrets, device tokens, the DNS token, calendar connection credentials).
- **Where it runs:** one box (Raspberry Pi or any Linux with Docker) on a home network. It is **not** exposed to the internet: no port forwarding, no cloud relay. Anyone who can reach its ports is on the family's LAN or Wi-Fi.
- **Who attacks:** a guest or a compromised device on the LAN; a thief with a phone or display; someone who finds the box's public DNS name; a malicious dependency or image; a mistake in an update. Not in scope: someone with root on the box or on a parent's unlocked phone.
- **Trust boundary:** the box's HTTPS ports. Everything behind them (SQLite file, `secret.key`, the data volume) is trusted; whoever can read the data volume owns the family's data.

How to read this: every threat has a **Check** (a CI job, a test, or a ticket that builds it) or an **Accepted risk** with the reason. A test (`internal/securitydocs`) fails if a threat has neither, if a named CI job does not exist, or if a named test file does not exist. Checks written as `#NN` are not built yet; the ticket owns them.

## CI checks and thresholds

| Check | Job | Fails the build on |
| --- | --- | --- |
| Known vulnerabilities in called dependencies | `ci:govulncheck` (govulncheck, BSD-3-Clause) | any vulnerability whose code the box calls |
| Static analysis | `ci:gosec` (gosec, Apache-2.0) | findings of **high** severity |
| Container image scan | `ci:image-scan` (Trivy, Apache-2.0) | **fixable** HIGH or CRITICAL findings in the image |

All three run only in CI; none is part of the binary. Unfixed (no patch exists) image findings do not fail the build, because nobody can act on them; they show up again when a fix ships.

### gosec findings of medium severity (triaged, not failing)

`gosec -severity medium ./...` reports the following. Each was read and judged; none is a vulnerability today. A new medium finding is not blocked by CI, so reviewers read the gosec output of a PR that touches these areas.

| Rule | Where | Judgement |
| --- | --- | --- |
| G124 cookie attributes | session and display cookies | `Secure` is set whenever the request came over TLS. Plain HTTP exists only for the setup wizard before HTTPS is configured (ADR 0014). `HttpOnly` and `SameSite=Lax` are always set. |
| G705, G203 XSS | family view, setup, display pairing | Markup is produced by `html/template` (auto-escaped) or by the project's own QR encoder; `template.HTML` wraps only that output. The SSE stream writes a hash. |
| G710 open redirect | login, HTTP-to-HTTPS | The login `next` goes through `localPath` (local paths only, tested in `login_test.go`); the HTTPS redirect uses the box's own origin. |
| G202 SQL concatenation | store (audience tables) | Only fixed table and column names are concatenated; every value is a bound parameter. |
| G304 file inclusion | `secret` package | The path is operator configuration (`HEJMO_SECRET_KEY_FILE`), not request input. |
| G505 SHA-1 | TOTP | RFC 6238 authenticator apps use HMAC-SHA-1; HMAC-SHA-1 is not weakened by SHA-1 collisions. |
| G204, G302, G306 | Radicale test helper | Test-only code that starts a container; never in the binary. |

Two **high** findings (G115, integer conversions in `internal/auth`) were fixed in this ticket: a stored password hash that asks for absurd Argon2 parameters or a key length outside 16 to 64 bytes is now refused instead of computed, and TOTP refuses times before 1970.

## Home network

### T1. Someone on the LAN reads or alters traffic

- **Threat:** a guest on the Wi-Fi sniffs passwords, one-time codes, device tokens or calendar data, or injects pages.
- **Check:** HTTPS is required for everything the box serves; port 80 only redirects, except the trust page the own CA needs (ADR 0014). `test:internal/server/https_test.go`
- **Accepted risk:** the CA fingerprint on the trust page is served over HTTP, so it protects against mistakes, not against an attacker already on the network (ADR 0014). It is also logged at creation for a stronger comparison.

### T2. A guest guesses a password or one-time code

- **Threat:** online guessing against the sign-in page.
- **Check:** passwords are Argon2id; sign-in needs password and one-time code; failed attempts are rate-limited per client. `test:internal/server/login_test.go`

### T3. A device on the LAN reaches ports it should not

- **Threat:** other devices can connect to the box's ports and probe it.
- **Accepted risk:** the LAN is the trust boundary; the box only publishes the HTTP and HTTPS ports and every function behind them needs a session, device token or paired display. Segmenting the network (guest Wi-Fi) is the family's router's job and is recommended in the install guide (#100).

### T4. The data volume is copied (stolen SD card, backup in the wrong place)

- **Threat:** a copy of the data volume exposes the family's data.
- **Check:** secrets the box must read back (DNS token, calendar connection credentials) are sealed with AES-256-GCM, and the key is a separate file that can live outside the database and its backups. `test:internal/secret/secret_test.go`
- **Accepted risk:** the SQLite file itself (events, tasks, names) is not encrypted at rest. A database key would have to sit next to it on a box that restarts unattended, so it would add little; full-disk encryption of the host is the family's choice.

## Displays

### T5. A stranger pairs their own screen or phone, or guesses a pairing code

- **Threat:** a display or phone shows the family's day to whoever completes pairing.
- **Check:** pairing needs a short code confirmed by a signed-in parent; codes expire; guessing codes and opening many pairing screens are rate-limited. `test:internal/server/displays_test.go`
- **Check:** a phone's pairing code is random (128 bits, stored only as a hash), works for one phone and ten minutes, and the phone only gets its device session after a signed-in parent confirmed it and chose the person; a code seen over a shoulder is useless once used, expired or unconfirmed. `test:internal/server/phones_test.go`
- **Check:** with the box's own CA the pairing code travels over plain HTTP (the trust page), so someone on the home network could open it before the real phone. The waiting phone and the phones screen therefore both show a six-digit number derived from the phone's secret; a parent confirms only a phone whose number matches the one in hand, and a second phone cannot take over a code that was opened. `test:internal/server/phones_test.go`
- **Accepted risk:** a parent who confirms a waiting phone without comparing the number gives that phone a session; the screen asks for the comparison but cannot enforce it.

### T6. A lost or stolen display keeps access

- **Threat:** a display kept by a thief keeps showing the family's day.
- **Check:** a parent revokes a display; it falls back to the pairing screen within a minute. `test:internal/server/displays_test.go`
- **Accepted risk:** a display shows the family view only (titles and times), never settings, tokens or other people's private data; this is the purpose of a wall display.

### T7. Hostile text in a title runs as script on a display or phone

- **Threat:** an event title or task name from a synced calendar contains markup.
- **Check:** every page is rendered by `html/template`, which escapes by context; tests render a `<script>` title on the family view, the display page and the tasks page and require it escaped. `test:internal/server/escaping_test.go` (gosec's XSS rules are medium severity and do not fail CI; the triage above covers the places that wrap markup deliberately.)
- **Accepted risk:** the box sets no Content-Security-Policy or clickjacking headers yet. Pages are only reachable on the LAN over HTTPS and templates escape, so exploitation needs a second flaw. Adding the headers is a follow-up for the product owner to schedule.

## Phones

### T8. A lost phone keeps a session or a CalDAV token

- **Threat:** a thief opens the web interface or syncs the calendar.
- **Check:** device tokens are stored as hashes and can be revoked, after which they are refused. `test:internal/server/caldav_test.go`
- **Check:** a paired phone's device session is listed on the phones screen and removed by a parent, after which the phone is signed out at its next request. `test:internal/server/phones_test.go`
- **Check:** the service worker deletes the offline copy of the pages when the person signs in again or the box redirects to the sign-in page, and keeps only styles and scripts, which hold nothing personal. `test:web/test/sw.test.js`
- **Check:** parent A ends parent B's browser session under Signed-in devices, after which B's next request is refused and lands on the sign-in page, which clears the cookie; a paired phone's session cannot be ended there, and a parent cannot end their own. `test:internal/server/sessions_test.go`
- **Accepted risk:** a browser session lasts 30 days. A lost phone keeps its session until a parent ends it (the box does not know a phone is lost) or it expires; the phone's own lock screen is the protection meanwhile. Sessions made before #108 show as an unknown device with no last use, and a parent finds them by person and sign-in date.

### T9. A child's account or token reaches parent functions

- **Threat:** a child's token or session is used for settings, tokens or other people's data.
- **Check:** tokens of a child are refused; only calendar scopes open CalDAV. `test:internal/server/caldav_test.go`

### T10. A bad certificate profile is installed on a phone

- **Threat:** the own-CA fallback asks parents to install a CA; a CA that could vouch for any site would be dangerous.
- **Check:** the CA is name-constrained to the box's names, so it cannot vouch for other sites. `test:internal/pki/pki_test.go`
- **Accepted risk:** a two-label LAN name such as `fritz.box` widens the constraint to everything under it (ADR 0014); the wizard prefers the IP address or `hejmo.local`.

## DNS records at deSEC

### T11. The deSEC token is stolen

- **Threat:** the token can change every record of the family's zone.
- **Check:** the token is sealed at rest (AES-256-GCM) and is not readable in the database file; the key is a separate file. `test:internal/publiccert/publiccert_test.go`
- **Accepted risk:** deSEC tokens are not scoped to one record. With the free `dedyn.io` name the zone holds only the box's record, so the damage of a stolen token is limited to that name; families using an own domain should use a zone with no other records. The install guide says so (#100).

### T12. The public name reveals the box's private address

- **Threat:** anyone can look up the A record and learn the LAN address.
- **Accepted risk:** the address is a private one (for example 192.168.x.x) that is not routable from the internet; it tells an outsider nothing they can reach. This is the price of a publicly trusted certificate without port forwarding (ADR 0014).

### T13. The name or the certificate authority is unavailable or taken over

- **Threat:** deSEC or the CA is down, a record is changed, or renewal fails and the certificate lapses.
- **Check:** the box never falls back to plain HTTP; it serves the stored certificate or its own CA's certificate, warns parents, and keeps renewing. `test:internal/server/public_https_test.go`
- **Accepted risk:** if an attacker controls the DNS record, phones are pointed at the attacker's machine, but the sign-in password never leaves the phone without a valid certificate for that name, which the attacker cannot obtain without the DNS token (T11).

### T14. A dependency or the base image has a known vulnerability

- **Threat:** a Go module, the Go toolchain or the distroless base image carries a published vulnerability.
- **Check:** `ci:govulncheck` fails on called vulnerabilities, `ci:image-scan` on fixable HIGH and CRITICAL findings in the image.

## Auto-update script

The host-side script (`hejmo-update`, installed by `install.sh`) runs as root once a night; signed images come from the release workflow (#99). Its tests run it as a black box against a stand-in Docker.

### T15. A tampered or unpublished image is installed

- **Threat:** the script pulls an image that was not released by the project, or a preview build.
- **Check:** only images with a valid keyless cosign signature from the release workflow, only release tags without a pre-release suffix, previous image recorded by digest. `test:internal/install/update_test.go` and `test:internal/release/workflows_test.go`

### T16. The box feeds the host script a malicious setting

- **Threat:** a compromised box writes the status file the root-run script reads, and gets code executed on the host.
- **Check:** the file has fixed keys and values; the script accepts nothing else and never runs or interpolates its content; the container gets no Docker socket. A settings file that is a link, oversized or doubtful means "notify", never install; the status file is replaced, never written through a link. `test:internal/install/update_test.go`, `test:internal/update/update_test.go`

### T17. An update loses or corrupts the database

- **Threat:** a bad release leaves the family without calendar and tasks.
- **Check:** online backup before the update, wait for `/healthz`, return to the previous image and database on failure, notice to parents. `test:internal/install/update_test.go`

### T18. The update script itself is replaced

- **Threat:** auto-update becomes a way to change the host script.
- **Check:** the script is updated only by re-running the install script, never by auto-update. `test:internal/install/update_test.go`

## Accounts

### T22. An invitation link is guessed, intercepted or reused

- **Threat:** someone other than the invited adult opens an invitation link and creates an account that can see and change everything a parent can.
- **Check:** the link holds 20 random characters (about 99 bits) and is stored only as a hash, so the box cannot show it again; it works once and for 7 days, a new link withdraws the old one, and a parent can withdraw it. A used, expired or withdrawn link never creates an account, also when two requests race. `test:internal/store/invitations_test.go` `test:internal/server/invite_test.go`
- **Check:** unknown or cut-off links answer with one page that names nobody and no date, and wrong tries per address are rate-limited like sign-ins; every try at the account form counts too. `test:internal/server/invite_test.go`
- **Check:** the invited person chooses username and password on their own device, so no password passes through another person; the password is hashed with Argon2id and is never sent back to the browser. A parent can reset the new parent's one-time codes (ADR 0017). `test:internal/server/invite_test.go`
- **Check:** only adults without an account can be invited; children keep device sessions (ADR 0010). Creating, withdrawing and accepting are logged with the names of the people. `test:internal/store/invitations_test.go` `test:internal/server/invite_api_test.go`
- **Accepted risk:** whoever holds the link before the invited person can create the account, and the link travels over a chat or over the QR code on the parent's screen. The page tells the parent to send it only to that person, and the parent sees on the invitation page when it was used; a link used by the wrong person shows as "used". **There is no remedy yet:** a parent cannot remove a person or an account, and resetting the one-time codes (ADR 0017) keeps the password and leads the next password sign-in into code setup, so the holder of the account stays a full parent. The invitation page shows no reset link while the one-time code is not yet set up. Open for the product owner: a way for a parent to remove an account created through an invitation (own ticket). With the box's own CA the short address and the trust page are plain HTTP (like phone pairing, T5): the token is visible to someone who can read the home network's traffic until the invited device trusts the box; the account form itself is HTTPS only.

### T23. A passkey signs in someone who should not be in, or outlives a lost device

- **Threat:** someone creates a passkey for a parent's account (with a stolen device token or browser session) and signs in for good; or a passkey on a lost device keeps signing in after the parent's one-time codes were reset; or a passkey is used on a look-alike address.
- **Check:** a passkey counts as password and one-time code together only because the box demands that the authenticator verified the user (fingerprint, face or PIN); a passkey whose authenticator did not is refused when it is created. `test:internal/passkey/passkey_test.go`
- **Check:** passkeys are created and used only when the box is reached over HTTPS by its box name (the public name, or `hejmo.local` with the own CA), never by IP address; the relying-party ID is the box name and the origin is checked, so an answer made on another site or address is refused. `test:internal/server/passkeys_test.go`
- **Check:** only a signed-in browser creates a passkey; the REST API refuses a device token there (403), because a passkey signs in for good and a stolen token must not be able to make one. A creation works once, for two minutes, and only for the person who began it. `test:internal/server/passkeys_test.go`
- **Check:** a parent removes their own passkeys at once, and a revoked passkey cannot sign in. When another parent resets one-time codes, the page lists that parent's passkeys by name and removes the ticked ones in the same step; none is ticked in advance. A passkey made for an earlier box name is shown as no longer working and signs nobody in. `test:internal/server/passkeys_test.go`
- **Check:** refused passkey sign-ins are rate-limited per client address; the counter of a passkey is stored and a passkey whose counter went back is refused (cloned). A sign-in after a reset still leads only to setting up new one-time codes. `test:internal/server/passkeys_test.go`
- **Accepted risk:** a browser session that was stolen (T8) can create a passkey, because the page asks for no password first (design approved with the preview of #95). The parent sees the passkey in "My account" and removes it there; signing out everywhere is under "Signed-in devices". The browser keeps one flag cookie, `hejmo_passkey_used`, that says a passkey was used on this device; it holds no name and no secret.

## Article library

### T24. A web address makes the box ask something inside the home network (server-side request forgery)

- **Threat:** an own article can take a picture from a web address (ADR 0019). Whoever types an address, or a token that may write lists, makes the box send a request. An address such as `http://192.168.178.1/`, `http://localhost:8080/`, a cloud metadata address (`169.254.169.254`), a name that points inside, a redirect from a public site to the inside, or a name that answers a public address once and a private one the next time would let the box read or poke the router, other boxes or its own services.
- **Check:** the box connects only to public addresses. The check sits where the connection is made, on the address that is really used, so it holds however the address is written (decimal, hexadecimal, shortened, IPv4 inside IPv6, 6to4, NAT64), for names that point inside, for every redirect, and against a name that changes its answer between checking and connecting. Loopback, private, link-local, shared (100.64.0.0/10), documentation, multicast, reserved and unspecified ranges are refused, and the refusal names the address at the field. `test:internal/picture/fetch_test.go` `test:internal/server/library_web_test.go` `test:internal/server/library_api_test.go`
- **Check:** only http and https, no user name or password in the address, at most 5 redirects, no environment proxy (it would bypass the check), 10 seconds in all, at most 15 MB read (declared size and while streaming), and at most 40 million pixels before decoding. Nothing is sent but a plain GET with the product name: no cookie, no referrer, no credentials. `test:internal/picture/fetch_test.go`
- **Check:** the answer must be a JPEG, PNG, WebP or GIF; it is decoded and written again as JPEG, scaled to at most 512 px, so no active content (SVG, HTML), no metadata (place, camera) and no trailing data survives, and the box never links to the address afterwards. A web page where a picture was expected is named as such. `test:internal/picture/picture_test.go`
- **Check:** only parents add or change articles; a token needs the scope `lists:write`. Uploaded photos take the same road and need no network at all. `test:internal/server/library_api_test.go`
- **Accepted risk:** the box can be made to fetch any public address, so a parent (or a token with `lists:write`) can make it send one request to a public site, which then sees the box's public address. The request carries nothing else.

## MCP

The MCP server is built in #96.

### T19. An MCP client gets more access than intended

- **Threat:** an AI tool connected to the box reads or changes data beyond what a parent meant.
- **Check:** off by default; only API tokens created for MCP work; token scopes are enforced unchanged; tokens of a child are refused; managing tokens and settings is never a tool. #96

### T20. MCP traffic is read or altered on the LAN

- **Threat:** token and family data cross the network in clear.
- **Check:** MCP is served only over HTTPS. #96

### T21. Hostile text in an event steers the connected AI tool

- **Threat:** an event description from a shared or imported calendar contains instructions aimed at the AI tool that reads it (prompt injection).
- **Accepted risk:** the box cannot tell data from instructions for the client's model. The damage is bounded by the token's scopes (T19): a parent who gives a tool write access to tasks accepts that it can change tasks. The install guide recommends read-only scopes for first use (#100).
