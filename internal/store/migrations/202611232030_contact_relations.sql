-- Relations between a contact and the people of the household (ADR 0021, ticket 298).
--
-- A relation is one word ("Oma", "Klassenlehrerin", at most 40 characters) plus the people it is for, or the whole
-- family. "Whole family" stores no people, so a person who joins later is included. Only further contacts have
-- relations (the people of the household are related through "Personen"); the store checks that. Deleting the
-- contact deletes its relations. When a person leaves, their id goes from the relations, and a relation that has no
-- people left and is not for the whole family goes with it.
CREATE TABLE contact_relations (
    id           TEXT PRIMARY KEY,
    contact_id   TEXT NOT NULL REFERENCES contacts(id) ON DELETE CASCADE,
    position     INTEGER NOT NULL,
    word         TEXT NOT NULL CHECK (length(word) BETWEEN 1 AND 40),
    whole_family INTEGER NOT NULL DEFAULT 0 CHECK (whole_family IN (0, 1)),
    UNIQUE (contact_id, position)
);

CREATE TABLE contact_relation_people (
    relation_id TEXT NOT NULL REFERENCES contact_relations(id) ON DELETE CASCADE,
    person_id   TEXT NOT NULL REFERENCES people(id) ON DELETE CASCADE,
    PRIMARY KEY (relation_id, person_id)
);
CREATE INDEX contact_relation_people_person ON contact_relation_people (person_id);

CREATE TRIGGER contact_relation_people_left AFTER DELETE ON contact_relation_people
BEGIN
    DELETE FROM contact_relations
    WHERE id = OLD.relation_id AND whole_family = 0
      AND NOT EXISTS (SELECT 1 FROM contact_relation_people WHERE relation_id = OLD.relation_id);
END;
