package server

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/hejmo-home/hejmo/internal/library"
	"github.com/hejmo-home/hejmo/internal/outbound"
	"github.com/hejmo-home/hejmo/internal/picture"
	"github.com/hejmo-home/hejmo/internal/store"
)

// The article library over the REST API (ticket 164, ADR 0019). Articles are
// not health data; who is warned about them is, so the warnings and the
// setting that hides articles need the explicit scope "health".

type articleJSON struct {
	ID            string   `json:"id"`
	Source        string   `json:"source"` // "hejmo" or "own"
	NameDE        string   `json:"name_de"`
	NameEN        string   `json:"name_en"`
	Dept          string   `json:"dept"`
	Kind          string   `json:"kind"`
	Allergens     []string `json:"allergens"`
	Units         []string `json:"units"`
	OfferedUnits  []string `json:"offered_units"`
	Symbol        string   `json:"symbol"`
	Picture       *string  `json:"picture"`
	PictureSource string   `json:"picture_source"`
	HiddenByHand  bool     `json:"hidden_by_hand"`
	CreatedBy     *string  `json:"created_by"`
	CreatedAt     *string  `json:"created_at"`
}

func (s *server) articleToJSON(p principal, it articleItem) articleJSON {
	j := articleJSON{ID: it.ID, Source: "hejmo", NameDE: it.De, NameEN: it.En, Dept: it.Dept, Kind: string(it.Kind), Allergens: nonNil(it.Allergens),
		Units: it.Units, OfferedUnits: s.offeredUnits(p, it.Article), Symbol: it.Symbol, HiddenByHand: it.handHidden}
	if it.own == nil {
		j.Symbol = it.Symbol
		return j
	}
	o := it.own
	j.Source, j.PictureSource = "own", o.PicSource
	if o.HasPicture {
		u := "/articles/" + o.ID + "/picture"
		j.Picture = &u
	}
	if o.CreatedBy != "" {
		j.CreatedBy = &o.CreatedBy
	}
	at := o.CreatedAt.UTC().Format(time.RFC3339)
	j.CreatedAt = &at
	return j
}

