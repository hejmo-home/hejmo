package server

import (
	"strconv"
	"strings"

	"github.com/hejmo-home/hejmo/internal/contact"
	"github.com/hejmo-home/hejmo/internal/store"
)

// "Wichtige Nummern" for children (ticket 301): the switch of a contact and what the parents see of it.

// childNames are the names of the children of the household, in the order of the people.
func (e ctEnv) childNames() []string {
	var out []string
	for _, p := range e.people {
		if p.Role == store.RoleChild {
			out = append(out, p.Name)
		}
	}
	return out
}

// ctKidsCard is the card "Wichtige Nummern für Kinder" of the contact form: the switch with its state in words, what
// the children see once it is on, and the notice for a switch that is on without a number.
type ctKidsCard struct {
	On                  bool
	OnText, OffText     string
	Sees, NoNumber      string // one of them: the sentence of what they see, or the notice
	Switch, Title, Hint string
}

func (e ctEnv) kidsCard(f ctForm, hasPhoto bool) ctKidsCard {
	card := ctKidsCard{On: f.ForChildren, OffText: e.t("contact.kids.off"), Switch: e.t("contact.kids.switch"), Title: e.t("contact.kids.title"), Hint: e.t("contact.kids.hint")}
	switch names := e.childNames(); len(names) {
	case 0:
		card.OnText = e.t("contact.kids.on.none")
	case 1:
		card.OnText = e.t("contact.kids.on.one", "names", names[0])
	default:
		card.OnText = e.t("contact.kids.on.many", "names", contact.JoinNames(names, e.t("contact.rel.and")))
	}
	var kinds []string
	for _, l := range f.Numbers {
		if line := l.line(true); strings.TrimSpace(line.Value) != "" {
			kinds = append(kinds, e.kindWord(line, false))
		}
	}
	name := strings.TrimSpace(f.Name)
	if len(kinds) == 0 {
		if name == "" {
			card.NoNumber = e.t("contact.kids.nonumber.unnamed")
		} else {
			card.NoNumber = e.t("contact.kids.nonumber", "name", name)
		}
		return card
	}
	nameText := e.t("contact.kids.sees.name.empty")
	if name != "" {
		nameText = e.t("contact.kids.sees.name", "name", name)
	}
	numKey := "contact.kids.sees.numbers.many"
	if len(kinds) == 1 {
		numKey = "contact.kids.sees.numbers.one"
	}
	numbers := e.t(numKey, "kinds", contact.JoinNames(kinds, e.t("contact.rel.and")))
	key := "contact.kids.sees.nophoto"
	if hasPhoto {
		key = "contact.kids.sees.photo"
	}
	card.Sees = e.t(key, "name", nameText, "numbers", numbers)
	return card
}

// kidsFact is the fact "Für Kinder" of the contact page.
func (e ctEnv) kidsFact(c store.Contact) string {
	switch n := len(c.Numbers); {
	case !c.ForChildren:
		return e.t("contact.fact.kids.off")
	case n == 0:
		return e.t("contact.fact.kids.wait")
	default:
		var count string
		switch n {
		case 1:
			count = e.t("contact.fact.kids.n.one")
		case 2:
			count = e.t("contact.fact.kids.n.two")
		default:
			count = e.t("contact.fact.kids.n.many", "n", e.t("contact.word."+strconv.Itoa(n)))
		}
		key := "contact.fact.kids.on.nophoto"
		if c.HasPhoto {
			key = "contact.fact.kids.on.photo"
		}
		return e.t(key, "numbers", count)
	}
}

// kidsFlash is the key of the success line after a save that changed what the children see: "" when nothing changed
// for them. before is the contact as it was (nil for a new one).
func kidsFlash(before *store.Contact, after store.Contact, newPhoto bool) string {
	wasShown := before != nil && before.ForChildren && len(before.Numbers) > 0
	nowShown := after.ForChildren && len(after.Numbers) > 0
	var code string
	switch {
	case nowShown && !wasShown:
		code = "on"
	case after.ForChildren && len(after.Numbers) == 0 && (before == nil || !before.ForChildren || len(before.Numbers) > 0):
		code = "wait"
	case !after.ForChildren && wasShown:
		code = "off"
	default:
		return ""
	}
	if newPhoto {
		code += "_photo"
	}
	return "flash.contact_saved_kids_" + code
}
