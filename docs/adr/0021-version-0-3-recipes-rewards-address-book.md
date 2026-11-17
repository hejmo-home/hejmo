# 0021. Version 0.3: recipes, star rewards and address book

- Status: Accepted
- Date: 16/11/2026
- Amends: [0012](0012-calendar-sync.md) (the box serves contacts over CardDAV), [0015](0015-display-web-interface-and-everyday.md) (meal plan entries may be recipes; layout block "Emergency numbers"), [0018](0018-path-to-0-1.md) (reward catalogue, streaks and badges; MCP area "contacts"), [0019](0019-lists-article-library-and-diets.md) (varieties of articles, pantry articles, recipes carry the free-text allergy note), [0020](0020-versions-0-2-and-0-3.md) (details of the 0.3 tickets)

## Context

ADR 0020 placed recipes (#193), star rewards (#194) and the box as address book (#195) in 0.3 and left their details to grilling rounds. The product owner decided them on 16/11/2026 (questions Q94 to Q136; the decisions are recorded on each ticket).

## Decision

### Recipes (#193)

- **Sources:** own entry, import from a web page, and a starter collection shipped with 0.3.
- **A recipe** has a name, pictures, base portions, ingredients with quantity and unit, numbered steps, an optional time, a source link, suitable mealtimes, a category, free tags, tips, a favourite mark per person, "last cooked" and, where available, nutrition values.
- **Own photos:** families add their own photos to any recipe, including imported and starter recipes, from the device's camera or gallery; one is the cover. Pictures follow the rules for article pictures (re-encoded, metadata removed), scaled to at most 1600 px.
- **Languages:** the starter collection exists in **German and English**, and the box shows the household's language. Recipes a family enters or imports keep the language they were written in.
- **Ingredients** are an article of the library, optionally with a **variety** ("Mehl · Type 405"), or free text. Allergies and diets are checked only for articles; free text carries "not checked automatically". Varieties also exist for list entries; quantities add up only for the same variety.
- **Portions:** a meal plan entry defaults to the number of people in the household; quantities scale and are rounded to one decimal, never up to whole numbers.
- **Meal plan:** an entry is free text or a recipe; meal plan templates may hold recipes.
- **Shopping list:** by hand (default; a tick list per meal or week) or automatically (household setting: every day at 03:00 the meals of the next 7 days, configurable). **Pantry articles** such as salt and oil are left out by default; nothing is added automatically in holiday mode. When a recipe leaves the plan or its portions shrink, its quantity is subtracted from entries not yet ticked.
- **Warnings** name the person and never block; vegetarian and vegan marks when all articles fit.
- **Cooking view** on every device (large text, ticking ingredients, step by step, screen stays on); wall displays show only the dish name. Parents create and plan recipes; children see them.
- **Import:** an own parser for schema.org/Recipe JSON-LD (make or buy: `go-recipe` used as reference only). The import takes everything for the household's private use and keeps the source link; imported recipes never leave the household.
- **Starter collection:** about 200 proven dishes from mixed established sources. Ingredients and quantities are taken over, the steps are written anew in own words, and each recipe names its source. A Sonnet agent researches the list and writes the texts; the product owner approves the list first. No invented recipes.
- **Nutrition:** imported values from the source page; otherwise calculated from the ingredients with BLS 4.0 (Max Rubner-Institut, CC BY 4.0) and USDA FoodData Central (CC0), shipped offline. The recipe says the values are calculated and names the source; missing values show "incomplete". Seven values of the EU nutrition declaration per portion, as guide values, only in the recipe view, hidden for children unless a parent switches them on, never on wall displays.
- **MCP:** recipes belong to the meal plan area.

### Star rewards (#194)

- A **reward catalogue** per household; stars stay with the person. A reward has a name, emoji or picture, star price, who it is for and an optional limit.
- **Redemption:** a child asks, a parent confirms or declines; parents can redeem directly. Stars are booked only on confirmation and never go below zero.
- One **savings goal** per person, with progress in the personal view and in the display's person column. No catalogue or redeeming on wall displays.
- **Streaks** only for routines, shown as "this week 5 of 7" and best streak; **pause days** do not break them; a missed day gives no message. No automatic bonus stars.
- About ten shipped **badges**, never taken away; own badges later.
- A switch per person, on for children and off for adults by default. MCP: under the tasks area.

### Address book (#195)

- **Contacts:** the household's people plus others, each with a full name and one **Hejmo name**, several labelled phone numbers and e-mail addresses, address, birthday, photo, note, contact groups and **relations** to people.
- **CardDAV** for parents with a device token of the new area `contacts`; two-way, except that name and deleting of household people happen only in Hejmo. Hejmo is its own address book on the phone; contacts are taken over by vCard import, with duplicates offered for merging.
- **Children** get no CardDAV but an "Important numbers" view of contacts marked visible for children; a relation alone does not make a contact visible.
- A generated read-only calendar **Birthdays** and a layout block **Emergency numbers** (off by default).
- Contacts stay in the box; MCP only with the area `contacts`, no right by default.

## Consequences

- The data model grows by recipes, ingredients, varieties, nutrition data, rewards, redemptions, streaks, badges, contacts and relations.
- The box ships two nutrition databases and a starter collection with a credits file; the import of web pages is a further outbound request for the threat model (same rules as article pictures).
- Each 0.3 ticket is split into buildable tickets; Opus designs every screen before code.
