package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/hejmo-home/hejmo/internal/store"
)

// maxPersonName is the longest name of a person or subgroup.
const maxPersonName = 80

// personColors are the eight person colours of the "Nordic Calm" palette in slot
// order (DESIGN.md). A person's colour is one of them: the web interface, the
// displays and the dark palette pick the matching slot, so changing the colour
// changes it everywhere.
var personColors = []string{"#B4583A", "#2F5E7A", "#4E7D52", "#9A7420", "#86587F", "#3A7A74", "#5559A3", "#B04A6A"}

// paletteColor returns the palette colour written like c (case ignored).
func paletteColor(c string) (string, bool) {
	for _, p := range personColors {
		if strings.EqualFold(p, c) {
			return p, true
		}
	}
	return "", false
}

func validShape(s string) bool { return slices.Contains(markerShapes, s) }

// slotOf is the colour slot (1..8) of the person at place i in the household:
// the slot of their palette colour, or one by place for a person without one.
func slotOf(p store.Person, i int) int {
	for j, c := range personColors {
		if strings.EqualFold(c, p.Color) {
			return j + 1
		}
	}
	return i%len(personColors) + 1
}

// displayedLook is the colour (palette colour) and marker the pages show for
// the person at place i: their own, or for a person without a palette colour
// or marker the one of their place.
func displayedLook(p store.Person, i int) (color, shape string) {
	shape = p.Shape
	if shape == "" {
		shape = markerShapes[i%len(markerShapes)]
	}
	return personColors[slotOf(p, i)-1], shape
}

// styleTaken reports whether another person already shows this colour with
// this marker, which would make the two hard to tell apart. People are
// compared by what the pages show, so a person without a palette colour or
// marker counts with their place-based one.
func styleTaken(people []store.Person, exceptID, color, shape string) bool {
	for i, p := range people {
		if c, s := displayedLook(p, i); p.ID != exceptID && strings.EqualFold(c, color) && s == shape {
			return true
		}
	}
	return false
}

// freeStyle picks a colour and marker no one has yet. A given colour or shape
// is kept. The default pairs come first, so the example family keeps its
// colours; then every combination of the eight colours and eight markers (from the ninth person on).
func freeStyle(people []store.Person, color, shape string) (string, string, bool) {
	try := func(c, s string) bool {
		return (color == "" || c == color) && (shape == "" || s == shape) && !styleTaken(people, "", c, s)
	}
	for _, st := range personStyles {
		if try(st.color, st.shape) {
			return st.color, st.shape, true
		}
	}
	for _, c := range personColors {
		for _, s := range markerShapes {
			if try(c, s) {
				return c, s, true
			}
		}
	}
	return "", "", false
}

// personInput is what a form or the API says about a person's look.
type personInput struct {
	Name, Color, Shape string
}

// checkLook validates name, colour and marker of a person. exceptID is the
// person being changed; cur is that person's stored look (nil for a new one):
// a colour or marker equal to the stored one is not checked again, so people
// stored with a colour from before the palette can still be renamed. An empty
// colour or marker is left to the caller. The result names the field that is
// wrong.
func checkLook(people []store.Person, exceptID string, in *personInput, cur *store.Person) (field, key string) {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || utf8.RuneCountInString(in.Name) > maxPersonName {
		return "name", "people.error.name"
	}
	sameColor := cur != nil && in.Color == cur.Color
	sameShape := cur != nil && in.Shape == cur.Shape
	if in.Color != "" && !sameColor {
		c, ok := paletteColor(in.Color)
		if !ok {
			return "color", "people.error.color"
		}
		in.Color = c
	}
	if in.Shape != "" && !sameShape && !validShape(in.Shape) {
		return "shape", "people.error.shape"
	}
	if sameColor && sameShape {
		return "", ""
	}
	// The pair that counts is what the person will show: a colour or marker
	// they have not got (yet) is the one the pages show for them.
	color, shape := in.Color, in.Shape
	if cur != nil {
		if i := slices.IndexFunc(people, func(x store.Person) bool { return x.ID == cur.ID }); i >= 0 {
			dc, ds := displayedLook(*cur, i)
			if _, ok := paletteColor(color); !ok {
				color = dc
			}
			if shape == "" {
				shape = ds
			}
		}
	}
	if color != "" && shape != "" && styleTaken(people, exceptID, color, shape) {
		return "shape", "people.error.taken"
	}
	return "", ""
}

