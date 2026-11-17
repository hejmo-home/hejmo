# 0014. HTTPS and certificates

- Status: Accepted, amended by [0024](0024-offline-mode-and-the-one-door-to-the-outside.md)
- Date: 14/11/2026

## Context

With the box as a CalDAV server in V1.0 (ADR 0012), phones send credentials to the box. Plain HTTP on the home network is not acceptable. Research on 14/11/2026:

- No public certificate authority may issue certificates for private IPs, `.local` or `.home.arpa` (CA/Browser Forum, since 2015). Let's Encrypt IP certificates need a publicly reachable IP.
- A LAN-only box can still get a publicly trusted certificate through a **public DNS name pointing to its private IP** and the **DNS-01** challenge, without port forwarding.
- deSEC (non-profit, Berlin) offers free `dedyn.io` names with an API.
- Free ACME CAs: Let's Encrypt (USA, non-profit, no account). EU options are few: Actalis (Italy, free, one name per certificate, account required); ZeroSSL (Vienna, owned by HID Global); Buypass ended its service in October 2025; HARICA and Certum offer no free public ACME.
- Routers such as the FRITZ!Box block DNS answers with private IPs by default (DNS rebind protection) unless the name is added as an exception.
- A self-made CA works well on iOS (profile) and in DAVx5 (prompt), poorly in other Android apps.

## Decision

- **HTTPS is required in V1.0** for everything the box serves: web interface, API, CalDAV, displays. Port 80 only redirects to 443; both ports are configurable in Compose.
- **Default:** a free **deSEC `dedyn.io` name**, or the family's own domain at a DNS provider with an API (e.g. Hetzner, INWX, Cloudflare, deSEC). The box sets the name to its LAN IP and obtains the certificate through DNS-01. The DNS token is encrypted like other credentials (ADR 0012).
- **Certificate authority:** Let's Encrypt by default; Actalis selectable in the setup wizard for EU jurisdiction. ZeroSSL as automatic fallback issuer.
- **Fallback without outside services:** the box creates its **own CA**, name-constrained to the box, so it cannot vouch for other sites. iOS gets a downloadable profile, DAVx5 a one-time confirmation, displays trust it on pairing (package 5).
- **Setup wizard order:** the wizard starts over HTTP on the LAN, then the HTTPS step is mandatory (deSEC/own domain or own CA). The first parent password is set only after HTTPS works. The wizard checks from the browser that the name resolves and shows step-by-step help for the FRITZ!Box rebind exception if it does not.
- If a certificate cannot be renewed, parents are warned like a failing sync (ADR 0012).
- **Libraries (make or buy):** `github.com/caddyserver/certmagic` (Apache-2.0) with `github.com/libdns/desec` (MIT) and other libdns providers. Rejected: `golang.org/x/crypto/acme/autocert` (no DNS-01), `go-acme/lego` (fine, but CertMagic also handles renewal, several issuers and storage), building it ourselves.

## Consequences

- The default setup depends on deSEC and a CA being reachable; the own-CA fallback keeps "no mandatory cloud" (ADR 0004, 0007).
- The Google relay page (ADR 0013) must accept `https` callbacks on any host, because the box's address is now a public name.
- The setup order in ADR 0010 ("setup is complete once the first parent has confirmed TOTP") gains the HTTPS step before the password.
- Let's Encrypt is shortening certificate lifetimes to 45 days by 2028; automatic renewal covers that.
- Passkeys stay in V1.x; they only need HTTPS, which now exists.

## Notes on the own-CA fallback (added in review of PR #45)

- Port 80 redirects everything except `/healthz` and the trust page with its downloads (`/ca`, `/ca/hejmo-ca.crt`, `/ca/hejmo.mobileconfig`), which a phone needs before it trusts the box.
- Port 80 also serves the static files those pages need to render: the stylesheet (`/static/hejmo.css`), the font (`/static/fonts/`) and the icons (`/static/icons/`). Threat model: these files are public, identical on every box and carry no household data, so serving them in the clear leaks nothing; they hold no script, so a network attacker who alters them can restyle the page but not run code. Scripts stay HTTPS-only.
- The trust page carries the language switcher (ticket 134), and a phone reads it over port 80 before it trusts the box. So port 80 also serves the flags (`/static/flags/`, single files like the other static directories: public, no script) and accepts `POST /language`, and nothing else by POST (the trust page and CA downloads answer GET and HEAD only). The choice sets only the `hejmo_lang` cookie, which is not `Secure` over HTTP (it holds a language code, no secret). A sign-in session cookie is `Secure` and never arrives over HTTP, so no account is changed from there.
- The CA fingerprint on the trust page is served over HTTP, so it only helps against mistakes, not against an attacker on the network. The box also logs the fingerprint when it creates the CA; compare it with the log for a stronger check.
- If the box is reached by a two-label LAN name such as `fritz.box`, the CA's name constraint permits every name under it. Prefer the IP address or `hejmo.local` during setup.
- The CA is valid for 10 years and is not renewed. Box certificates are capped at the CA's expiry, and the box logs a warning once that cap applies.

