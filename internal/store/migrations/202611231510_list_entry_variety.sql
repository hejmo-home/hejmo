-- Varieties of articles on list entries (ticket 277, ADR 0019): "Mehl · Type 405".
-- NULL for none; only an entry with an article has one. The words of the entry
-- stay in text, the article's name; quantities add up only for the same article
-- and variety (internal/list).
ALTER TABLE list_entries ADD COLUMN variety TEXT CHECK (variety IS NULL OR (variety <> '' AND article_id <> ''));
