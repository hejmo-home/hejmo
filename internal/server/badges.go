package server

import (
	"errors"
	"html"
	"html/template"
	"net/http"
	"strings"
	"time"

	"github.com/hejmo-home/hejmo/internal/badge"
	"github.com/hejmo-home/hejmo/internal/familyview"
	"github.com/hejmo-home/hejmo/internal/i18n"
	"github.com/hejmo-home/hejmo/internal/store"
)

// The badges pages (ticket 295, preview-295): the child's own page at
// /tasks/badges and the parents' page of one person at /tasks/badges/{person}.
// Nothing is shown while the person's switch "week count and badges" is off,
// and badges are never awarded or removed by hand here.

// badgeItem is one badge as a page shows it.
type badgeItem struct {
	Key, Name, How, When string
	New                  bool
}

// badgeName and badgeHow are the texts of a badge in the language.
func (s *server) badgeName(lang string, k badge.Key) string {
	return s.i18n.T(lang, "badge."+string(k)+".name")
}

func (s *server) badgeHow(lang string, k badge.Key) string {
	return s.i18n.T(lang, "badge."+string(k)+".how")
}

// householdLoc is the household's time zone.
func householdLoc(h store.Household) *time.Location {
	return principal{household: h}.location()
}

// badgeWhen says when a badge was earned: "Got it today", "Got it yesterday,
// Tuesday 11/24" or "Got it Wednesday 11/18", days in the household's time zone.
func (s *server) badgeWhen(lang string, h store.Household, earned, now time.Time) string {
	loc := householdLoc(h)
	e, n := earned.In(loc), now.In(loc)
	ey, em, ed := e.Date()
	ny, nm, nd := n.Date()
	switch {
	case ey == ny && em == nm && ed == nd:
		return s.i18n.T(lang, "badge.when.today")
	case time.Date(ey, em, ed+1, 0, 0, 0, 0, loc).Equal(time.Date(ny, nm, nd, 0, 0, 0, 0, loc)):
		return s.i18n.Format(lang, i18n.Msg("badge.when.yesterday", "date", s.dayLabel(lang, h.DateFormat, e)))
	}
	return s.i18n.Format(lang, i18n.Msg("badge.when.on", "date", s.dayLabel(lang, h.DateFormat, e)))
}

// badgeItems turns earned badges, newest first, into what the pages show.
func (s *server) badgeItems(h store.Household, lang string, list []store.Badge) []badgeItem {
	now := s.now()
	loc := householdLoc(h)
	out := make([]badgeItem, 0, len(list))
	for _, b := range list {
		out = append(out, badgeItem{Key: string(b.Key), Name: s.badgeName(lang, b.Key), How: s.badgeHow(lang, b.Key),
			When: s.badgeWhen(lang, h, b.EarnedAt, now), New: b.New(now, loc)})
	}
	return out
}

// newBadges are the badges of items that count as new.
func newBadges(items []badgeItem) []badgeItem {
	var out []badgeItem
	for _, it := range items {
		if it.New {
			out = append(out, it)
		}
	}
	return out
}

// shownBadges is the badges of a person for the screens that show them: none
// while the switch is off.
func (s *server) shownBadges(h store.Household, lang string, person store.Person) ([]badgeItem, error) {
	if !person.StreaksShown {
		return nil, nil
	}
	list, err := s.db.Badges(h.ID, person.ID)
	if err != nil {
		return nil, err
	}
	return s.badgeItems(h, lang, list), nil
}

// personalBadges are the badges for the foot of a person's view on a display:
// the new ones first, then the rest, newest first. Empty with the switch off.
func (s *server) personalBadges(h store.Household, lang string, person store.Person) (fresh, rest []familyview.Badge, err error) {
	items, err := s.shownBadges(h, lang, person)
	if err != nil {
		return nil, nil, err
	}
	for _, it := range items {
		b := familyview.Badge{Key: it.Key, Name: it.Name}
		if it.New {
			fresh = append(fresh, b)
		} else {
			rest = append(rest, b)
		}
	}
	return fresh, rest, nil
}

