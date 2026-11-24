package contact

import "slices"

// KidCandidate is a contact as the page "Wichtige Nummern" for children sees it (ticket 301). Candidates come in the
// order of the store: people first in the order of the people, then the others by creation.
type KidCandidate struct {
	ID, Name string
	Person   bool // a person of the household
	Visible  bool // switched on for the children
	Numbers  int  // how many phone numbers
	Notfall  bool // in the group Notfall
	Familie  bool // in the group Familie
}

// KidSectionIDs are the contact ids of the three sections, in the order they are shown.
type KidSectionIDs struct{ Emergency, Family, Other []string }

// Empty reports whether no section holds a contact.
func (k KidSectionIDs) Empty() bool {
	return len(k.Emergency)+len(k.Family)+len(k.Other) == 0
}

// KidSections sorts the contacts the children may call into "Im Notfall" (group Notfall, in the given order),
// "Familie" (the people in the given order, then the group Familie A to Z) and "Weitere Nummern" (A to Z). A contact
// stands once, in the first section that fits. Contacts that are not switched on, have no number or are `own`
// (the contact of the child who looks) are left out. cmp compares names in the language of the page.
func KidSections(in []KidCandidate, own string, cmp func(a, b string) int) KidSectionIDs {
	var out KidSectionIDs
	var fam, other []KidCandidate
	for _, c := range in {
		if !c.Visible || c.Numbers == 0 || c.ID == own {
			continue
		}
		switch {
		case c.Notfall:
			out.Emergency = append(out.Emergency, c.ID)
		case c.Person:
			out.Family = append(out.Family, c.ID)
		case c.Familie:
			fam = append(fam, c)
		default:
			other = append(other, c)
		}
	}
	byName := func(a, b KidCandidate) int { return cmp(a.Name, b.Name) }
	slices.SortStableFunc(fam, byName)
	slices.SortStableFunc(other, byName)
	for _, c := range fam {
		out.Family = append(out.Family, c.ID)
	}
	for _, c := range other {
		out.Other = append(out.Other, c.ID)
	}
	return out
}
