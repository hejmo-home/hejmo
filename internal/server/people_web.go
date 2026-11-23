package server

import (
	"errors"
	"net/http"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/hejmo-home/hejmo/internal/i18n"
	"github.com/hejmo-home/hejmo/internal/store"
)

// personRow is a person on the people page: the chip, the role and where they
// stand in the order of the family view.
type personRow struct {
	personChip
	Role        store.Role
	First, Last bool
	// Acc says in words whether an adult can sign in (ticket 145).
	Acc accLine
	// Food is the diet and allergies in words (ticket 163, ADR 0019).
	Food foodView
}

// accLine is the account state in the role line of the people overview: for
// yourself "That's you", for another adult the state in words and, where there
// is something to do, a link.
type accLine struct {
	Self                  bool
	State, Href, LinkText string
}

type groupRow struct {
	ID, Name string
	Members  []personChip
}

// colorOption is one colour of the choice. Users names the other people who
// wear it (empty: free); TakenWith lists the markers they wear it with, and Off
// is set when the pair with the selected marker would be refused.
type colorOption struct {
	Hex       string
	Slot      int
	Users     string
	TakenWith string
	Off       bool
}

// shapeOption is one marker of the choice, like colorOption.
type shapeOption struct {
	Shape     string
	Users     string
	TakenWith string
	Off       bool
}

// lookData is what the colour and marker choice of a form needs: the options
// with who uses them. exceptID is the person being changed (their own look is
// not "taken"); data["Form"] holds the selected Color and Shape. The selected
// option is never disabled, so a refused pair can be sent again and shows its
// error.
func lookData(people []store.Person, exceptID string, data map[string]any) map[string]any {
	form, _ := data["Form"].(map[string]any)
	color, _ := form["Color"].(string)
	shape, _ := form["Shape"].(string)
	colors := make([]colorOption, len(personColors))
	for i, c := range personColors {
		o := colorOption{Hex: c, Slot: i + 1}
		var users, shapes []string
		for i, p := range people {
			if dc, ds := displayedLook(p, i); p.ID != exceptID && dc == c {
				users = append(users, p.Name)
				shapes = append(shapes, ds)
			}
		}
		o.Users, o.TakenWith = strings.Join(users, ", "), strings.Join(shapes, " ")
		o.Off = !strings.EqualFold(c, color) && styleTaken(people, exceptID, c, shape)
		colors[i] = o
	}
	shapes := make([]shapeOption, len(markerShapes))
	for i, sh := range markerShapes {
		o := shapeOption{Shape: sh}
		var users, cols []string
		for i, p := range people {
			if dc, ds := displayedLook(p, i); p.ID != exceptID && ds == sh {
				users = append(users, p.Name)
				cols = append(cols, dc)
			}
		}
		o.Users, o.TakenWith = strings.Join(users, ", "), strings.Join(cols, " ")
		o.Off = sh != shape && styleTaken(people, exceptID, color, sh)
		shapes[i] = o
	}
	data["Colors"], data["Shapes"] = colors, shapes
	return data
}

func (s *server) peopleOf(w http.ResponseWriter, p principal) ([]store.Person, bool) {
	people, err := s.db.People(p.household.ID)
	if err != nil {
		s.fail(w, "people", err)
	}
	return people, err == nil
}