func (s *server) apiListArticles(w http.ResponseWriter, r *http.Request, p principal) {
	st, err := s.libState(p, s.langOf(r))
	if err != nil {
		s.fail(w, "library state", err)
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	out := []articleJSON{}
	for _, it := range st.items() {
		if q == "" || library.Matches(it.Article, q) {
			out = append(out, s.articleToJSON(p, it))
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) apiGetArticle(w http.ResponseWriter, r *http.Request, p principal) {
	st, err := s.libState(p, s.langOf(r))
	if err != nil {
		s.fail(w, "library state", err)
		return
	}
	it, ok := st.item(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "article not found")
		return
	}
	writeJSON(w, http.StatusOK, s.articleToJSON(p, it))
}

// articleIn is what creating or changing an own article takes. Missing fields
// of a change keep the stored value.
type articleIn struct {
	Name          *string   `json:"name"`
	Dept          *string   `json:"dept"`
	Kind          *string   `json:"kind"`
	Allergens     *[]string `json:"allergens"`
	Unit1         *string   `json:"unit1"`
	Unit2         *string   `json:"unit2"`
	Symbol        *string   `json:"symbol"`
	PictureURL    *string   `json:"picture_url"`
	RemovePicture bool      `json:"remove_picture"`
}

// pictureErrorJSON answers a web image or an upload that was refused.
func (s *server) pictureErrorJSON(w http.ResponseWriter, field string, err error) {
	var private *picture.PrivateError
	var big *picture.TooLargeError
	var status *picture.StatusError
	switch {
	case errors.Is(err, outbound.ErrRefused):
		writeFieldError(w, http.StatusConflict, field, "web images are switched off (offline mode, or the connection \"images\" is off): see GET /outbound")
	case errors.As(err, &private):
		writeFieldError(w, http.StatusUnprocessableEntity, field, "the address leads to the own network ("+private.Addr+"), not to the internet; only public addresses are loaded")
	case errors.As(err, &big):
		writeFieldError(w, http.StatusUnprocessableEntity, field, "the picture is over 15 MB")
	case errors.Is(err, picture.ErrWebPage):
		writeFieldError(w, http.StatusUnprocessableEntity, field, "this is a web page, not a picture")
	case errors.Is(err, picture.ErrNotAnImage), errors.Is(err, picture.ErrTooManyPixels):
		writeFieldError(w, http.StatusUnprocessableEntity, field, "not a JPEG, PNG, WebP or GIF picture")
	case errors.Is(err, picture.ErrScheme), errors.Is(err, picture.ErrAddress):
		writeFieldError(w, http.StatusUnprocessableEntity, field, "not an http or https address")
	case errors.Is(err, picture.ErrTimeout):
		writeFieldError(w, http.StatusUnprocessableEntity, field, "the site did not answer within 10 seconds")
	case errors.As(err, &status):
		writeFieldError(w, http.StatusUnprocessableEntity, field, "the site answered with an error")
	default:
		writeFieldError(w, http.StatusUnprocessableEntity, field, "the picture could not be loaded")
	}
}

func fieldMessage(e library.FieldErr) string { return e.Field + ": " + e.Code }

// articleWrite creates (existing nil) or changes an own article.
func (s *server) articleWrite(w http.ResponseWriter, r *http.Request, p principal, existing *store.OwnArticle) {
	var in articleIn
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	cur := store.OwnArticle{Dept: library.DefaultDept, Unit1: s.offeredUnits(p, library.Article{Units: []string{"piece"}})[0]}
	if existing != nil {
		cur = *existing
	}
	set := func(dst *string, v *string) {
		if v != nil {
			*dst = *v
		}
	}
	set(&cur.Name, in.Name)
	set(&cur.Dept, in.Dept)
	set(&cur.Kind, in.Kind)
	set(&cur.Unit1, in.Unit1)
	set(&cur.Unit2, in.Unit2)
	if in.Allergens != nil {
		cur.Allergens = *in.Allergens
	}
	change := store.KeepPicture
	var pic *store.ArticlePicture
	fromWeb := in.PictureURL != nil && *in.PictureURL != ""
	hasSymbol := in.Symbol != nil && *in.Symbol != ""
	if hasSymbol && fromWeb {
		writeError(w, http.StatusUnprocessableEntity, "give a symbol or a picture_url, not both")
		return
	}
	switch {
	case fromWeb:
		change = store.SetPicture
	case hasSymbol:
		cur.PicSource, cur.Symbol = "symbol", *in.Symbol
		change = store.RemovePicture
	case in.RemovePicture:
		cur.PicSource, cur.Symbol = "", ""
		change = store.RemovePicture
	}
	check := library.Input{Name: cur.Name, Dept: cur.Dept, Kind: cur.Kind, Allergens: cur.Allergens, Unit1: cur.Unit1, Unit2: cur.Unit2}
	if cur.PicSource == "symbol" {
		check.PicSource, check.Symbol = "symbol", cur.Symbol
	}
	if errs := check.Validate(s.activeUnitsFor(p, existing)); len(errs) > 0 {
		writeFieldError(w, http.StatusUnprocessableEntity, errs[0].Field, fieldMessage(errs[0]))
		return
	}
	// Only a request that is valid otherwise may cause an outbound download.
	if fromWeb {
		res, err := s.fetcher.Fetch(r.Context(), *in.PictureURL)
		if err != nil {
			s.pictureErrorJSON(w, "picture_url", err)
			return
		}
		pic = &store.ArticlePicture{JPEG: res.JPEG, Source: "web", Host: res.Host, FromW: res.FromW, FromH: res.FromH, FromBytes: res.FromBytes}
	}
	var saved store.OwnArticle
	var err error
	if existing == nil {
		if pic != nil {
			cur.PicSource = "web"
		}
		saved, err = s.db.CreateArticle(p.household.ID, p.person.ID, cur, pic)
	} else {
		saved, err = s.db.UpdateArticle(p.household.ID, cur, change, pic)
	}
	if err != nil {
		if errors.Is(err, store.ErrInvalid) {
			writeError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		s.fail(w, "save article", err)
		return
	}
	status := http.StatusOK
	if existing == nil {
		status = http.StatusCreated
	}
	writeJSON(w, status, s.articleToJSON(p, articleItem{Article: ownArticle(saved), own: &saved}))
}

func (s *server) apiCreateArticle(w http.ResponseWriter, r *http.Request, p principal) {
	s.articleWrite(w, r, p, nil)
}

func (s *server) ownByID(w http.ResponseWriter, p principal, id string) (*store.OwnArticle, bool) {
	o, err := s.db.Article(p.household.ID, id)
	if err != nil {
		if _, shipped := library.ShippedByID(id); shipped {
			writeError(w, http.StatusUnprocessableEntity, "articles from Hejmo cannot be changed: hide them or copy them")
		} else {
			writeError(w, http.StatusNotFound, "article not found")
		}
		return nil, false
	}
	return &o, true
}

func (s *server) apiUpdateArticle(w http.ResponseWriter, r *http.Request, p principal) {
	o, ok := s.ownByID(w, p, r.PathValue("id"))
	if !ok {
		return
	}
	s.articleWrite(w, r, p, o)
}

func (s *server) apiDeleteArticle(w http.ResponseWriter, r *http.Request, p principal) {
	o, ok := s.ownByID(w, p, r.PathValue("id"))
	if !ok {
		return
	}
	if err := s.db.DeleteArticle(p.household.ID, o.ID); err != nil {
		s.fail(w, "delete article", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) apiGetArticlePicture(w http.ResponseWriter, r *http.Request, p principal) {
	b, err := s.db.ArticlePicture(p.household.ID, r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "article or picture not found")
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(b)
}

// apiPutArticlePicture takes a photo as the request body.
func (s *server) apiPutArticlePicture(w http.ResponseWriter, r *http.Request, p principal) {
	o, ok := s.ownByID(w, p, r.PathValue("id"))
	if !ok {
		return
	}
	res, err := picture.ProcessContext(r.Context(), http.MaxBytesReader(w, r.Body, picture.MaxBytes+1))
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			err = &picture.TooLargeError{Bytes: tooBig.Limit}
		}
		s.pictureErrorJSON(w, "picture", err)
		return
	}
	pic := &store.ArticlePicture{JPEG: res.JPEG, Source: "upload", FromW: res.FromW, FromH: res.FromH, FromBytes: res.FromBytes}
	saved, err := s.db.UpdateArticle(p.household.ID, *o, store.SetPicture, pic)
	if err != nil {
		s.fail(w, "set article picture", err)
		return
	}
	writeJSON(w, http.StatusOK, s.articleToJSON(p, articleItem{Article: ownArticle(saved), own: &saved}))
}

func (s *server) apiSetArticleHidden(w http.ResponseWriter, r *http.Request, p principal) {
	var in struct {
		Hidden bool `json:"hidden"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	id := r.PathValue("id")
	if _, ok := library.ShippedByID(id); !ok {
		if _, err := s.db.Article(p.household.ID, id); err == nil {
			writeError(w, http.StatusUnprocessableEntity, "only articles from Hejmo are hidden; delete an own article instead")
		} else {
			writeError(w, http.StatusNotFound, "article not found")
		}
		return
	}
	if err := s.db.SetArticleHidden(p.household.ID, id, in.Hidden); err != nil {
		s.fail(w, "hide article", err)
		return
	}
	st, err := s.libState(p, s.langOf(r))
	if err != nil {
		s.fail(w, "library state", err)
		return
	}
	it, _ := st.item(id)
	writeJSON(w, http.StatusOK, s.articleToJSON(p, it))
}

func (s *server) apiCopyArticle(w http.ResponseWriter, r *http.Request, p principal) {
	a, ok := library.ShippedByID(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "article from Hejmo not found")
		return
	}
	lang := s.langOf(r)
	active := s.activeUnits(p.household)
	own := store.OwnArticle{Name: a.Name(lang), Dept: a.Dept, Kind: string(a.Kind), Allergens: a.Allergens, Unit1: s.offeredUnits(p, a)[0]}
	if len(a.Units) > 1 && slices.Contains(active, a.Units[1]) && a.Units[1] != own.Unit1 {
		own.Unit2 = a.Units[1]
	}
	saved, err := s.db.CreateArticle(p.household.ID, p.person.ID, own, nil)
	if err != nil {
		s.fail(w, "copy article", err)
		return
	}
	if err := s.db.SetArticleHidden(p.household.ID, a.ID, true); err != nil {
		s.fail(w, "hide copied article", err)
		return
	}
	if err := s.db.MoveAlwaysRule(p.household.ID, a.ID, saved.ID); err != nil { // "always on the list" goes with the copy
		s.fail(w, "move always rule", err)
		return
	}
	writeJSON(w, http.StatusCreated, s.articleToJSON(p, articleItem{Article: ownArticle(saved), own: &saved}))
}

type warningJSON struct {
	People    []string `json:"people"`
	Allergens []string `json:"allergens"`
}

// apiArticleWarnings tells who has to look out for an article and whether
// Hejmo suggests it: health data, so only the scope health reaches it.
func (s *server) apiArticleWarnings(w http.ResponseWriter, r *http.Request, p principal) {
	st, err := s.libState(p, s.langOf(r))
	if err != nil {
		s.fail(w, "library state", err)
		return
	}
	it, ok := st.item(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "article not found")
		return
	}
	warnings := []warningJSON{}
	for _, wn := range st.warnings(it) {
		warnings = append(warnings, warningJSON{People: wn.People, Allergens: wn.Allergens})
	}
	fit, notFit := library.FitsPeople(it.Article, st.persons)
	ids := func(ps []library.Person) []string {
		out := []string{}
		for _, x := range ps {
			out = append(out, x.ID)
		}
		return out
	}
	sug := st.suggestion(it)
	var reason *string
	if sug.Reason != library.ReasonNone {
		rs := string(sug.Reason)
		reason = &rs
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"article_id": it.ID, "warnings": warnings, "suits": ids(fit), "does_not_suit": ids(notFit),
		"suggested": sug.Suggested, "not_suggested_because": reason, "hidden_for": nonNil(sug.By),
		"note": uncheckedNote,
	})
}

type hidingJSON struct {
	Enabled      bool     `json:"enabled"`
	Everyone     bool     `json:"everyone"`
	People       []string `json:"people"`
	ConfirmedBy  *string  `json:"confirmed_by"`
	ConfirmedAt  *string  `json:"confirmed_at"`
	Experimental bool     `json:"experimental"`
}

func hidingToJSON(set store.LibrarySettings) hidingJSON {
	j := hidingJSON{Enabled: set.HideAllergen, Everyone: set.Everyone, People: nonNil(set.People), Experimental: true}
	if set.ConfirmedBy != "" {
		j.ConfirmedBy = &set.ConfirmedBy
	}
	if !set.ConfirmedAt.IsZero() {
		at := set.ConfirmedAt.UTC().Format(time.RFC3339)
		j.ConfirmedAt = &at
	}
	return j
}

func (s *server) apiGetHiding(w http.ResponseWriter, _ *http.Request, p principal) {
	set, err := s.db.LibrarySettings(p.household.ID)
	if err != nil {
		s.fail(w, "library settings", err)
		return
	}
	writeJSON(w, http.StatusOK, hidingToJSON(set))
}

// apiPutHiding switches the experimental hiding on or off. Switching it on
// needs confirm: the person behind the token says they read the notice.
func (s *server) apiPutHiding(w http.ResponseWriter, r *http.Request, p principal) {
	var in struct {
		Enabled  bool     `json:"enabled"`
		Everyone *bool    `json:"everyone"`
		People   []string `json:"people"`
		Confirm  bool     `json:"confirm"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	old, err := s.db.LibrarySettings(p.household.ID)
	if err != nil {
		s.fail(w, "library settings", err)
		return
	}
	next := old
	next.HideAllergen = in.Enabled
	if in.Enabled {
		if !in.Confirm {
			writeFieldError(w, http.StatusUnprocessableEntity, "confirm", "experimental: Hejmo guarantees no completeness and takes no liability; send confirm: true to confirm that the notice was read")
			return
		}
		people, err := s.db.People(p.household.ID)
		if err != nil {
			s.fail(w, "people", err)
			return
		}
		chosen := []string{}
		for _, id := range in.People {
			if !slices.ContainsFunc(people, func(x store.Person) bool { return x.ID == id }) {
				writeFieldError(w, http.StatusUnprocessableEntity, "people", "unknown person "+id)
				return
			}
			if !slices.Contains(chosen, id) {
				chosen = append(chosen, id)
			}
		}
		next.Everyone = len(chosen) == 0 || (in.Everyone != nil && *in.Everyone)
		next.People = chosen
		if next.Everyone {
			next.People = []string{}
		}
		if !old.HideAllergen || old.ConfirmedBy == "" {
			next.ConfirmedBy = p.person.ID
		}
	}
	if err := s.db.SetLibrarySettings(p.household.ID, next); err != nil {
		s.fail(w, "set library settings", err)
		return
	}
	set, _ := s.db.LibrarySettings(p.household.ID)
	writeJSON(w, http.StatusOK, hidingToJSON(set))
}
