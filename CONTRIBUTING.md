# Contributing to Hejmo

Thanks for helping. A few rules keep the project healthy.

## Developer Certificate of Origin (DCO)

Hejmo uses the [Developer Certificate of Origin 1.1](https://developercertificate.org/) instead of a CLA. By signing off a commit you confirm that you wrote the change or have the right to submit it under the project's license (AGPL-3.0).

Sign off every commit:

```bash
git commit -s -m "Add holiday rules for Bavaria"
```

This adds a line `Signed-off-by: Your Name <you@example.com>`. Pull requests with unsigned commits cannot be merged.

## How we work

- **Test first.** Write a failing test, make it pass, then clean up. No production code without a red test first.
- **Small commits, one topic each.** Commit messages, code, comments and issues are in English.
- **Dependencies** must be open source and AGPL-compatible. Explain every new dependency in the pull request.
- **No proprietary runtime.** Anything a user must install to run Hejmo is open source. Integrations with proprietary services (Google, iCloud, Outlook) are fine but never required. See [ADR 0004](docs/adr/0004-open-source-only-runtime.md).
- **No real personal data** in the repository. Use the example family from the specification (Mama, Papa, Sebastian, Mia).
- **Translatable text.** All user-facing text goes through the i18n catalogues in `internal/i18n/locales/` (German and English must have the same keys; a test checks this).

## Decisions

Larger decisions are recorded as ADRs in `docs/adr/`. Propose a new ADR in your pull request if you change scope, guardrails or the data model.
