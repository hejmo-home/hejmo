# 0026. Phone numbers with nyaruka/phonenumbers

- Status: Accepted
- Date: 24/11/2026
- Decided by: the product owner on 23/11/2026 (comment on #195)
- Relates to: [0004](0004-open-source-only-runtime.md) (open-source runtime), [0021](0021-version-0-3-recipes-rewards-address-book.md) (address book)

## Context

The address book of version 0.3 needs to know when two phone numbers are the same number. The vCard import (#300) must find duplicates by phone number, not only by name, and files write the same number in many ways ("+49 171 3456789", "0171 3456 789", "0049-171-3456789"). Tap-to-call for children (#301) needs a number a phone can dial. Parsing, validating and typing phone numbers for every country is foundational work with a large, changing data set (number plans), so the make-or-buy rule of CLAUDE.md applies.

## Decision

- **Buy:** Hejmo uses `github.com/nyaruka/phonenumbers` (MIT, pure Go, a port of Google's libphonenumber with its metadata) to parse, validate, normalise to E.164 and type phone numbers. No other phone number library and no hand-written number plans.
- **Region:** a number without a country code is read with the household's home country (ADR 0022/0023) as its region.
- **Storage stays as typed:** a contact keeps the number as the parent or the file wrote it, because that is how people recognise it. The E.164 form is stored beside it and is used for comparing (duplicates in the import, the same number twice on one contact) and for `tel:` links.
- **Numbers that cannot be parsed** (service numbers like 116117, short internal numbers) are kept as typed and compared by their digits only; they are never rejected.
- **Empty values never match**, neither in E.164 nor in digits.
- It comes in with the first contacts ticket that needs it (#300). The library makes no network connections, so ADR 0024 is not touched.

## Consequences

- `go.mod` gets `github.com/nyaruka/phonenumbers`; its licence goes into the licences page like every other dependency.
- Updating the library updates the number plans, so it should be kept current like other dependencies.
- The binary grows by the metadata (a few MB), which is acceptable for the box.
