package store

import (
	"database/sql"
	"slices"
	"strings"

	"github.com/hejmo-home/hejmo/internal/contact"
)

// Relation says who a contact is for the family (ticket 298): a word plus the people it is for, or the whole
// family. WholeFamily stores no people, so a person who joins later is included. PersonIDs are in the order of
// the people of the household.
type Relation struct {
	Word        string
	WholeFamily bool
	PersonIDs   []string
}

// RelatedContact is what the card of a person says about one contact: the contact and the word.
type RelatedContact struct{ ContactID, ContactName, Word string }

func relationsError(msg string) error { return &FieldError{Field: "relations", Msg: msg} }

// cleanRelations trims the words, drops people from whole-family relations, checks every relation and merges the
// same word (ignoring case) for the same people. The order of the first of each is kept.
func cleanRelations(in []Relation) ([]Relation, error) {
	var out []Relation
	seen := map[string]bool{}
	for _, r := range in {
		r.Word = strings.TrimSpace(r.Word)
		switch {
		case r.Word == "":
			return nil, relationsError("a relation needs a word")
		case contact.Runes(r.Word) > contact.MaxRelation:
			return nil, relationsError("a relation word is at most 40 characters")
		}
		r.PersonIDs = slices.Clone(r.PersonIDs)
		if r.WholeFamily {
			r.PersonIDs = nil
		}
		slices.Sort(r.PersonIDs)
		r.PersonIDs = slices.Compact(r.PersonIDs)
		if !r.WholeFamily && len(r.PersonIDs) == 0 {
			return nil, relationsError("a relation is for the whole family or for people")
		}
		key := strings.ToLower(r.Word) + "|" + strings.Join(r.PersonIDs, ",")
		if r.WholeFamily {
			key += "|whole"
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, r)
	}
	return out, nil
}

// writeRelations replaces the relations of a contact.
func writeRelations(tx *sql.Tx, householdID, contactID string, in []Relation, isPerson bool) error {
	if _, err := tx.Exec(`DELETE FROM contact_relations WHERE contact_id = ?`, contactID); err != nil {
		return err
	}
	rels, err := cleanRelations(in)
	if err != nil {
		return err
	}
	if isPerson && len(rels) > 0 {
		return relationsError("the contact of a person has no relations")
	}
	for i, r := range rels {
		id := newID()
		whole := 0
		if r.WholeFamily {
			whole = 1
		}
		if _, err := tx.Exec(`INSERT INTO contact_relations (id, contact_id, position, word, whole_family) VALUES (?, ?, ?, ?, ?)`, id, contactID, i+1, r.Word, whole); err != nil {
			return err
		}
		for _, p := range r.PersonIDs {
			res, err := tx.Exec(`INSERT INTO contact_relation_people (relation_id, person_id)
				SELECT ?, person_id FROM household_members WHERE household_id = ? AND person_id = ?`, id, householdID, p)
			if err != nil {
				return err
			}
			if n, _ := res.RowsAffected(); n == 0 {
				return relationsError("unknown person")
			}
		}
	}
	return nil
}

// loadRelations fills the relations of contacts (all of the household, or only one when onlyID is set).
func loadRelations(q interface {
	Query(string, ...any) (*sql.Rows, error)
}, householdID, onlyID string, list []Contact, at map[string]int) error {
	rows, err := q.Query(`SELECT r.contact_id, r.id, r.word, r.whole_family FROM contact_relations r JOIN contacts c ON c.id = r.contact_id
		WHERE c.household_id = ? AND (? = '' OR c.id = ?) ORDER BY r.contact_id, r.position`, householdID, onlyID, onlyID)
	if err != nil {
		return err
	}
	byID := map[string][2]int{} // relation id -> contact index, relation index
	for rows.Next() {
		var cid, rid string
		var r Relation
		if err := rows.Scan(&cid, &rid, &r.Word, &r.WholeFamily); err != nil {
			rows.Close()
			return err
		}
		if i, ok := at[cid]; ok {
			list[i].Relations = append(list[i].Relations, r)
			byID[rid] = [2]int{i, len(list[i].Relations) - 1}
		}
	}
	if err := rows.Close(); err != nil {
		return err
	}
	people, err := q.Query(`SELECT rp.relation_id, rp.person_id FROM contact_relation_people rp
		JOIN contact_relations r ON r.id = rp.relation_id JOIN contacts c ON c.id = r.contact_id
		JOIN household_members m ON m.person_id = rp.person_id AND m.household_id = c.household_id
		WHERE c.household_id = ? AND (? = '' OR c.id = ?) ORDER BY m.sort_order, m.rowid`, householdID, onlyID, onlyID)
	if err != nil {
		return err
	}
	defer people.Close()
	for people.Next() {
		var rid, pid string
		if err := people.Scan(&rid, &pid); err != nil {
			return err
		}
		if at, ok := byID[rid]; ok {
			r := &list[at[0]].Relations[at[1]]
			r.PersonIDs = append(r.PersonIDs, pid)
		}
	}
	return people.Err()
}

// PersonRelations lists what the card of a person says about contacts: one entry per relation that has the person
// in it or is for the whole family, with the contact and the word.
func (db *DB) PersonRelations(householdID, personID string) ([]RelatedContact, error) {
	rows, err := db.sql.Query(`SELECT c.id, c.hejmo_name, r.word FROM contact_relations r JOIN contacts c ON c.id = r.contact_id
		WHERE c.household_id = ? AND (r.whole_family = 1 OR EXISTS (SELECT 1 FROM contact_relation_people rp WHERE rp.relation_id = r.id AND rp.person_id = ?))
		ORDER BY c.hejmo_name COLLATE NOCASE, c.rowid, r.position`, householdID, personID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RelatedContact
	for rows.Next() {
		var r RelatedContact
		if err := rows.Scan(&r.ContactID, &r.ContactName, &r.Word); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
