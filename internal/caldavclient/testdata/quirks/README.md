# Provider quirk corpus

Real CalDAV providers answer differently from the clean WebDAV of our fakes and of Radicale. Every quirk we have met lives here as a hand-built, anonymised response in the provider's shape (no real account data, hosts end in `.example`). `quirks_test.go` in the parent folder runs each quirk through discovery, list and multiget.

**Rule: every future provider bug becomes a quirk file first.** Add the quirk, watch the table test fail, then fix the client. Never fix a provider bug without a quirk that fails without the fix.

## Layout of a quirk folder

| File | Content |
| --- | --- |
| `quirk.txt` | One sentence: what the provider does. |
| `routes` | One line per answer: `HOST METHOD[:BODY-SUBSTRING] PATH STATUS BODYFILE [Location=URL]`. `BODYFILE` is `-` for an empty body. A `REPORT:calendar-multiget` route matches only requests whose body contains `calendar-multiget`. Unmatched requests answer 404. |
| `*.xml` | Response bodies. `{{ics:name.ics}}` is replaced by the XML-escaped content of `name.ics` in the same folder. |
| `*.ics` | iCalendar text exactly as the provider stores it (CRLF line ends, kept by `.gitattributes`). |
| `expect.json` | `server` (address the client is given), optional `calendars` (expected result of discovery), `calendar` (address to list and fetch from), `list` (expected objects with ETag) and `fetch` (expected objects with ETag and `data` file; the data must arrive byte for byte). |

Each host named in `routes` becomes a fake server; requests for that host name are dialed to it, so multi-host providers such as iCloud can be modelled.

## Not covered yet

Write-back (ticket #17) is not merged. When it lands, add a write step to the table test and a quirk per provider rule for it (ETag handling, rewritten UIDs).