func (s *server) renderPeople(w http.ResponseWriter, r *http.Request, p principal, status int, form map[string]any) {
	people, ok := s.peopleOf(w, p)
	if !ok {
		return
	}
	groups, err := s.db.Groups(p.household.ID)
	if err != nil {
		s.fail(w, "groups", err)
		return
	}
	chips := chipsOf(people)
	lang := s.langOf(r)
	foods, err := s.db.HouseholdFood(p.household.ID)
	if err != nil {
		s.fail(w, "food", err)
		return
	}
	rows := make([]personRow, len(people))
	byID := map[string]personChip{}
	for i, c := range chips {
		rows[i] = personRow{personChip: c, Role: people[i].Role, First: i == 0, Last: i == len(people)-1, Food: s.foodViewOf(lang, foods[c.ID])}
		if people[i].Role == store.RoleParent {
			line, err := s.accLineOf(r, p, people[i])
			if err != nil {
				s.fail(w, "sign-in state", err)
				return
			}
			rows[i].Acc = line
		}
		byID[c.ID] = c
	}
	var subgroups []groupRow
	for _, g := range groups {
		row := groupRow{ID: g.ID, Name: g.Name}
		for _, id := range g.Members {
			if c, ok := byID[id]; ok {
				row.Members = append(row.Members, c)
			}
		}
		subgroups = append(subgroups, row)
	}
	if form == nil {
		color, shape, _ := freeStyle(people, "", "")
		form = map[string]any{"Role": string(store.RoleChild), "Color": color, "Shape": shape}
	}
	s.render(w, r, status, "people.html", lookData(people, "", map[string]any{
		"Person": p.person, "SyncStale": s.syncStale(p.household.ID), "People": rows, "Groups": subgroups,
		"Form": form, "Error": form["Error"], "ErrorField": form["ErrorField"],
	}))
}

// accLineOf words an adult's sign-in state for the people overview.
func (s *server) accLineOf(r *http.Request, p principal, person store.Person) (accLine, error) {
	if person.ID == p.person.ID {
		return accLine{Self: true}, nil
	}
	st, err := s.signInOf(p.household.ID, person)
	if err != nil {
		return accLine{}, err
	}
	lang := s.langOf(r)
	href := "/people/" + person.ID + "/invitation"
	invite := accLine{Href: href, LinkText: s.i18n.Format(lang, i18n.Msg("people.acc.invite", "name", person.Name))}
	switch st.Kind {
	case signInOpen:
		return accLine{State: s.i18n.T(lang, "people.acc.open"), Href: href, LinkText: s.i18n.T(lang, "people.acc.view")}, nil
	case signInExpired:
		invite.State = s.i18n.T(lang, "people.acc.expired")
		return invite, nil
	case signInAccount:
		if st.OTPSet {
			return accLine{State: s.i18n.T(lang, "people.acc.can")}, nil
		}
		return accLine{State: s.i18n.T(lang, "people.acc.otp_missing")}, nil
	}
	invite.State = s.i18n.T(lang, "people.acc.none")
	return invite, nil
}

func (s *server) handlePeoplePage(w http.ResponseWriter, r *http.Request) {
	if p, ok := s.parentPage(w, r); ok {
		s.renderPeople(w, r, p, http.StatusOK, nil)
	}
}

func (s *server) handlePersonAdd(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	people, ok := s.peopleOf(w, p)
	if !ok {
		return
	}
	look := personInput{r.PostFormValue("name"), r.PostFormValue("color"), r.PostFormValue("shape")}
	role := store.Role(r.PostFormValue("role"))
	field, key := checkLook(people, "", &look, nil)
	if field == "" && !role.Valid() {
		field, key = "role", "people.error.role"
	}
	if field == "" {
		var free bool
		if look.Color, look.Shape, free = freeStyle(people, look.Color, look.Shape); !free {
			field, key = "shape", "people.error.full"
		}
	}
	if field != "" {
		s.renderPeople(w, r, p, http.StatusUnprocessableEntity, map[string]any{
			"Name": strings.TrimSpace(r.PostFormValue("name")), "Role": string(role), "Color": look.Color, "Shape": look.Shape, "Error": key, "ErrorField": field,
		})
		return
	}
	if _, err := s.db.AddPerson(p.household.ID, store.Person{Name: look.Name, Role: role, Color: look.Color, Shape: look.Shape}); err != nil {
		s.fail(w, "add person", err)
		return
	}
	s.redirectSaved(w, r, "/people", "flash.person_added")
}

// personFor loads the person of the path, answering 404 when there is none.
func (s *server) personFor(w http.ResponseWriter, r *http.Request, p principal) (store.Person, []store.Person, bool) {
	people, ok := s.peopleOf(w, p)
	if !ok {
		return store.Person{}, nil, false
	}
	i := slices.IndexFunc(people, func(x store.Person) bool { return x.ID == r.PathValue("id") })
	if i < 0 {
		http.NotFound(w, r)
		return store.Person{}, nil, false
	}
	return people[i], people, true
}

