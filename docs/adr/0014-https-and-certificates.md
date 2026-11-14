# 0014. HTTPS and certificates

- Status: Accepted
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