func (s *server) apiUpdatePerson(w http.ResponseWriter, r *http.Request, p principal) {
	var in struct {
		Name, Color, Shape *string
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	people, err := s.db.People(p.household.ID)
	if err != nil {
		s.fail(w, "list people", err)
		return
	}
	i := slices.IndexFunc(people, func(x store.Person) bool { return x.ID == r.PathValue("id") })
	if i < 0 {
		writeError(w, http.StatusNotFound, "person not found")
		return
	}
	cur := people[i]
	look := personInput{cur.Name, cur.Color, cur.Shape}
	if in.Name != nil {
		look.Name = *in.Name
	}
	if in.Color != nil {
		if *in.Color == "" {
			writeError(w, http.StatusUnprocessableEntity, "invalid color")
			return
		}
		look.Color = *in.Color
	}
	if in.Shape != nil {
		if *in.Shape == "" {
			writeError(w, http.StatusUnprocessableEntity, "invalid shape")
			return
		}
		look.Shape = *in.Shape
	}
	if field, _ := checkLook(people, cur.ID, &look, &cur); field != "" {
		writeError(w, http.StatusUnprocessableEntity, "invalid "+field)
		return
	}
	if err := s.db.UpdatePerson(p.household.ID, cur.ID, look.Name, look.Color, look.Shape); err != nil {
		s.fail(w, "update person", err)
		return
	}
	cur.Name, cur.Color, cur.Shape = look.Name, look.Color, look.Shape
	writeJSON(w, http.StatusOK, toJSON(cur))
}

func (s *server) apiOrderPeople(w http.ResponseWriter, r *http.Request, p principal) {
	var in struct {
		IDs []string `json:"ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if err := s.db.SetPeopleOrder(p.household.ID, in.IDs); errors.Is(err, store.ErrInvalid) {
		writeError(w, http.StatusUnprocessableEntity, "ids must list every person exactly once")
		return
	} else if err != nil {
		s.fail(w, "order people", err)
		return
	}
	s.apiListPeople(w, r, p)
}

func (s *server) apiUpdateGroup(w http.ResponseWriter, r *http.Request, p principal) {
	var in struct {
		Name    *string   `json:"name"`
		Members *[]string `json:"members"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	g, err := s.db.Group(p.household.ID, r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "group not found")
		return
	} else if err != nil {
		s.fail(w, "get group", err)
		return
	}
	if in.Name != nil {
		g.Name = *in.Name
		if utf8.RuneCountInString(strings.TrimSpace(g.Name)) > maxPersonName {
			writeError(w, http.StatusUnprocessableEntity, "name is too long")
			return
		}
	}
	if in.Members != nil {
		g.Members = *in.Members
	}
	if err := s.db.UpdateGroup(p.household.ID, g.ID, g.Name, g.Members); errors.Is(err, store.ErrInvalid) {
		writeError(w, http.StatusUnprocessableEntity, "name is required")
		return
	} else if err != nil {
		s.fail(w, "update group", err)
		return
	}
	g, _ = s.db.Group(p.household.ID, g.ID)
	writeJSON(w, http.StatusOK, groupJSON{g.ID, g.Name, nonNil(g.Members)})
}

func (s *server) apiDeleteGroup(w http.ResponseWriter, r *http.Request, p principal) {
	switch err := s.db.DeleteGroup(p.household.ID, r.PathValue("id")); {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "group not found")
	case errors.Is(err, store.ErrInUse):
		writeError(w, http.StatusConflict, "the group still decides who sees a calendar or an event")
	case err != nil:
		s.fail(w, "delete group", err)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}
