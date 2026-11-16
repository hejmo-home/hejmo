# 0020. Home location

- Status: Accepted, amended by [0021](0021-home-location-from-a-list-on-the-box.md) (the place is picked from a list on the box; no Open-Meteo lookup, no internet for the wizard)
- Date: 15/11/2026
- Amends: [0015](0015-display-web-interface-and-everyday.md) (the sun position for quiet mode, the front light and themes comes from a confirmed home location), [0017](0017-web-interface-usability.md) (a place field with confirmation in the setup wizard and under Settings → Household)

## Context

Nothing set the household's latitude and longitude: the setup wizard collected a town name and a region, and no page or API wrote coordinates. The sun rule for the front light (#29), the themes by sunrise and sunset (#52) and the weather (#28) all need them. Deriving them from the region is too coarse for sunset. The product owner decided (Q59, 15/11/2026, issue #86).

## Decision

- The home location is a **place name** the parent enters in the setup wizard and under Settings → Household (ticket #86). The box searches it **once** with Open-Meteo's geocoding API (the provider of the weather; no key, no new library) and stores the confirmed **latitude and longitude** with the household.
- The page shows the resolved place for confirmation. When several places match, the parent chooses (up to five are offered); none is preselected. Nothing is stored before the parent confirms: the wizard does not create the household and Settings does not save any field until a place is confirmed.
- No match, or a service that cannot be reached, is an error at the name field; what was typed stays. Only the name (and the country of the region and the language of the page) is sent to Open-Meteo.
- A name that did not change, on a household that already has a location, is not searched again. Boxes that have a name but no location are asked in Settings; the weather's own one-time search by name (ADR 0016) stays as a fallback until then.
- API: `GET /settings` returns `home_name`, `latitude` and `longitude` (null while unknown); `PATCH /settings` takes them (both coordinates together, in range; a new name needs coordinates); `GET /places?name=` lists matches for clients that have no web page (ADR 0015: web pages with API parity).
- Sunrise and sunset, quiet mode's front light, themes and the weather read the stored coordinates only.

## Consequences

- The setup wizard needs a working internet connection for the place search; without it the wizard shows the error and the parent can retry.
- Places can be found only where Open-Meteo knows them; the parent can choose a larger place nearby. Entering raw coordinates in the web interface is not offered; the API accepts them.
