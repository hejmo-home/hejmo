# Hejmo – notes for agents

Self-hosted, open-source family organizer: a Go box (Raspberry Pi, Docker) with SQLite, a REST API and calm E-Ink wall displays. AGPL-3.0.

## Start here

- `GLOSSARY.md`: domain language. Use these terms in code, tests, issues and PRs.
- `docs/adr/`: decisions. **ADRs override `docs/spec.md` and `docs/handover.md`.** ADR 0003 holds the V1.0 work packages; ADR 0018 says what else goes into 0.1, the first public version.
- `docs/spec.md`: the full product specification (frozen 14/11/2026), including acceptance criteria and the "Nordic Calm" design values.
- `docs/research/`: researched facts with sources (e.g. public holidays).

## Working rules

- **Strict TDD.** Every acceptance criterion starts as a failing test. No production code without a red test first.
- **Seams:** domain logic in pure packages (`internal/calendar`, `internal/holiday`, `internal/auth`) tested directly. Persistence through `internal/store` against a real SQLite file. HTTP through `internal/server` tests that call the API **contract-checked against `api/openapi.yaml`**. Every API change updates `openapi.yaml`.
- **Small commits, one topic each, always `git commit -s`** (DCO sign-off; CI rejects unsigned commits; `.githooks/prepare-commit-msg` adds it, see CONTRIBUTING.md). End commit messages with the `Co-Authored-By` trailer for the model in use.
- **One branch and one PR per work package or ticket.** The PR body lists acceptance criteria with their tests, decisions to review, new dependencies (license, why, in binary or not) and local measurements.
- **Merge** only after CI is green on x86_64 and arm64 **and** an Opus `/code-review` of the PR has no open findings; then merge without waiting for the product owner, who reviews afterwards. Tickets labelled `ready-for-human` still wait for the product owner's part, and design changes (palettes, layouts) wait for her approval of a preview. Use a merge commit (`gh pr merge --merge --delete-branch`), not squash, so signed-off commits survive.
- **Ask before:** deviating from scope, guardrails or acceptance criteria; adding external services, network connections or non-AGPL-compatible dependencies; data-model changes that would make V2/V3 (shared custody, multiple households) harder.
- **Models for subagents:** code is written by subagents on **Sonnet**; reviews and verification (code review, checking a subagent's result, adversarial checks) run on **Opus**.
- **Make or buy:** before building anything foundational (a protocol, parser, crypto, sync engine, UI framework), search for open-source libraries, compare them (license, maintenance, size, fit) against building it, and put the choice to the product owner. "Make" is not the default.
- **Open source only at runtime** (ADR 0004). Proprietary services are allowed as optional integrations.
- **No real personal data** in the repo. Test data uses the example family: Mama, Papa, Sebastian, Mia.
- **Migrations** are named `YYYYMMDDhhmm_name.sql` (UTC minute you write it, lower-case ascii name); never a running counter, so branches cannot collide. A test fails on duplicate or non-ascending prefixes. They are append-only once 0.1 is released (preview builds from `main` are not releases); before that, an unreleased migration may still be edited.
- **Reviewers** also read `CODING_STANDARDS.md`: judgement calls no check can enforce. Implementers do not need it.
- Code, comments, commits, issues and docs in English. User-facing text through `internal/i18n/locales/` (German and English, same keys).

## Commands

```bash
go test ./...                       # all tests
go test -race -count=1 ./...        # as CI runs them (CI sets HEJMO_REQUIRE_RADICALE=1: no Docker means failure, not skip)
ci/test-shard.sh 1 -race -count=1       # one CI shard (1 and 2: half of internal/server each; 3: every other package); tests that share a database use internal/store/storetest.Path, a migrated copy per test; tests are parallel (t.Parallel()) unless they swap a package-level value
node --test web/test                # the JavaScript tests alone (also run inside go test; CI sets HEJMO_REQUIRE_NODE=1: no node means failure, not skip)
go test -run 'TestRenderedLayout(Part2)?$' ./internal/server  # the rendered layout check (two tests, each half of the pages by a hash of the page name, one per CI shard): every page in headless Chrome (chromedp) at 375, 768, 1024, 1440 and 1900 px, light and black-and-white, German and English; names broken, text overflowing, pages wider than the viewport and a side navigation that is not beside the content from 1024 px up fail it. Needs Chrome: skipped without, CI sets HEJMO_REQUIRE_CHROME=1 (x86_64) so no Chrome means failure; HEJMO_CHROME picks the binary, HEJMO_LAYOUT_SHOTS the directory for screenshots of failing pages
gofmt -l . && go vet ./...          # formatting and vet (CI checks both)
docker compose up -d --build --wait # run the box on http://localhost (HTTP) and https://localhost (HTTPS)
```

**Local test runs share one 16 GB Mac** with other agents, the test box and a browser; full `-race` suites in parallel have filled its memory and frozen the desktop app. Locally, run only the tests you added or changed, for example `go test -race -count=1 -p 1 -parallel 2 -run 'TestFoo|TestBar' ./internal/server`; never run the whole `./internal/server` package locally. Measured in CI (`/usr/bin/time -v`, `-race -count=1`, run 45731098389 of #372): the whole package peaks at about 2.6 GB (arm64) and 2.6 GB (x86_64), down from about 4.1 GB on the main of 26/11/2026 (and about 10 GB before #239, JetsamEvent of 22/11/2026, issue #238); the CI shards stay at 0.85 to 1.9 GB each. Still above what a shared Mac should spend. A local watchdog kills any test binary over 3 GB; a killed run means: rely on CI, plus `gofmt`, `go vet` and `node --test web/test`. The full `-race` suite runs in CI (about 10 minutes): cite that CI run as evidence instead of a local full run. Red runs for the PR body come from the single test that proves the fix.

Local tooling on the development Mac: Go in `~/.local/go/bin`, Homebrew in `/opt/homebrew/bin` (`gh`, `node`), Docker CLI in `/Applications/Docker.app/Contents/Resources/bin`. Non-interactive shells may need these on `PATH`.

## Agent skills

### Issue tracker

Issues and tickets live in GitHub Issues of `hejmo-home/hejmo`, used via the `gh` CLI. See `docs/agents/issue-tracker.md`.

### Triage labels

The five default roles with identical label names (`needs-triage`, `needs-info`, `ready-for-agent`, `ready-for-human`, `wontfix`). See `docs/agents/triage-labels.md`.

### Domain docs

Single-context: `GLOSSARY.md` and `docs/adr/` at the repo root. See `docs/agents/domain.md`.