// handleBadges is the badges page of a child on their own device.
func (s *server) handleBadges(w http.ResponseWriter, r *http.Request) {
	p, ok := s.memberPage(w, r)
	if !ok {
		return
	}
	if p.person.Role != store.RoleChild { // parents see the badges of a person at /tasks/badges/{person}
		http.NotFound(w, r)
		return
	}
	s.renderBadges(w, r, p, p.person, false)
}

// handleBadgesOf is the badges page of one person for the parents.
func (s *server) handleBadgesOf(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	person, err := s.db.Person(p.household.ID, r.PathValue("person"))
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.fail(w, "person", err)
		return
	}
	s.renderBadges(w, r, p, person, true)
}

func (s *server) renderBadges(w http.ResponseWriter, r *http.Request, p principal, person store.Person, parent bool) {
	lang := s.langOf(r)
	people, err := s.db.People(p.household.ID)
	if err != nil {
		s.fail(w, "people", err)
		return
	}
	chip := chipOf(chipsOf(people), person.ID)
	title := s.i18n.T(lang, "badge.title")
	if parent {
		title = s.i18n.Format(lang, i18n.Msg("badge.page.parent", "name", person.Name, "possessive", possessive(lang, person.Name)))
	}
	data := map[string]any{
		"Person": p.person, "SyncStale": s.syncStale(p.household.ID), "PageTitle": title, "Parent": parent, "Name": person.Name, "Slot": chip.Slot, "Shape": chip.Shape,
		"Shown": person.StreaksShown, "BackHref": "/tasks", "BackText": s.i18n.T(lang, "badge.back"),
	}
	link := `<a href="/people/` + html.EscapeString(person.ID) + `">` + html.EscapeString(s.i18n.Format(lang, i18n.Msg("badge.lead.parent.link", "name", person.Name, "possessive", possessive(lang, person.Name)))) + `</a>`
	if !person.StreaksShown {
		switch {
		case parent:
			data["Off"] = withLink(s.i18n.Format(lang, i18n.Msg("badge.off.parent", "name", person.Name, "link", "\x00")), link)
		default:
			parents := s.parentNames(people)
			key := "badge.off.many"
			switch len(parents) {
			case 0:
				key = "badge.off.none"
			case 1:
				key = "badge.off.one"
			}
			data["Off"] = template.HTML(html.EscapeString(s.i18n.Format(lang, i18n.Msg(key, "parents", s.joinNames(lang, "rwd.or", parents)))))
		}
		s.render(w, r, http.StatusOK, "badges.html", data)
		return
	}
	list, err := s.db.Badges(p.household.ID, person.ID)
	if err != nil {
		s.fail(w, "badges", err)
		return
	}
	have := s.badgeItems(p.household, lang, list)
	earned := map[string]bool{}
	for _, it := range have {
		earned[it.Key] = true
	}
	hasRoutine, hasStars, hasRewards, err := s.badgeReach(p.household.ID, person.ID)
	if err != nil {
		s.fail(w, "badge reach", err)
		return
	}
	var open []badgeItem
	for _, k := range badge.Catalogue {
		if !earned[string(k)] && badge.Reachable(k, hasRoutine, hasStars, hasRewards) {
			open = append(open, badgeItem{Key: string(k), Name: s.badgeName(lang, k), How: s.badgeHow(lang, k)})
		}
	}
	empty := "none"
	switch {
	case hasRoutine:
		empty = "routine"
	case hasStars:
		empty = "stars"
	case hasRewards:
		empty = "rewards"
	}
	data["Have"], data["Open"], data["All"] = have, open, len(have) == len(badge.Catalogue)
	args := []string{"name", person.Name}
	if parent {
		data["HaveHead"], data["MoreHead"] = s.i18n.T(lang, "badge.group.have.parent"), s.i18n.T(lang, "badge.group.more.parent")
		data["Hint"] = s.i18n.Format(lang, i18n.Msg("badge.hint.parent", args...))
		data["Empty"] = s.i18n.Format(lang, i18n.Msg("badge.empty.parent."+empty, args...))
		data["AllText"] = s.i18n.Format(lang, i18n.Msg("badge.all.parent", args...))
		data["Lead"] = withLink(s.i18n.Format(lang, i18n.Msg("badge.lead.parent", "name", person.Name, "link", "\x00")), link)
	} else {
		data["HaveHead"], data["MoreHead"] = s.i18n.T(lang, "badge.group.have"), s.i18n.T(lang, "badge.group.more")
		data["Hint"] = s.i18n.T(lang, "badge.hint")
		data["Empty"] = s.i18n.T(lang, "badge.empty."+empty)
		data["AllText"] = s.i18n.T(lang, "badge.all")
		data["Lead"] = template.HTML(html.EscapeString(s.i18n.T(lang, "badge.lead")))
	}
	s.render(w, r, http.StatusOK, "badges.html", data)
}

