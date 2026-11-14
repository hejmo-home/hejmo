# Hejmo – notes for agents

Self-hosted, open-source family organizer: a Go box (Raspberry Pi, Docker) with SQLite, a REST API and calm E-Ink wall displays. AGPL-3.0.

## Start here

- `GLOSSARY.md`: domain language. Use these terms in code, tests, issues and PRs.
- `docs/adr/`: decisions. **ADRs override `docs/spec.md` and `docs/handover.md`.** ADR 0003 holds the V1.0 work packages and what moved to V1.x.
- `docs/spec.md`: the full product specification (frozen 14/11/2026), including acceptance criteria and the "Nordic Calm" design values.
- `docs/research/`: researched facts with sources (e.g. public holidays).

## Working rules

- **Strict TDD.** Every acceptance criterion starts as a failing test. No production code without a red test first.
- **Seams:** domain logic in pure packages (`internal/calendar`, `internal/holiday`, `internal/auth`) tested directly. Persistence through `internal/store` against a real SQLite file. HTTP through `internal/server` tests that call the API **contract-checked against `api/openapi.yaml`**. Every API change updates `openapi.yaml`.
- **Small commits, one topic each, always `git commit -s`** (DCO sign-off; CI rejects unsigned commits). End commit messages with the `Co-Authored-By` trailer for the model in use.
- **One branch and one PR per work package or ticket.** The PR body lists acceptance criteria with their tests, decisions to review, new dependencies (license, why, in binary or not) and local measurements.
- **Merge** only after CI is green on x86_64 and arm64 **and** an Opus `/code-review` of the PR has no open findings; then merge without waiting for the product owner, who reviews afterwards. Tickets labelled `ready-for-human` still wait for the product owner's part, and design changes (palettes, layouts) wait for her approval of a preview. Use a merge commit (`gh pr merge --merge --delete-branch`), not squash, so signed-off commits survive.
- **Ask before:** deviating from scope, guardrails or acceptance criteria; adding external services, network connections or non-AGPL-compatible dependencies; data-model changes that would make V2/V3 (shared custody, multiple households) harder.
- **Models for subagents:** code is written by subagents on **Sonnet**; reviews and verification (code review, checking a subagent's result, adversarial checks) run on **Opus**.
- **Make or buy:** before building anything foundational (a protocol, parser, crypto, sync engine, UI framework), search for open-source libraries, compare them (license, maintenance, size, fit) against building it, and put the choice to the product owner. "Make" is not the default.
- **Open source only at runtime** (ADR 0004). Proprietary services are allowed as optional integrations.
- **No real personal data** in the repo. Test data uses the example family: Mama, Papa, Sebastian, Mia.
- **Migrations are append-only** once a 0.x release exists. Before that, an unreleased migration may still be edited.
- Code, comments, commits, issues and docs in English. User-facing text through `internal/i18n/locales/` (German and English, same keys).

## Commands

```bash
go test ./...                       # all tests
go test -race -count=1 ./...        # as CI runs them
gofmt -l . && go vet ./...          # formatting and vet (CI checks both)
docker compose up -d --build --wait # run the box on http://localhost (HTTP) and https://localhost (HTTPS)
```

Local tooling on the development Mac: Go in `~/.local/go/bin`, Homebrew in `/opt/homebrew/bin` (`gh`), Docker CLI in `/Applications/Docker.app/Contents/Resources/bin`. Non-interactive shells may need these on `PATH`.

## Agent skills

### Issue tracker

Issues and tickets live in GitHub Issues of `hejmo-home/hejmo`, used via the `gh` CLI. See `docs/agents/issue-tracker.md`.

### Triage labels

The five default roles with identical label names (`needs-triage`, `needs-info`, `ready-for-agent`, `ready-for-human`, `wontfix`). See `docs/agents/triage-labels.md`.

### Domain docs

Single-context: `GLOSSARY.md` and `docs/adr/` at the repo root. See `docs/agents/domain.md`.
