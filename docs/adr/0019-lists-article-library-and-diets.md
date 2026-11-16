# 0019. Lists: article library, quantities, tile view and diets

- Status: Accepted
- Date: 16/11/2026
- Amends: [0015](0015-display-web-interface-and-everyday.md) (list entries come from an article library or free text; tile view), [0018](0018-path-to-0-1.md) (0.1 grows by this ADR's 0.1 items)

## Context

Lists (#32, #33) take only typed text. On 16/11/2026 the product owner asked where entries come from, for a library of default articles by diet, a picture view for children, quantities for recipes, sync with other apps, and diets and allergies per person (questions Q11 to Q17).

## Decision

### Versions

- **0.1:** diet and allergies per person, article library with suggestions and own articles, adding with or without quantity, tile view.
- **0.2:** sync of lists with other apps (Apple Reminders), "always on the list" articles.

### Diet and allergies per person

- Each person has a **diet**: omnivore (default), vegetarian, pescatarian or vegan.
- Each person may have **allergies and intolerances**: any of the 14 main allergens of EU Regulation 1169/2011 Annex II, plus free text.
- Only parents change them. They are health data: they stay in the box, are never synced to other apps, and MCP tokens get them only when a parent grants it explicitly.
- **Free text cannot be checked:** wherever allergies are shown or used for warnings, a free-text allergy is shown as a note ("Mia: Kiwi, not checked automatically"). Recipes (V2) always carry this note.

### Article library

- An **article** is a thing that can be put on a list: name (de/en), symbol, department, the diets it fits, the allergens it contains.
- The box ships about 250 articles in German and English. Symbols are emoji from **Noto Emoji** (Apache-2.0) as placeholders.
- Parents manage the library under Settings, Lists, Library: add **own articles**, hide shipped ones, change name, symbol, department, diets and allergens. An own article's picture is a **photo upload** or an **image address on the web**. The box downloads a web image once, never links to it; it accepts only http(s) addresses that resolve to public addresses, checks type and size, and stores every picture re-encoded and scaled down (at most 512 px, JPEG), metadata removed.
- Free text stays possible; afterwards the box offers to add it to the library.
- **Suggestions follow the household's diets:** an article is suggested when it fits at least one person's diet.
- **Allergen warnings:** an article containing an allergen of a person shows a warning in words that names the person. Optionally, a household setting **hides** such articles. That setting is marked **experimental**, with the notice that Hejmo does not guarantee completeness and takes no liability; the warning in words stays the default.

### Adding and quantities

- Two ways to add: **quick** (the article only) and **with quantity** (number and unit).
- Units: piece, dozen, pack, bunch; metric g, kg, ml, l; imperial and US oz, lb, fl oz (US), fl oz (UK), cup (US), pint (US), pint (UK), quart (US), gallon (US), gallon (UK). US and UK measures that differ are named apart.
- The same article with the same unit adds up; different units stand side by side.

### Tile view

- A list can be shown as **tiles** (symbol or picture, name, tick) or as a **list**. Children see tiles by default, adults the list; each person's choice is remembered.

### Sync (0.2)

- Each list appears as a list in **Apple Reminders** over CalDAV (VTODO), as the tasks of ADR 0018: entry, done, quantity in the title. Settings, Lists, "Share with other apps" has a switch per list and the iOS profile. Apple Notes has no interface and is not supported.

## Consequences

- New tickets for each 0.1 item; Opus designs before code.
- The data model grows by articles, pictures, quantities, diets and allergies; list entries keep their text so free text and sync stay simple.
- Downloading web images is a new outbound request from the box; the threat model gets an entry (server-side request forgery).
