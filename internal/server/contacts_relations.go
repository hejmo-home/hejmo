package server

import (
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/text/collate"
	textlang "golang.org/x/text/language"

	"github.com/hejmo-home/hejmo/internal/contact"
	"github.com/hejmo-home/hejmo/internal/store"
)

// Relations between contacts and people (ticket 298, ADR 0021): the words, the sentence, the form lines and the
// "Kontakte von Mia" section of a person's page.

// peopleChips is the people as chips in household order.
func (e ctEnv) peopleChips() []personChip { return chipsOf(e.people) }

// relNames are the names of the people with these ids, in the order of the people of the household.
func (e ctEnv) relNames(ids []string) []string {
	var out []string
	for _, c := range e.peopleChips() {
		if slices.Contains(ids, c.ID) {
			out = append(out, c.Name)
		}
	}
	return out
}

// relSentence is how a relation reads: "Oma von Sebastian, Mia und Ben", "Nachbarin der ganzen Familie".
func (e ctEnv) relSentence(word string, names []string, whole bool) string {
	word = strings.TrimSpace(word)
	if whole {
		return word + " " + e.t("contact.rel.whole")
	}
	return word + " " + e.t("contact.rel.of") + " " + contact.JoinNames(names, e.t("contact.rel.and"))
}

// relSentences are the sentences of a contact's relations. For a person only the relations that have the person
// in them or are for the whole family are kept, each naming only that person.
func (e ctEnv) relSentences(c store.Contact, forPerson string) []string {
	var out []string
	for _, r := range c.Relations {
		switch {
		case r.WholeFamily:
			out = append(out, e.relSentence(r.Word, nil, true))
		case forPerson == "":
			out = append(out, e.relSentence(r.Word, e.relNames(r.PersonIDs), false))
		case slices.Contains(r.PersonIDs, forPerson):
			out = append(out, e.relSentence(r.Word, e.relNames([]string{forPerson}), false))
		}
	}
	return out
}

// ctWho is a person in the head of a contact page, with what follows the name.
type ctWho struct {
	Chip personChip
	Sep  string
}

// ctRelItem is a relation in the head of a contact page.
type ctRelItem struct {
	Word, Of, WholeText string
	Whole               bool
	Who                 []ctWho
}

func (e ctEnv) relItems(c store.Contact) []ctRelItem {
	var out []ctRelItem
	for _, r := range c.Relations {
		it := ctRelItem{Word: r.Word, Of: e.t("contact.rel.of"), WholeText: e.t("contact.rel.whole"), Whole: r.WholeFamily}
		var chips []personChip
		for _, ch := range e.peopleChips() {
			if slices.Contains(r.PersonIDs, ch.ID) {
				chips = append(chips, ch)
			}
		}
		for i, ch := range chips {
			sep := ""
			switch {
			case i == len(chips)-2:
				sep = " " + e.t("contact.rel.and") + " "
			case i < len(chips)-2:
				sep = ", "
			}
			it.Who = append(it.Who, ctWho{ch, sep})
		}
		out = append(out, it)
	}
	return out
}

// relDeleteExplain is the sentence of the danger box: what goes with the contact.
func (e ctEnv) relDeleteExplain(c store.Contact) string {
	if len(c.Relations) == 0 {
		return e.t("contact.delete.explain", "name", c.HejmoName)
	}
	var ids []string
	whole := false
	for _, r := range c.Relations {
		whole = whole || r.WholeFamily
		ids = append(ids, r.PersonIDs...)
	}
	names := e.relNames(ids)
	var who string
	switch {
	case whole && len(names) == 0:
		who = e.t("contact.delete.to.whole")
	default:
		if whole {
			names = append(names, e.t("contact.delete.item.whole"))
		}
		who = e.t("contact.delete.to", "names", contact.JoinNames(names, e.t("contact.rel.and")))
	}
	key := "contact.delete.explain.one"
	if len(c.Relations) > 1 || len(names) > 1 {
		key = "contact.delete.explain.many"
	}
	return e.t(key, "name", c.HejmoName, "who", who)
}

// ctRelView is a relation line of the form with its field names, ready for the page.
type ctRelView struct {
	N                                           int
	ID, Word                                    string
	Selected                                    []string
	Blank                                       bool
	Err, Say                                    string
	WordName, WholeName, PersonName, AriaRemove string
}

func (e ctEnv) relViews(lines []ctRelForm, errs map[string]string) []ctRelView {
	if len(lines) == 0 {
		lines = []ctRelForm{{Whole: true}}
	}
	var out []ctRelView
	for i, l := range lines {
		n := strconv.Itoa(i + 1)
		v := ctRelView{N: i + 1, ID: "rel" + n, Word: l.Word, Selected: l.People, Err: errs["rel"+n],
			Blank:    len(lines) == 1 && strings.TrimSpace(l.Word) == "" && l.Whole,
			WordName: "rel" + n + "_word", WholeName: "rel" + n + "_whole", PersonName: "rel" + n + "_person", AriaRemove: e.t("contact.aria.rel.remove", "n", n)}
		if strings.TrimSpace(l.Word) != "" && (l.Whole || len(l.People) > 0) {
			v.Say = e.relSentence(l.Word, e.relNames(l.People), l.Whole)
		}
		out = append(out, v)
	}
	return out
}