// withLink escapes a text that holds one \x00 where the link goes and puts the
// (already escaped) link markup there.
func withLink(text, link string) template.HTML {
	return template.HTML(strings.Replace(html.EscapeString(text), "\x00", link, 1)) // #nosec G203 -- the text is escaped, the link is built from escaped parts
}

// badgeReach says which kinds of badges a person can still earn: routine badges
// need a routine, star badges a task worth stars or a star booking, reward
// badges a reward meant for them.
func (s *server) badgeReach(householdID, personID string) (routine, stars, rewards bool, err error) {
	routines, err := s.db.Routines(householdID)
	if err != nil {
		return false, false, false, err
	}
	for _, r := range routines {
		if r.PersonID == personID {
			routine = true
		}
	}
	sums, err := s.db.StarSummaries(householdID)
	if err != nil {
		return false, false, false, err
	}
	stars = sums[personID].Shown
	rewards, err = s.db.HasRewards(householdID, personID)
	return routine, stars, rewards, err
}

// badgeSub is the sub-line of a child's link to the badges: the new ones named
// ("New: A and B"), else the newest one, else what the page is for.
func (s *server) badgeSub(lang string, items []badgeItem) template.HTML {
	fresh := newBadges(items)
	if len(fresh) > 0 {
		parts := make([]string, len(fresh))
		for i, it := range fresh {
			parts[i] = `<b class="g-inline">` + html.EscapeString(it.Name) + `</b>`
		}
		names := parts[len(parts)-1]
		if len(parts) > 1 {
			names = strings.Join(parts[:len(parts)-1], ", ") + html.EscapeString(s.i18n.T(lang, "badge.link.and")) + names
		}
		return template.HTML(strings.Replace(html.EscapeString(s.i18n.T(lang, "badge.link.new")), "{names}", names, 1)) // #nosec G203 -- the names are escaped, the text is ours
	}
	if len(items) > 0 {
		return formatHTML(s.i18n, lang, "badge.link.newest", "name", items[0].Name)
	}
	return template.HTML(html.EscapeString(s.i18n.T(lang, "badge.link.none")))
}

type badgeJSON struct {
	Key      string `json:"key"`
	Name     string `json:"name"`
	EarnedAt string `json:"earned_at"`
	New      bool   `json:"new"`
	HowToGet string `json:"how"`
}

type badgesJSON struct {
	PersonID     string      `json:"person_id"`
	StreaksShown bool        `json:"streaks_shown"`
	Badges       []badgeJSON `json:"badges"`
}

// apiGetBadges lists a person's badges, newest first (ticket 295). Read only:
// badges are awarded by what happens, never by hand. While the person's
// switch "week count and badges" is off the list is empty, as everywhere else;
// the badges stay stored.
func (s *server) apiGetBadges(w http.ResponseWriter, r *http.Request, p principal) {
	person, err := s.db.Person(p.household.ID, r.PathValue("id"))
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "person not found")
		return
	case err != nil:
		s.fail(w, "person", err)
		return
	}
	lang := s.langOf(r)
	out := badgesJSON{PersonID: person.ID, StreaksShown: person.StreaksShown, Badges: []badgeJSON{}}
	if person.StreaksShown {
		list, err := s.db.Badges(p.household.ID, person.ID)
		if err != nil {
			s.fail(w, "badges", err)
			return
		}
		loc := p.location()
		for _, b := range list {
			out.Badges = append(out.Badges, badgeJSON{Key: string(b.Key), Name: s.badgeName(lang, b.Key), HowToGet: s.badgeHow(lang, b.Key),
				EarnedAt: b.EarnedAt.In(loc).Format(time.RFC3339), New: b.New(s.now(), loc)})
		}
	}
	writeJSON(w, http.StatusOK, out)
}
