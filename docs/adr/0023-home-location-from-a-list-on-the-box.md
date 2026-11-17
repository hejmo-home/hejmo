# 0023. Home location from a list on the box

- Status: Accepted
- Date: 16/11/2026
- Amends: [0022](0022-home-location.md) (supersedes its points "Open-Meteo once" and "internet needed for the wizard": the place is picked from a list that ships on the box), [0016](0016-family-view-header-and-footer.md) (the weather no longer finds a home by name; it reads stored coordinates only)

## Context

ADR 0022 made the parent type a place name and had the box look it up once at Open-Meteo. The design pass of ticket #86 (product owner decision Q1 = a, 15/11/2026) replaced that by suggestions while typing: a parent should see the places that match after two letters, tell same-named places apart by their region, and pick one. A lookup per keystroke at a service on the internet does not fit a box whose families expect it to stay at home, and it makes the setup wizard depend on the internet.

## Decision

- **A list of places ships inside the binary**, `internal/places/data/places.tsv.gz`, built from the GeoNames dumps for Germany, Austria and Switzerland by `tools/placelist` (run by hand or with `go generate ./internal/places`, never by the box). It keeps feature class P with 500 or more inhabitants, every PPLX (a district of a town) whatever its population, and every PPLF (a farm village: GeoNames gives such municipalities, for example Schmedeswurtherwesterdeich, no population); it drops PPLH, PPLQ and PPLW. Each place has its id, German name, ASCII name, German, English, French and Italian spellings for search, coordinates (4 decimals), population, country, ISO 3166-2 region, admin2 name and, for a district, its parent town (the largest non-district place with the same country and admin codes: Pasing → München). The list has about 22,000 places and is 0.5 MB gzipped (budget 2 MB; a test fails above 3 MB). It is loaded into memory at start-up.
- **Licence:** GeoNames is CC BY 4.0. The attribution ships next to the data (`LICENSE-GeoNames.txt`), is named on the licences page of the web interface (Settings → Licences) and in `NOTICE`. The file header names the dump date and the filter.
- **Search is local:** from two letters, case- and diacritic-insensitive (ü = ue = u, ß = ss) over name, ASCII name and the alternate spellings; names that start with the text come before names with a word that does, then larger places. At most five places are shown, with a flag that says more matched; the wizard and Settings say so in words ("the 5 largest") and never show "+n".
- **The form posts `place_id`**, the GeoNames id, and never coordinates. The server looks the coordinates and the region up and refuses an unknown id with a field error. `home_name` is stored as the place's name; the household keeps `home_place_id` (nullable) so the region line can be rebuilt in the page language. A home whose coordinates were set through the API has no place id; the box then shows its name and today's sun times without a region.
- **The region of the setup wizard comes from the place** while the select is empty; an explicit choice stays.
- **No Open-Meteo geocoding any more.** `weather.Search` and `weather.Geocode` are gone, and so is the weather's own fallback that looked a home up by name: every household location now comes from the list. A household set up before the list (a name, no coordinates) is asked in Settings, which opens the list with suggestions for its stored name; until a parent picks one the box shows no weather and no sun times. Open-Meteo remains the weather provider.
- **API:** `GET /places?name=&lang=` answers from the local list with at most five places `{id, name, region, latitude, longitude, region_code}` and `more` (200 also for no match; the 502 is gone). `PATCH /settings` accepts `home_place_id` as an alternative to explicit coordinates. The pages' script uses the same answer at `/places` (outside the API: the wizard has no sign-in yet).
- Without JavaScript the same picker works by submit: the server renders the suggestions for the posted text, each row is a submit button, "Change" and "Keep" are submits too.

## Consequences

- The setup wizard no longer needs the internet; the weather still does.
- A place that is not in the list (a hamlet under 500 inhabitants, a place outside DACH) cannot be picked; the page says to pick a larger town nearby. Raw coordinates stay possible through the API.
- A household set up before the list (a stored name, no coordinates) whose name matches no place of the list cannot save any household setting until the parent picks a place: the household page always posts the home field, and text without a pick is refused.
- The list is a snapshot: it is refreshed by running the tool and committing the result, with the release.
- The binary grows by about 0.5 MB, and memory by a few MB.
