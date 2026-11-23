-- Address book (ADR 0021, ticket 296): contacts with several labelled numbers
-- and e-mail addresses, an address, a birthday, a note and groups.
--
-- Every person of the household is a contact from the start (person_id): the
-- row is made with the person (AddPerson), follows the person's name and goes
-- with the person (ON DELETE CASCADE). The contact of a person has the id of
-- the person. Numbers and e-mail addresses are kept exactly as typed.
CREATE TABLE contacts (
    id           TEXT PRIMARY KEY,
    household_id TEXT NOT NULL REFERENCES households(id) ON DELETE CASCADE,
    person_id    TEXT UNIQUE REFERENCES people(id) ON DELETE CASCADE, -- NULL for someone outside the household
    hejmo_name   TEXT NOT NULL,                                       -- what Hejmo and the displays show
    full_name    TEXT NOT NULL DEFAULT '',
    street       TEXT NOT NULL DEFAULT '',
    postcode     TEXT NOT NULL DEFAULT '',
    city         TEXT NOT NULL DEFAULT '',
    country      TEXT NOT NULL DEFAULT '',
    bday_day     INTEGER,                                             -- day and month together, the year may be unknown
    bday_month   INTEGER,
    bday_year    INTEGER,
    note         TEXT NOT NULL DEFAULT '',
    created_by   TEXT REFERENCES people(id) ON DELETE SET NULL,
    created_at   TEXT NOT NULL,
    updated_by   TEXT REFERENCES people(id) ON DELETE SET NULL,
    updated_at   TEXT NOT NULL,
    CHECK ((bday_day IS NULL) = (bday_month IS NULL))
);
CREATE INDEX contacts_household ON contacts (household_id, hejmo_name);

CREATE TABLE contact_phones (
    contact_id   TEXT NOT NULL REFERENCES contacts(id) ON DELETE CASCADE,
    position     INTEGER NOT NULL,
    kind         TEXT NOT NULL CHECK (kind IN ('cell', 'home', 'work', 'other', 'custom')),
    custom_label TEXT NOT NULL DEFAULT '',
    value        TEXT NOT NULL, -- as typed
    PRIMARY KEY (contact_id, position)
);

CREATE TABLE contact_emails (
    contact_id   TEXT NOT NULL REFERENCES contacts(id) ON DELETE CASCADE,
    position     INTEGER NOT NULL,
    kind         TEXT NOT NULL CHECK (kind IN ('cell', 'home', 'work', 'other', 'custom')),
    custom_label TEXT NOT NULL DEFAULT '',
    value        TEXT NOT NULL, -- as typed
    PRIMARY KEY (contact_id, position)
);

-- The five groups of Hejmo carry a shipped_key (their name comes from the
-- locales); a household's own groups carry a name.
CREATE TABLE contact_groups (
    id           TEXT PRIMARY KEY,
    household_id TEXT NOT NULL REFERENCES households(id) ON DELETE CASCADE,
    name         TEXT NOT NULL DEFAULT '',
    shipped_key  TEXT CHECK (shipped_key IN ('familie', 'kinderbetreuung_schule', 'arztpraxen', 'notfall', 'sonstige')),
    created_by   TEXT REFERENCES people(id) ON DELETE SET NULL,
    created_at   TEXT NOT NULL,
    CHECK ((shipped_key IS NULL) = (name <> ''))
);
CREATE UNIQUE INDEX contact_groups_shipped ON contact_groups (household_id, shipped_key) WHERE shipped_key IS NOT NULL;
CREATE UNIQUE INDEX contact_groups_name ON contact_groups (household_id, name COLLATE NOCASE) WHERE shipped_key IS NULL;

CREATE TABLE contact_group_members (
    group_id   TEXT NOT NULL REFERENCES contact_groups(id) ON DELETE CASCADE,
    contact_id TEXT NOT NULL REFERENCES contacts(id) ON DELETE CASCADE,
    PRIMARY KEY (group_id, contact_id)
);
CREATE INDEX contact_group_members_contact ON contact_group_members (contact_id);

-- Households that exist already: the five groups, and a contact for every
-- person, always in Familie.
INSERT INTO contact_groups (id, household_id, name, shipped_key, created_at)
SELECT lower(hex(randomblob(16))), h.id, '', k.key, h.created_at
FROM households h
JOIN (SELECT 'familie' AS key UNION ALL SELECT 'kinderbetreuung_schule' UNION ALL SELECT 'arztpraxen'
      UNION ALL SELECT 'notfall' UNION ALL SELECT 'sonstige') k;

INSERT INTO contacts (id, household_id, person_id, hejmo_name, created_at, updated_at)
SELECT p.id, m.household_id, p.id, p.name, p.created_at, p.updated_at
FROM people p JOIN household_members m ON m.person_id = p.id;

INSERT INTO contact_group_members (group_id, contact_id)
SELECT g.id, c.id FROM contacts c
JOIN contact_groups g ON g.household_id = c.household_id AND g.shipped_key = 'familie'
WHERE c.person_id IS NOT NULL;
