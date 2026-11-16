package server

import (
	"html"
	"html/template"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/hejmo-home/hejmo/internal/i18n"
	"github.com/hejmo-home/hejmo/internal/places"
	"github.com/hejmo-home/hejmo/internal/store"
	"github.com/hejmo-home/hejmo/internal/sun"
)

// The home location (ADR 0020, ticket 86): the parent picks a place from the
// list of places in Germany, Austria and Switzerland that ships on the box. The
// form posts the place's id (place_id) and never coordinates; the box looks the
// coordinates and the region up in the list. Nothing goes to the internet.

// placeOption is one suggestion of the open picker.
type placeOption struct {
	ID           int
	Name, Region string
	RegionCode   string
}

// chosenPlace is the picked place, confirmed in words.
type chosenPlace struct {
	ID           int
	Name, Region string // Region is empty for a home set through the API: it has no place id
	State        string // the line in bold: chosen, in use today with sun times, or pending
	RegionCode   string
}

// placeKeep is the row of Settings that says which place stays in use while
// another is being picked, and offers to keep it.
type placeKeep struct{ Hint, Button string }

// placeMissing is the dashed box of a household that has a name but no place.
type placeMissing struct{ Title, Hint string }

// placePick is what the placePicker partial shows: the open state (a field
// with suggestions) or the confirmed state (the picked place in words).
type placePick struct {
	Chosen *chosenPlace
	// Text is what is typed in the open field.
	Text string
	// Question and Options are the suggestions under the field.
	Question string
	Options  []placeOption
	// Error is the text at the field (already in the page language).
	Error   string
	Keep    *placeKeep
	Missing *placeMissing
}

// pickerInput is what a form posted about the place. A pick is a post that
// carries the typed text and a place id (the button of a suggestion); a post
// of the confirmed state carries the id only.
type pickerInput struct {
	Text    string // home_name, trimmed
	HasText bool
	ID      int // place_id, 0 if none or not a number
	HasID   bool
	Change  bool // the Change button
	Keep    bool // the Keep button
}

func readPickerInput(r *http.Request) pickerInput {
	_ = r.ParseForm()
	in := pickerInput{Change: r.PostFormValue("place_change") != "", Keep: r.PostFormValue("place_keep") != ""}
	if _, ok := r.PostForm["home_name"]; ok {
		in.HasText, in.Text = true, strings.TrimSpace(r.PostFormValue("home_name"))
	}
	if v := strings.TrimSpace(r.PostFormValue("place_id")); v != "" {
		in.HasID = true
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			in.ID = n
		}
	}
	return in
}

// suggestions are the places for what is typed: the question above them and
// at most five options. The question says in words when more places match.
func (s *server) suggestions(lang, text string) (question string, opts []placeOption, found int) {
	list, more := s.places.Search(text, lang)
	for _, p := range list {
		opts = append(opts, placeOption{ID: p.ID, Name: p.Name, Region: p.Region, RegionCode: p.RegionCode})
	}
	switch {
	case len(opts) == 0:
	case more:
		question = s.i18n.T(lang, "place.more5")
	case len(opts) == 1:
		question = s.i18n.T(lang, "place.one_match")
	default:
		question = s.i18n.Format(lang, i18n.Msg("place.n_match", "n", strconv.Itoa(len(opts))))
	}
	return question, opts, len(opts)
}

// openPicker is the open field with the suggestions for text.
func (s *server) openPicker(lang, text string) *placePick {
	q, opts, _ := s.suggestions(lang, text)
	return &placePick{Text: text, Question: q, Options: opts}
}

// openPickerError is the open field with an error at it. A search without a
// result says that no place was found; text that matches places asks to pick
// one of them.
func (s *server) openPickerError(lang, text string) *placePick {
	v := s.openPicker(lang, text)
	switch {
	case text == "":
		v.Error = s.i18n.T(lang, "setup.error.home")
	case len(v.Options) == 0:
		v.Error = s.i18n.Format(lang, i18n.Msg("place.error.none", "q", text))
	default:
		v.Error = s.i18n.T(lang, "place.error.pick")
	}
	return v
}

// pickError is the open field after a post that named no known place.
func (s *server) pickError(lang, text string) *placePick {
	v := s.openPicker(lang, text)
	v.Error = s.i18n.T(lang, "place.error.pick")
	return v
}

// chosenPicker is the confirmed state for a place; state is its line in bold.
func chosenPicker(pl places.Place, state string) *placePick {
	return &placePick{Chosen: &chosenPlace{ID: pl.ID, Name: pl.Name, Region: pl.Region, RegionCode: pl.RegionCode, State: state}}
}

// ErrorHTML is the error text for the field, ready for the fieldErrorHTML
// partial.
func (p *placePick) ErrorHTML() template.HTML { return template.HTML(html.EscapeString(p.Error)) }

// sunLine is "In use · today sunrise 07:20, sunset 16:38" for a home, in the
// household's time zone and format. Where there is no sunrise to name (polar
// day or night), it says the place is chosen.
func (s *server) sunLine(r *http.Request, p principal, lat, lon float64) string {
	h := p.household
	lang := s.langOf(r)
	t := sun.On(s.now().In(p.location()), lat, lon)
	if t.Kind != sun.Normal {
		return s.i18n.T(lang, "place.chosen")
	}
	layout := "15:04"
	if h.TimeFormat == store.Time12h {
		layout = "3:04 PM"
	}
	return s.i18n.Format(lang, i18n.Msg("place.in_use", "rise", t.Rise.Round(time.Minute).In(p.location()).Format(layout), "set", t.Set.Round(time.Minute).In(p.location()).Format(layout)))
}

// placeJSON is a place of the API's search.
type placeJSON struct {
	ID         int     `json:"id"`
	Name       string  `json:"name"`
	Region     string  `json:"region"`
	Latitude   float64 `json:"latitude"`
	Longitude  float64 `json:"longitude"`
	RegionCode string  `json:"region_code"`
}

// searchPlacesJSON answers a search of the list on the box: at most five
// places and whether more match. It never leaves the box.
func (s *server) searchPlacesJSON(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.URL.Query().Get("name"))
	if name == "" {
		writeError(w, http.StatusUnprocessableEntity, "name is required")
		return
	}
	lang := s.langOf(r)
	if l := r.URL.Query().Get("lang"); l != "" {
		if l != "de" && l != "en" {
			writeError(w, http.StatusUnprocessableEntity, "lang must be de or en")
			return
		}
		lang = l
	}
	found, more := s.places.Search(name, lang)
	out := make([]placeJSON, 0, len(found))
	for _, p := range found {
		out = append(out, placeJSON{p.ID, p.Name, p.Region, p.Lat, p.Lon, p.RegionCode})
	}
	writeJSON(w, http.StatusOK, map[string]any{"places": out, "more": more})
}

// apiSearchPlaces is GET /api/v1/places?name=&lang=.
func (s *server) apiSearchPlaces(w http.ResponseWriter, r *http.Request, _ principal) {
	s.searchPlacesJSON(w, r)
}

// handlePlaces is the same search for the pages' script, outside the API: the
// setup wizard has no sign-in yet. It answers from the public list on the box.
func (s *server) handlePlaces(w http.ResponseWriter, r *http.Request) {
	s.searchPlacesJSON(w, r)
}