// relTemplate is an empty line with {n} where its number goes, for the script to add a line without a round trip.
func (e ctEnv) relTemplate() ctRelView {
	return ctRelView{ID: "rel{n}", Selected: nil, WordName: "rel{n}_word", WholeName: "rel{n}_whole", PersonName: "rel{n}_person", AriaRemove: e.t("contact.aria.rel.remove", "n", "{n}")}
}

// relWords are the words the form suggests: the shipped ones, then the words the household uses already.
func (e ctEnv) relWords(all []store.Contact) []string {
	out := strings.Split(e.t("contact.rel.words"), ",")
	have := map[string]bool{}
	for _, w := range out {
		have[strings.ToLower(w)] = true
	}
	var own []string
	for _, c := range all {
		for _, r := range c.Relations {
			if k := strings.ToLower(r.Word); !have[k] {
				have[k] = true
				own = append(own, r.Word)
			}
		}
	}
	coll := collate.New(textlang.Make(e.lang))
	slices.SortFunc(own, coll.CompareString)
	return append(out, own...)
}

// knownPerson answers the id when it is a person of the household, else "".
func (e ctEnv) knownPerson(id string) string {
	if slices.ContainsFunc(e.people, func(p store.Person) bool { return p.ID == id }) {
		return id
	}
	return ""
}

// dropVanishedPeople takes people who left the household out of the relation lines; a line that had people and has
// none left goes (it must not turn into "whole family").
func (e ctEnv) dropVanishedPeople(f *ctForm) {
	var kept []ctRelForm
	for _, l := range f.Rels {
		had := len(l.People) > 0
		l.People = slices.DeleteFunc(slices.Clone(l.People), func(id string) bool { return e.knownPerson(id) == "" })
		if had && len(l.People) == 0 {
			continue
		}
		kept = append(kept, l)
	}
	f.Rels = kept
	f.For = e.knownPerson(f.For)
}

// personContacts is the section "Kontakte von Mia" of a person's page: the contacts related to the person, A to Z,
// each with only this person's relation, filtered by the groups that occur among them.
func (s *server) personContacts(r *http.Request, p principal, people []store.Person, person store.Person) (map[string]any, error) {
	groups, err := s.db.ContactGroups(p.household.ID)
	if err != nil {
		return nil, err
	}
	all, err := s.db.Contacts(p.household.ID)
	if err != nil {
		return nil, err
	}
	e := ctEnv{s: s, lang: s.langOf(r), p: p, people: people, chips: map[string]personChip{}, groups: groups}
	for _, c := range chipsOf(people) {
		e.chips[c.ID] = c
	}
	var related []store.Contact
	present := map[string]bool{}
	for _, c := range all {
		if c.IsPerson() || !slices.ContainsFunc(c.Relations, func(r store.Relation) bool { return r.WholeFamily || slices.Contains(r.PersonIDs, person.ID) }) {
			continue
		}
		related = append(related, c)
		for _, g := range c.GroupIDs {
			present[g] = true
		}
	}
	coll := collate.New(textlang.Make(e.lang))
	slices.SortStableFunc(related, func(a, b store.Contact) int { return coll.CompareString(a.HejmoName, b.HejmoName) })
	base := "/people/" + person.ID
	out := map[string]any{
		"Head": e.t("contact.person.contacts", "name", person.Name), "NewHref": contactsPath + "/new?" + url.Values{"for": {person.ID}}.Encode(),
		"NewText": e.t("contact.person.new", "name", person.Name), "AllText": e.t("contact.person.all"),
	}
	if len(related) == 0 {
		out["Empty"] = e.t("contact.person.contacts.empty", "name", person.Name, "possessive", possessive(e.lang, person.Name))
		return out, nil
	}
	out["Lead"] = e.t("contact.person.contacts.lead", "name", person.Name)
	g := r.URL.Query().Get("g")
	if !present[g] {
		g = ""
	}
	var shown []ctRow
	for _, c := range related {
		if g != "" && !slices.Contains(c.GroupIDs, g) {
			continue
		}
		row := e.row(c, true)
		row.Rels = e.relSentences(c, person.ID)
		shown = append(shown, row)
	}
	out["Rows"] = shown
	if len(present) >= 2 {
		filters := []map[string]any{{"Href": base + "#contacts", "Text": e.t("contact.filter.all"), "Current": g == ""}}
		for _, grp := range groups {
			if present[grp.ID] {
				filters = append(filters, map[string]any{"Href": "?g=" + grp.ID + "#contacts", "Text": e.groupName(grp), "Current": g == grp.ID})
			}
		}
		out["Filters"], out["FilterLabel"] = filters, e.t("contact.person.contacts.filter", "name", person.Name)
	}
	return out, nil
}
