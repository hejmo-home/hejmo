package server

import (
	"errors"
	"net/http"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/hejmo-home/hejmo/internal/store"
)

// personRow is a person on the people page: the chip, the role and where they
// stand in the order of the family view.
type personRow struct {
	personChip
	Role        store.Role
	First, Last bool
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
		for _, p := range people {
			if p.ID != exceptID && strings.EqualFold(p.Color, c) {
				users = append(users, p.Name)
				if p.Shape != "" {
					shapes = append(shapes, p.Shape)
				}
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
		for _, p := range people {
			if p.ID != exceptID && p.Shape == sh {
				users = append(users, p.Name)
				if p.Color != "" {
					cols = append(cols, strings.ToUpper(p.Color))
				}
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
	rows := make([]personRow, len(people))
	byID := map[string]personChip{}
	for i, c := range chips {
		rows[i] = personRow{c, people[i].Role, i == 0, i == len(people)-1}
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
	s.render(w, r, status, "person_edit.html", lookData(people, person.ID, map[string]any{
		"Person": p.person, "SyncStale": s.syncStale(p.household.ID), "Subject": chip, "Role": person.Role,
		"Form": form, "Error": form["Error"], "ErrorField": form["ErrorField"],
	}))
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
	// colour and can be renamed); only a changed one is checked.
	for _, c := range chipsOf(people) {
		if c.ID != person.ID {
			continue
		}
		if _, ok := paletteColor(person.Color); !ok && strings.EqualFold(look.Color, personColors[c.Slot-1]) {
			look.Color = person.Color
		}
		if person.Shape == "" && look.Shape == c.Shape {
			look.Shape = person.Shape
		}
	}
	field, key := checkLook(people, person.ID, &look, &person)
	if field == "" {
		// A form without colour or marker keeps what the person has.
		if look.Color == "" {
			look.Color = person.Color
		}
		if look.Shape == "" {
			look.Shape = person.Shape
		}
		if (look.Color != person.Color || look.Shape != person.Shape) && look.Color != "" && look.Shape != "" && styleTaken(people, person.ID, look.Color, look.Shape) {
			field, key = "shape", "people.error.taken"
		}
	}
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

func (s *server) renderGroup(w http.ResponseWriter, r *http.Request, p principal, status int, g store.Group, isNew bool, errKey, errField string) {
	people, ok := s.peopleOf(w, p)
	if !ok {
		return
	}
	s.render(w, r, status, "group.html", map[string]any{
		"Person": p.person, "SyncStale": s.syncStale(p.household.ID), "Group": g, "New": isNew, "People": chipsOf(people),
		"Error": errKey, "ErrorField": errField,
	})
}

func (s *server) handleGroupNew(w http.ResponseWriter, r *http.Request) {
	if p, ok := s.parentPage(w, r); ok {
		s.renderGroup(w, r, p, http.StatusOK, store.Group{}, true, "", "")
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
		s.renderGroup(w, r, p, http.StatusUnprocessableEntity, g, true, "groups.error.name", "name")
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
		s.renderGroup(w, r, p, http.StatusOK, g, false, "", "")
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
		s.renderGroup(w, r, p, http.StatusUnprocessableEntity, in, false, "groups.error.name", "name")
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
		s.renderGroup(w, r, p, http.StatusConflict, g, false, "groups.error.in_use", "")
	case err != nil:
		s.fail(w, "delete group", err)
	default:
		s.redirectSaved(w, r, "/people", "flash.group_deleted")
	}
}
