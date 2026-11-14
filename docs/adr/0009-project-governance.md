# 0009. Project governance

- Status: Accepted
- Date: 14/11/2026

## Decision

- **Hosting:** GitHub organisation `hejmo-home`, repository `hejmo`. CI runs on GitHub Actions for ARM64 and x86; merges only with green tests.
- **Language:** code, commits, issues, documentation and README in English. The user interface ships in German and English from the start.
- **Contributions:** Developer Certificate of Origin (sign-off per commit), no CLA.
- **Test data:** only the example family from the specification (Mama, Papa, Sebastian, Mia). No real personal data in the repository.
- **Roles:** the product owner approves one pull request per work package; the implementer decides technical details within the guardrails and documents them in the pull request.
- **Trademark:** the trademark check for "Hejmo" (DPMA/EUIPO, classes 9 and 42) is still open. Domain candidate: `hejmo-home.org`.

## Consequences

- Without a CLA, dual licensing would later need consent from every contributor.