## Notes on the public name (added in ticket #14)

- The wizard offers deSEC (a free `dedyn.io` name or an own domain), Cloudflare, Hetzner and INWX as DNS providers; #57 delivered the last three (product owner decision Q30, 16/11/2026). Cloudflare and INWX use the libdns modules `libdns/cloudflare` (an API token for one zone, optionally with a second token that only reads zones) and `libdns/inwx` (user name, password and, with two-factor sign-in, the shared secret, because INWX offers no scoped token). The Hetzner provider is Hejmo's own: a thin libdns provider over the Hetzner Cloud DNS REST API with `net/http` only (never the deprecated DNS Console API), because `libdns/hetzner/v2` would add 4.6 MB and some 45 modules to the binary. Each is one entry in `publiccert.defaultProviders`.
- Before the box writes its address record it looks up the name in the zone. A public address or a CNAME at the name is never overwritten (the family's website would be gone): the wizard refuses and suggests another name. An address that can only be an earlier box is replaced: private, loopback, link-local, 100.64/10 (carrier-grade NAT) and the LAN address of this try. Challenge records left by an earlier attempt are removed first.
- All credentials of a provider are sealed together (see below); the wizard never fills a secret into a form again after a failure.
- The box finds the DNS zone through SOA lookups, sets an A (or AAAA) record for the name to the LAN address the family enters (prefilled from the address used to reach the wizard) and keeps no other DNS records.
- Actalis needs an account: the wizard asks for its external account binding (key ID and HMAC key). ZeroSSL hands out account keys against an email address, so the fallback issuer is active only if the family enters an email address.
- The name check runs in the browser (a `no-cors` request to `https://<name>/healthz` that succeeds only if the name resolves and the certificate is trusted); the box cannot know what the family's router does with the name. On failure the page shows the FRITZ!Box rebind help and tries again every five seconds.
- The DNS credentials (token, or user name, password and shared secret) and account keys are sealed with AES-256-GCM; the key is a file, never in the database: `secret.key` in the data directory by default, or the file named by `HEJMO_SECRET_KEY_FILE` (for example a Docker secret), as ADR 0012 says.
- If the stored public setup cannot be served as usual at start, the box never falls back to plain HTTP (product owner decision Q27). It warns parents, also through `GET /api/v1/https`, keeps redirecting HTTP to HTTPS and logs the error. There are three cases:
  - **Not resumed** (the key file was lost, so the token cannot be opened): the box keeps serving the stored certificate, because renewal needs the token and serving does not.
  - **Not loaded** (the certificate is missing on disk): the box orders it again in the background.
  - **Not obtained** (the token opens, but the CA or deSEC cannot be reached): the public manager is ready but has no certificate.
  In the last two cases, and whenever the public certificate cannot be produced for a handshake, the box serves its own CA's certificate for the public name. The public certificate stays preferred. Phones then need to trust the own CA (trust page `/ca`, and `GET /api/v1/https` reports `own-ca` with `warning` true). The box creates that CA at start only once setup is complete: before that, the wizard's own-CA choice creates it for the address the browser used. For a short window after start, while a missing certificate is being obtained again, no warning is shown yet. Plain HTTP stays only for the setup wizard before HTTPS is configured. Choosing the public name again after such a fallback, or entering the token again, needs a settings page behind sign-in; that is open for the product owner.
- If the name check cannot pass because the router offers no rebind exception, the check page offers the box's own CA while setup is not complete; this forgets the public setup.
- Renewal failures are stored in the database and shown to parents on the start page and the calendars page and through `GET /api/v1/https`. The same warning appears when a certificate has less than 7 days left.
