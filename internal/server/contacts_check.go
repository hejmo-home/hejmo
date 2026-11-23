package server

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/hejmo-home/hejmo/internal/contact"
	"github.com/hejmo-home/hejmo/internal/store"
)

// ctDraft is a contact as a person typed it, before anything is checked. The web form and the REST API
// both build one, so they follow the same rules (ticket 296).
type ctDraft struct {
	Name, Full                      string
	Phones, Emails                  []contact.Line
	Street, Postcode, City, Country string
	Day, Month, Year                int
	BdayNotNumbers                  bool // the form held something that is no number
	Note, NewGroup                  string
	GroupIDs                        []string
	IsPerson                        bool
}

// ctProblem is one thing to put right. Anchor is the id of the part of the form it belongs to, Field the name of
// the field in the page's errors (and in the API's message), N the 1-based line.
type ctProblem struct {
	Anchor, Field string
	N             int
	Code, Arg     string
}

func (p ctProblem) String() string {
	f := p.Field
	if p.N > 0 {
		f += "[" + strconv.Itoa(p.N) + "]"
	}
	if p.Arg != "" {
		return fmt.Sprintf("%s: %s %q", f, p.Code, p.Arg)
	}
	return f + ": " + p.Code
}

// checkContact applies the rules of the address book to a draft, in the order of the form. taken answers
// whether a Hejmo name is in use (case-insensitively) and by whom; groupTaken the same for a new group.
func checkContact(d ctDraft, today time.Time, taken func(name string) *store.NameTakenError, groupTaken func(name string) bool) []ctProblem {
	var out []ctProblem
	add := func(anchor, field string, n int, code, arg string) {
		out = append(out, ctProblem{Anchor: anchor, Field: field, N: n, Code: code, Arg: arg})
	}
	if !d.IsPerson {
		name := strings.TrimSpace(d.Name)
		switch {
		case name == "":
			add("name", "name", 0, "required", "")
		case contact.Runes(name) > contact.MaxName:
			add("name", "name", 0, "long", "")
		default:
			if t := taken(name); t != nil {
				add("name", "name", 0, "taken", t.Existing)
				if t.ExistingIsPerson {
					out[len(out)-1].Code = "taken_person"
				}
			}
		}
	}
	if contact.Runes(strings.TrimSpace(d.Full)) > contact.MaxFullName {
		add("name", "full_name", 0, "long", "")
	}
	for _, set := range []struct {
		lines          []contact.Line
		phone          bool
		field, anchor  string
		many, manyName string
	}{
		{d.Phones, true, "phone", "phone", "phones", "phones"},
		{d.Emails, false, "email", "email", "emails", "mails"},
	} {
		filled := 0
		for i, l := range set.lines {
			n := i + 1
			value := strings.TrimSpace(l.Value)
			label := strings.TrimSpace(l.Label)
			if value == "" {
				continue // an empty line is never stored, so it is never wrong
			}
			filled++
			id := set.anchor + strconv.Itoa(n)
			if set.phone {
				if code, bad := contact.CheckPhone(value); code != "" {
					add(id, set.field, n, code, bad)
				}
			} else if code := contact.CheckEmail(value); code != "" {
				add(id, set.field, n, code, "")
			}
			if l.Kind == contact.Custom {
				switch {
				case label == "":
					add(id, set.field+"_own", n, "label_required", "")
				case contact.Runes(label) > contact.MaxOwnLabel:
					add(id, set.field+"_own", n, "label_long", "")
				}
			}
		}
		if filled > contact.MaxLines {
			add(set.many, set.many, 0, "many", "")
		}
	}
	switch {
	case d.BdayNotNumbers:
		add("birthday", "birthday", 0, "date", "")
	default:
		if code := contact.CheckBirthday(d.Day, d.Month, d.Year, today); code != "" {
			p := ctProblem{Anchor: "birthday", Field: "birthday", Code: code}
			if code == "date" && d.Day != 0 && d.Month >= 1 && d.Month <= 12 {
				p.Arg = strconv.Itoa(d.Day) + "." + strconv.Itoa(d.Month)
				if d.Year != 0 {
					p.Arg += "." + strconv.Itoa(d.Year)
				}
			}
			out = append(out, p)
		}
	}
	if contact.Runes(strings.TrimSpace(d.Note)) > contact.MaxNote {
		add("note", "note", 0, "long", "")
	}
	if g := strings.TrimSpace(d.NewGroup); g != "" {
		switch {
		case contact.Runes(g) > contact.MaxName:
			add("groups", "new_group", 0, "long", "")
		case groupTaken(g):
			add("groups", "new_group", 0, "taken", g)
		}
	}
	return out
}

// input is the draft as the store takes it.
func (d ctDraft) input() store.ContactInput {
	return store.ContactInput{
		HejmoName: d.Name, FullName: d.Full, Street: d.Street, Postcode: d.Postcode, City: d.City, Country: d.Country,
		BdayDay: d.Day, BdayMonth: d.Month, BdayYear: d.Year, Note: d.Note,
		Phones: d.Phones, Emails: d.Emails, GroupIDs: d.GroupIDs, NewGroup: d.NewGroup,
	}
}
