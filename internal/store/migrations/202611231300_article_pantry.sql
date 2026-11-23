-- Pantry articles (ADR 0021, ticket 278): articles usually at home, such as
-- salt and oil, which recipes leave out of the shopping list by default.
-- An own article carries the mark itself; for a shipped article the data file
-- holds the default and only a parent's deviation from it is stored here.
ALTER TABLE articles ADD COLUMN pantry INTEGER NOT NULL DEFAULT 0;

CREATE TABLE article_pantry (
    household_id TEXT NOT NULL REFERENCES households(id) ON DELETE CASCADE,
    article_key  TEXT NOT NULL, -- key of the data file
    pantry       INTEGER NOT NULL,
    PRIMARY KEY (household_id, article_key)
);