func (s *server) renderPerson(w http.ResponseWriter, r *http.Request, p principal, status int, person store.Person, people []store.Person, form map[string]any) {
	var chip personChip
	for _, c := range chipsOf(people) {
		if c.ID == person.ID {
			chip = c
		}
	}
	if form == nil {
		form = map[string]any{"Name": person.Name, "Color": person.Color, "Shape": chip.Shape}
		if c, ok := paletteColor(person.Color); ok {
			form["Color"] = c
		} else {
			form["Color"] = personColors[chip.Slot-1] // the colour the displays show for them
		}
	}
	data := map[string]any{
		"Person": p.person, "SyncStale": s.syncStale(p.household.ID), "Subject": chip, "Role": person.Role, "StreaksShown": person.StreaksShown,
		"Form": form, "Error": form["Error"], "ErrorField": form["ErrorField"],
	}
	f, err := s.db.PersonFood(p.household.ID, person.ID)
	if err != nil {
		s.fail(w, "food", err)
		return
	}
	data["Food"] = s.foodViewOf(s.langOf(r), f)
	if row, ok := s.personContactRow(r, p, person.ID); ok { // the contact data of the person (ticket 296)
		data["ContactRow"] = row
	}
	if person.Role == store.RoleParent { // children get no account (ADR 0010)
		row, err := s.signInRow(r, p, person)
		if err != nil {
			s.fail(w, "sign-in state", err)
			return
		}
		data["SignIn"] = row
	}
	s.render(w, r, status, "person_edit.html", lookData(people, person.ID, data))
}

func (s *server) handlePersonPage(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	if person, people, ok := s.personFor(w, r, p); ok {
		s.renderPerson(w, r, p, http.StatusOK, person, people, nil)
	}
}

func (s *server) handlePersonSave(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	person, people, ok := s.personFor(w, r, p)
	if !ok {
		return
	}
	look := personInput{r.PostFormValue("name"), r.PostFormValue("color"), r.PostFormValue("shape")}
	// The form preselects the colour and marker the displays show. Sent back
	// unchanged, they stand for what is stored (a legacy person keeps their
	// colour and can be renamed); only a changed one is checked. A form without
	// colour or marker keeps what the person has. checkLook stores an edited
	// look as the palette colour and marker the pair check used, so the person
	// leaves the legacy state.
	if i := slices.IndexFunc(people, func(x store.Person) bool { return x.ID == person.ID }); i >= 0 {
		dc, ds := displayedLook(person, i)
		if _, ok := paletteColor(person.Color); !ok && strings.EqualFold(look.Color, dc) {
			look.Color = person.Color
		}
		if person.Shape == "" && look.Shape == ds {
			look.Shape = person.Shape
		}
	}
	if look.Color == "" {
		look.Color = person.Color
	}
	if look.Shape == "" {
		look.Shape = person.Shape
	}
	field, key := checkLook(people, person.ID, &look, &person)
	if field != "" {
		s.renderPerson(w, r, p, http.StatusUnprocessableEntity, person, people, map[string]any{
			"Name": strings.TrimSpace(r.PostFormValue("name")), "Color": look.Color, "Shape": look.Shape, "Error": key, "ErrorField": field,
		})
		return
	}
	if err := s.db.UpdatePerson(p.household.ID, person.ID, look.Name, look.Color, look.Shape); err != nil {
		s.fail(w, "update person", err)
		return
	}
	// The switch "week count and badges" is saved with the rest of the form.
	streaks := r.PostFormValue("streaks") != ""
	if streaks != person.StreaksShown {
		if err := s.db.SetStreaksShown(p.household.ID, person.ID, streaks); err != nil {
			s.fail(w, "streaks switch", err)
			return
		}
		if !streaks {
			s.redirectSavedNamed(w, r, "/people", "flash.streaks_off", look.Name)
			return
		}
	}
	s.redirectSaved(w, r, "/people", "flash.person_saved")
}

