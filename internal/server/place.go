package server

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/hejmo-home/hejmo/internal/weather"
)

// The home location (Q59): the parent types a place name, the box asks
// Open-Meteo's geocoding once, shows what it found and stores the coordinates
// the parent confirmed. Sunrise and sunset (the sun rule of displays, themes)
// and the weather read the stored coordinates and never search again.

// searchTimeout bounds a place search while a parent waits for the page.
const searchTimeout = 8 * time.Second

// placeOption is one place a search found, as a choice or card on the page.
type placeOption struct {
	Name   string // what the parent would call it
	Region string // "district, state, country", without empty or repeated parts
	Value  string // "lat,lon"
	Label  string // name and region in one line, carried along as place_label
	Coords string // the coordinates in degrees
}

// placeView is what the confirmation part of a form shows.
type placeView struct {
	For string // the name the places were searched for or confirmed for
	// Options are the places to choose from when several match; none is preselected.
	Options []placeOption
	// Found is the one place to confirm by saving: the single match of a
	// search, or the place already chosen for For and carried along while
	// other fields of the form are corrected.
	Found *placeOption
}

// regionText joins the parts of a place below its name, leaving out empty ones
// and ones that repeat the name or an earlier part.
func regionText(name string, parts ...string) string {
	seen := map[string]bool{name: true}
	var out []string
	for _, p := range parts {
		if p != "" && !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	return strings.Join(out, ", ")
}

func newPlaceOption(p weather.Place) placeOption {
	o := placeOption{Name: p.Name, Region: regionText(p.Name, p.Area, p.Region, p.Country), Value: placeValue(p.Latitude, p.Longitude), Coords: coordinatesText(p.Latitude, p.Longitude)}
	o.Label = regionText("", o.Name, o.Region)
	return o
}

// formatCoord writes a coordinate the way the form carries it.
func formatCoord(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }

func placeValue(lat, lon float64) string { return formatCoord(lat) + "," + formatCoord(lon) }

// parsePlace reads a "lat,lon" value and checks the ranges.
func parsePlace(v string) (lat, lon float64, ok bool) {
	a, b, found := strings.Cut(v, ",")
	if !found {
		return 0, 0, false
	}
	lat, err1 := strconv.ParseFloat(strings.TrimSpace(a), 64)
	lon, err2 := strconv.ParseFloat(strings.TrimSpace(b), 64)
	if err1 != nil || err2 != nil || !validCoordinates(lat, lon) {
		return 0, 0, false
	}
	return lat, lon, true
}

// validCoordinates reports whether lat and lon are real degrees. (0, 0) is
// refused: the stored home treats it as "no location yet".
func validCoordinates(lat, lon float64) bool {
	if lat == 0 && lon == 0 {
		return false
	}
	return !math.IsNaN(lat) && !math.IsNaN(lon) && lat >= -90 && lat <= 90 && lon >= -180 && lon <= 180
}

// countryOfRegion is the ISO 3166-1 country code a region code (DE-BY) starts with.
func countryOfRegion(region string) string {
	c, _, _ := strings.Cut(region, "-")
	return c
}

// errPlaceUnreachable is a search that failed for another reason than "no match".
var errPlaceUnreachable = errors.New("place search unreachable")

// searchPlaces looks a place name up, in the country of the region if given.
func (s *server) searchPlaces(ctx context.Context, name, country, lang string) ([]weather.Place, error) {
	ctx, cancel := context.WithTimeout(ctx, searchTimeout)
	defer cancel()
	places, err := s.weather.Search(ctx, name, country, lang)
	switch {
	case errors.Is(err, weather.ErrNoPlace):
		return nil, weather.ErrNoPlace
	case err != nil:
		s.logger.Warn("place search failed", "err", err)
		return nil, errPlaceUnreachable
	}
	return places, nil
}

// placeErrorKey is the text of a failed search.
func placeErrorKey(err error) string {
	if errors.Is(err, weather.ErrNoPlace) {
		return "place.error.none"
	}
	return "place.error.unreachable"
}

// resolvePlace decides what a form that carries a home name needs. If the form
// holds a place confirmed for exactly this name, it returns its coordinates
// (confirmed). Otherwise it searches: the result is either a view to confirm
// or choose from, or the key of the error to show at the name field.
func (s *server) resolvePlace(r *http.Request, name, region string) (lat, lon float64, confirmed bool, view *placeView, errKey string) {
	if r.PostFormValue("place_for") == name {
		if lat, lon, ok := parsePlace(r.PostFormValue("place")); ok {
			return lat, lon, true, nil, ""
		}
	}
	places, err := s.searchPlaces(r.Context(), name, countryOfRegion(region), s.langOf(r))
	if err != nil {
		return 0, 0, false, nil, placeErrorKey(err)
	}
	v := &placeView{For: name}
	if len(places) == 1 {
		o := newPlaceOption(places[0])
		v.Found = &o
		return 0, 0, false, v, ""
	}
	for _, p := range places {
		v.Options = append(v.Options, newPlaceOption(p))
	}
	return 0, 0, false, v, ""
}

// confirmedPlace is the view that carries an already chosen place along.
func confirmedPlace(r *http.Request, name string) *placeView {
	if r.PostFormValue("place_for") != name {
		return nil
	}
	lat, lon, ok := parsePlace(r.PostFormValue("place"))
	if !ok {
		return nil
	}
	o := placeOption{Name: coordinatesText(lat, lon), Value: placeValue(lat, lon)} // a choice among several places carries no label
	if label := strings.TrimSpace(r.PostFormValue("place_label")); label != "" {
		o.Label, o.Coords = label, coordinatesText(lat, lon)
		o.Name, o.Region, _ = strings.Cut(label, ", ")
	}
	return &placeView{For: name, Found: &o}
}

// coordinatesText shows coordinates in degrees, north and east positive.
func coordinatesText(lat, lon float64) string {
	ns, ew := "N", "E"
	if lat < 0 {
		ns, lat = "S", -lat
	}
	if lon < 0 {
		ew, lon = "W", -lon
	}
	return fmt.Sprintf("%s° %s, %s° %s", strconv.FormatFloat(math.Round(lat*1e4)/1e4, 'f', -1, 64), ns, strconv.FormatFloat(math.Round(lon*1e4)/1e4, 'f', -1, 64), ew)
}

// placeJSON is a place of the API's search.
type placeJSON struct {
	Label     string  `json:"label"`
	Name      string  `json:"name"`
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
}

// apiSearchPlaces lists the places that match ?name=, in the country of the
// household's region unless ?country= says otherwise.
func (s *server) apiSearchPlaces(w http.ResponseWriter, r *http.Request, p principal) {
	name := strings.TrimSpace(r.URL.Query().Get("name"))
	if name == "" {
		writeError(w, http.StatusUnprocessableEntity, "name is required")
		return
	}
	country := countryOfRegion(p.household.Region)
	if c, ok := r.URL.Query()["country"]; ok && len(c) > 0 {
		country = strings.ToUpper(strings.TrimSpace(c[0]))
		if !isAlpha2(country) {
			writeError(w, http.StatusUnprocessableEntity, "country must be an ISO 3166-1 alpha-2 code such as DE")
			return
		}
	}
	places, err := s.searchPlaces(r.Context(), name, country, s.langOf(r))
	out := []placeJSON{}
	switch {
	case errors.Is(err, weather.ErrNoPlace):
	case err != nil:
		writeError(w, http.StatusBadGateway, "the place search could not be reached")
		return
	default:
		for _, pl := range places {
			out = append(out, placeJSON{pl.Label(), pl.Name, pl.Latitude, pl.Longitude})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"places": out})
}

// isAlpha2 reports whether c is two letters A-Z.
func isAlpha2(c string) bool {
	return len(c) == 2 && c[0] >= 'A' && c[0] <= 'Z' && c[1] >= 'A' && c[1] <= 'Z'
}
