-- Nutrition values from ingredients (ticket 288, ADR 0021).
--
-- The switch "Nährwerte bei Rezepten" per person: off for everyone until a parent
-- switches it on for a child. Parents always see the values, so the switch is
-- only read for children.
ALTER TABLE people ADD COLUMN nutrition_shown INTEGER NOT NULL DEFAULT 0;

-- The food behind an own article: the id of a food of the box ("bls:S161000",
-- "usda:170848"; internal/nutrition) or '' for none, and the weight of one piece
-- in grams (0: not known). Shipped articles have theirs in the data file.
ALTER TABLE articles ADD COLUMN food TEXT NOT NULL DEFAULT '';
ALTER TABLE articles ADD COLUMN piece_g REAL NOT NULL DEFAULT 0 CHECK (piece_g >= 0 AND piece_g <= 5000);