// handlePersonMove moves a person one place up or down in the family view.
func (s *server) handlePersonMove(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	delta := map[string]int{"up": -1, "down": 1}[r.PostFormValue("direction")]
	if delta == 0 {
		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return
	}
	person, people, ok := s.personFor(w, r, p)
	if !ok {
		return
	}
	ids := make([]string, len(people))
	for i, x := range people {
		ids[i] = x.ID
	}
	i := slices.Index(ids, person.ID)
	if j := i + delta; j >= 0 && j < len(ids) {
		ids[i], ids[j] = ids[j], ids[i]
		if err := s.db.SetPeopleOrder(p.household.ID, ids); err != nil {
			s.fail(w, "order people", err)
			return
		}
	}
	s.redirectSaved(w, r, "/people", "flash.people_ordered")
}

// Subgroups.

func (s *server) renderGroup(w http.ResponseWriter, r *http.Request, p principal, status int, g store.Group, saved string, isNew bool, errKey, errField string) {
	people, ok := s.peopleOf(w, p)
	if !ok {
		return
	}
	s.render(w, r, status, "group.html", map[string]any{
		"Person": p.person, "SyncStale": s.syncStale(p.household.ID), "Group": g, "Saved": saved, "New": isNew, "People": chipsOf(people),
		"Error": errKey, "ErrorField": errField,
	})
}

func (s *server) handleGroupNew(w http.ResponseWriter, r *http.Request) {
	if p, ok := s.parentPage(w, r); ok {
		s.renderGroup(w, r, p, http.StatusOK, store.Group{}, "", true, "", "")
	}
}

func groupFromForm(r *http.Request) store.Group {
	_ = r.ParseForm()
	return store.Group{Name: strings.TrimSpace(r.PostFormValue("name")), Members: r.PostForm["people"]}
}

func groupNameOK(name string) bool {
	return name != "" && utf8.RuneCountInString(name) <= maxPersonName
}

func (s *server) handleGroupCreate(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	g := groupFromForm(r)
	if !groupNameOK(g.Name) {
		s.renderGroup(w, r, p, http.StatusUnprocessableEntity, g, "", true, "groups.error.name", "name")
		return
	}
	if _, err := s.db.CreateGroup(p.household.ID, g.Name, g.Members); err != nil {
		s.fail(w, "create group", err)
		return
	}
	s.redirectSaved(w, r, "/people", "flash.group_saved")
}

func (s *server) groupFor(w http.ResponseWriter, r *http.Request, p principal) (store.Group, bool) {
	g, err := s.db.Group(p.household.ID, r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return g, false
	}
	if err != nil {
		s.fail(w, "group", err)
		return g, false
	}
	return g, true
}

func (s *server) handleGroupPage(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	if g, ok := s.groupFor(w, r, p); ok {
		s.renderGroup(w, r, p, http.StatusOK, g, g.Name, false, "", "")
	}
}

func (s *server) handleGroupSave(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	g, ok := s.groupFor(w, r, p)
	if !ok {
		return
	}
	in := groupFromForm(r)
	in.ID = g.ID
	if !groupNameOK(in.Name) {
		s.renderGroup(w, r, p, http.StatusUnprocessableEntity, in, g.Name, false, "groups.error.name", "name")
		return
	}
	if err := s.db.UpdateGroup(p.household.ID, g.ID, in.Name, in.Members); err != nil {
		s.fail(w, "update group", err)
		return
	}
	s.redirectSaved(w, r, "/people", "flash.group_saved")
}

func (s *server) handleGroupDelete(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	g, ok := s.groupFor(w, r, p)
	if !ok {
		return
	}
	switch err := s.db.DeleteGroup(p.household.ID, g.ID); {
	case errors.Is(err, store.ErrInUse):
		s.renderGroup(w, r, p, http.StatusConflict, g, g.Name, false, "groups.error.in_use", "")
	case err != nil:
		s.fail(w, "delete group", err)
	default:
		s.redirectSaved(w, r, "/people", "flash.group_deleted")
	}
}
