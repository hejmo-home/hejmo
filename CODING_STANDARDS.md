# Coding standards for review

**Read by the reviewer, not by implementers.** These are the judgement calls no test, linter or CI check can enforce. Everything a check can enforce lives in CI, not here. Each point blocks the merge unless stated otherwise.

1. **A change that loosens a test or a check blocks.** Weakening an assertion, widening an expected value, adding a skip, relaxing a CI step or a lint rule needs a justification in the PR body that says why the old strictness was wrong. No justification, no merge. Deleting a test counts as loosening it.
2. **Data received from providers is stored as sent.** Calendar objects, ETags and other provider data are kept byte for byte and never decoded and re-encoded on the way into the store. The box may read them to understand them; it writes back what it received, changing only the fields it models. A provider bug becomes a quirk file first (`internal/caldavclient/testdata/quirks/README.md`).
3. **The PR body matches the code.** Every claim in the body (acceptance criteria with their tests, new dependencies, "no behaviour change", measurements) is checked against the diff. A claim the code does not back, or a change the body does not mention, blocks.
