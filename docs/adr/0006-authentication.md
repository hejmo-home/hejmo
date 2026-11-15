# 0006. Authentication

- Status: Accepted, amended by [0015](0015-display-web-interface-and-everyday.md), [0017](0017-web-interface-usability.md), [0018](0018-path-to-0-1.md)
- Date: 14/11/2026

## Context

The specification sets passkeys as default sign-in, password + TOTP as fallback, plus a parent PIN. Passkeys (WebAuthn) only work in a secure context: HTTPS or `localhost`. A fresh box on `http://192.168.x.x` has neither.

## Decision

- **Default sign-in for adults:** password plus TOTP.
- **Passkeys** are offered once HTTPS is configured (own domain or reverse proxy); moved to V1.x.
- **Parent PIN** applies **only on displays**, to unlock management functions there. Web and phone always use the parent account.

## Consequences

- The setup wizard works on a fresh box without certificates.
- The PIN is a convenience lock on a shared device, not an account credential.
