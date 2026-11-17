package server

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"html"
	"html/template"
	"image"
	_ "image/jpeg"
	"io"
	"mime/multipart"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/hejmo-home/hejmo/internal/food"
	"github.com/hejmo-home/hejmo/internal/i18n"
	"github.com/hejmo-home/hejmo/internal/library"
	"github.com/hejmo-home/hejmo/internal/outbound"
	"github.com/hejmo-home/hejmo/internal/picture"
	"github.com/hejmo-home/hejmo/internal/store"
	"github.com/hejmo-home/hejmo/internal/unit"
)

// The form of an own article (ticket 164, preview-164): name, department,
// picture (symbol, photo or an image from the web), units, what is in it and
// allergens. The same form creates and changes an article.

// maxForm is the most a form with a photo may send; the photo itself may be 15 MB.
const maxForm = 40 << 20

// maxSymbolTiles is how many symbols a search shows at most.
const maxSymbolTiles = 96

// artForm is what the form holds: the stored article, or what was posted.
type artForm struct {
	Name, Dept, Kind, Unit1, Unit2 string
	Allergens                      []string
	PicSource, Symbol, SymQ, URL   string
	Token                          string
	Replacing                      bool
}

func (s *server) formOf(o store.OwnArticle) artForm {
	return artForm{Name: o.Name, Dept: o.Dept, Kind: o.Kind, Unit1: o.Unit1, Unit2: o.Unit2, Allergens: o.Allergens, PicSource: o.PicSource, Symbol: o.Symbol}
}

// Views of the parts of the form.

type deptChip struct {
	Key, Name, Symbol string
	Checked           bool
}

type unitChip struct {
	Key, Name     string
	Checked, Same bool
}

type kindCard struct {
	Key, Name, Desc string
	Checked         bool
}

type allergenCard2 struct {
	Key, Name, Examples string
	Checked             bool
	Aff                 template.HTML
}

type symTile struct {
	Emoji, Name string
	Checked     bool
}

// picResult is a picture shown in words: a picture just made, or the one the article has.
type picResult struct {
	Source, URL, Title, Text string
	CanRemove                bool
}

func newToken() string {
	var b [12]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// renderArticleForm shows the form of a new article (existing nil) or of an own article.
func (s *server) renderArticleForm(w http.ResponseWriter, r *http.Request, p principal, status int, st libState, existing *store.OwnArticle, f artForm, pend *pendingPicture, errs map[string]string) {
	lang := st.lang
	if errs == nil {
		errs = map[string]string{}
	}
	active := s.activeUnits(p.household.ID)
	if f.Dept == "" || !library.ValidDept(f.Dept) {
		f.Dept = library.DefaultDept
	}
	if f.Unit1 == "" && existing == nil {
		if slices.Contains(active, unit.Piece) {
			f.Unit1 = unit.Piece
		} else {
			f.Unit1 = active[0]
		}
	}

	deptName := s.i18n.T(lang, "dept."+f.Dept)
	depts := make([]deptChip, 0, len(library.Depts))
	for _, d := range library.Depts {
		depts = append(depts, deptChip{d, s.i18n.T(lang, "dept."+d), library.DeptSymbol(d), d == f.Dept})
	}

	// What the picture choice shows.
	var res *picResult
	source := f.PicSource
	switch {
	case pend != nil:
		source = pend.Source
		pr := picResult{Source: pend.Source, URL: "/settings/lists/library/picture/" + f.Token, Title: s.i18n.T(lang, "artf.res.web")}
		size := humanBytes(lang, int64(len(pend.JPEG)))
		vals := []string{"host", pend.Host, "file", pend.File, "fw", strconv.Itoa(pend.FromW), "fh", strconv.Itoa(pend.FromH), "tw", strconv.Itoa(pend.ToW), "th", strconv.Itoa(pend.ToH), "size", size}
		if pend.Source == "upload" {
			pr.Title = s.i18n.T(lang, "artf.res.upload")
			pr.Text = s.i18n.Format(lang, i18n.Message{Key: "artf.res.upload.text", Params: pairs(vals)})
		} else {
			pr.Text = s.i18n.Format(lang, i18n.Message{Key: "artf.res.web.text", Params: pairs(vals)})
		}
		res = &pr
	case existing != nil && existing.HasPicture && !f.Replacing:
		source = existing.PicSource
		key := "artf.res.current.web"
		if existing.PicSource == "upload" {
			key = "artf.res.current.upload"
		}
		w, h := s.storedSize(p, existing.ID)
		res = &picResult{Source: existing.PicSource, URL: pictureURL(*existing), Title: s.i18n.T(lang, "artf.res.current"), CanRemove: true,
			Text: s.i18n.Format(lang, i18n.Msg(key, "date", p.day(existing.PictureAt), "w", strconv.Itoa(w), "h", strconv.Itoa(h)))}
	}
	if res != nil {
		f.Token = tokenOf(pend, f.Token)
	}

	// The symbol panel.
	var tiles []symTile
	var symHead, symLegend, symNone string
	var chosen *symTile
	if sy, ok := library.SymbolByEmoji(f.Symbol); ok && f.Symbol != "" {
		chosen = &symTile{Emoji: f.Symbol, Name: library.SymbolName(sy, lang), Checked: true}
	}
	var capped bool
	if q := strings.TrimSpace(f.SymQ); q != "" {
		found := library.SearchSymbols(q, lang)
		if len(found) == 0 {
			symNone = s.i18n.Format(lang, i18n.Msg("artf.sym.none", "q", q))
		} else {
			key := "artf.sym.count.many"
			if len(found) == 1 {
				key = "artf.sym.count.one"
			}
			symHead = s.i18n.Format(lang, i18n.Msg(key, "n", strconv.Itoa(len(found)), "q", q))
			symLegend = s.i18n.Format(lang, i18n.Msg("artf.sym.results", "q", q))
			if capped = len(found) > maxSymbolTiles; capped {
				found = found[:maxSymbolTiles]
			}
			for _, x := range found {
				tiles = append(tiles, symTile{x.Emoji, library.SymbolName(x, lang), chosen != nil && library.CanonEmoji(x.Emoji) == library.CanonEmoji(chosen.Emoji)})
			}
		}
	} else {
		symLegend = s.i18n.Format(lang, i18n.Msg("artf.sym.dept", "dept", deptName))
		for _, e := range library.DeptSymbols(f.Dept) {
			if x, ok := library.SymbolByEmoji(e); ok {
				tiles = append(tiles, symTile{x.Emoji, library.SymbolName(x, lang), chosen != nil && library.CanonEmoji(x.Emoji) == library.CanonEmoji(chosen.Emoji)})
			}
		}
	}
	if chosen != nil && !slices.ContainsFunc(tiles, func(t symTile) bool { return t.Checked }) && symNone == "" && f.SymQ == "" {
		tiles = append([]symTile{*chosen}, tiles...)
	}
	articleName := strings.TrimSpace(f.Name)
	if articleName == "" {
		articleName = s.i18n.T(lang, "artf.sym.article")
	}
	chosenText, chosenLabel := "", ""
	if chosen != nil {
		chosenText = s.i18n.Format(lang, i18n.Msg("artf.sym.where", "article", articleName))
		chosenLabel = s.i18n.Format(lang, i18n.Msg("artf.sym.chosen", "name", chosen.Name))
	}

	showFallback := res == nil && chosen == nil
	if source == "symbol" && chosen != nil {
		showFallback = false
	}
	if res == nil && source != "symbol" && source != "upload" && source != "web" {
		source = ""
	}

	// Units.
	chips := func(selected, other string) []unitChip {
		keys := slices.Clone(active)
		for _, k := range []string{f.Unit1, f.Unit2} {
			if unit.Valid(k) && !slices.Contains(keys, k) {
				keys = append(keys, k)
			}
		}
		slices.SortStableFunc(keys, func(a, b string) int { return slices.Index(unit.All, a) - slices.Index(unit.All, b) })
		var out []unitChip
		for _, k := range keys {
			c := unitChip{Key: k, Name: s.unitLong(lang, k), Checked: k == selected}
			if other != "" && k == other {
				c.Same, c.Checked = true, false
				c.Name = s.i18n.Format(lang, i18n.Msg("artf.units.same", "unit", s.unitLong(lang, k)))
			}
			out = append(out, c)
		}
		return out
	}
	units1 := chips(f.Unit1, "")
	units2 := chips(f.Unit2, f.Unit1)

	kinds := make([]kindCard, 0, len(library.Kinds))
	for _, k := range library.Kinds {
		kinds = append(kinds, kindCard{string(k), s.i18n.T(lang, "artf.kind."+string(k)), s.i18n.T(lang, "artf.kind."+string(k)+".desc"), string(k) == f.Kind})
	}

	cards := make([]allergenCard2, 0, len(food.Allergens))
	for _, a := range food.Allergens {
		var who []string
		for _, x := range st.persons {
			if slices.Contains(x.Allergens, a) {
				who = append(who, x.ID)
			}
		}
		c := allergenCard2{Key: a, Name: s.allergenName(lang, a), Examples: s.i18n.T(lang, "allergen."+a+".examples"), Checked: slices.Contains(f.Allergens, a)}
		if len(who) > 0 {
			key := "artf.aff.one"
			if len(who) > 1 {
				key = "artf.aff.many"
			}
			c.Aff = template.HTML(repl(html.EscapeString(s.i18n.T(lang, key)), "{who}", s.whoHTML(st, who)))
		}
		cards = append(cards, c)
	}
	collatedSort(lang, cards)

	first := ""
	for _, field := range []string{"name", "photo", "url", "symbol", "unit1", "unit2", "kind", "allergens"} {
		if errs[field] != "" {
			first = field
			break
		}
	}

	data := map[string]any{
		"Person": p.person, "SyncStale": s.syncStale(p.household.ID),
		"BackHref": libraryPath, "BackText": s.i18n.T(lang, "art.back"),
		"New": existing == nil, "F": f, "Depts": depts, "DeptName": deptName, "DeptSymbol": library.DeptSymbol(f.Dept),
		"Source": source, "Result": res, "ShowFallback": showFallback,
		"FallbackText": s.i18n.Format(lang, i18n.Msg("artf.pic.fallback", "dept", deptName)),
		"Chosen":       chosen, "ChosenText": chosenText, "ChosenLabel": chosenLabel, "Tiles": tiles, "SymHead": symHead, "SymLegend": symLegend, "SymNone": symNone, "SymSearching": strings.TrimSpace(f.SymQ) != "",
		"SymMore": s.i18n.Format(lang, i18n.Msg("artf.sym.more", "count", s.symbolCount(lang))), "SymCapped": capped,
		"Units1": units1, "Units2": units2, "Kinds": kinds, "Cards": cards, "IsNonFood": f.Kind == string(library.NonFood),
		"ImageNote": s.noteFor(r, outbound.Images, "outside.note.image"), "Errs": errs, "FirstErr": first, "Waiting": s.i18n.T(lang, "artf.url.busy"),
		"Token": f.Token, "ID": "", "Name": "",
	}
	if existing != nil {
		o := *existing
		it := articleItem{Article: ownArticle(o), own: &o}
		data["ID"], data["Name"], data["Symbol"] = o.ID, o.Name, symbolOf(it)
		if o.HasPicture {
			data["Picture"] = pictureURL(o)
		}
		from := s.i18n.Format(lang, i18n.Msg("art.from.own_anon", "date", p.day(o.CreatedAt)))
		if c, ok := st.chips[o.CreatedBy]; ok {
			from = s.i18n.Format(lang, i18n.Msg("art.from.own", "name", c.Name, "date", p.day(o.CreatedAt)))
		}
		data["From"] = from
		data["Warns"] = s.warnLines(st, st.warnings(it))
		onLists, err := s.listsWith(p, o.Name, false)
		if err != nil {
			s.fail(w, "lists", err)
			return
		}
		data["OnLists"] = s.listLinks(lang, onLists)
		data["HasLists"] = len(onLists) > 0
		stays, err := s.listsWith(p, o.Name, true)
		if err != nil {
			s.fail(w, "lists", err)
			return
		}
		names := make([]string, len(stays))
		for i, l := range stays {
			names[i] = l.Name
		}
		data["Stays"] = names
		data["DeleteExplain"] = s.i18n.Format(lang, i18n.Msg("art.delete.explain", "name", o.Name))
		if len(stays) == 0 {
			data["DeleteExplain"] = s.i18n.Format(lang, i18n.Msg("art.delete.explain_none", "name", o.Name))
		}
		data["DeleteConfirm"] = s.i18n.Format(lang, i18n.Msg("art.delete.confirm", "name", o.Name))
	}
	s.render(w, r, status, "article_form.html", data)
}

// symbolCount is the number of symbols rounded down to hundreds, written as a number of the language.
func (s *server) symbolCount(lang string) string {
	n := library.SymbolCount() / 100 * 100
	sep := "."
	if lang == "en" {
		sep = ","
	}
	return strconv.Itoa(n/1000) + sep + strconv.Itoa(n%1000/100) + "00"
}

func tokenOf(pend *pendingPicture, token string) string {
	if pend == nil {
		return ""
	}
	return token
}

func pairs(kv []string) map[string]string {
	m := map[string]string{}
	for i := 0; i+1 < len(kv); i += 2 {
		m[kv[i]] = kv[i+1]
	}
	return m
}

func collatedSort(lang string, cards []allergenCard2) {
	names := make([]string, len(cards))
	for i, c := range cards {
		names[i] = c.Name
	}
	sortNames(lang, names)
	slices.SortFunc(cards, func(a, b allergenCard2) int { return slices.Index(names, a.Name) - slices.Index(names, b.Name) })
}

func (s *server) handleArticleNew(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	st, err := s.libState(p, s.langOf(r))
	if err != nil {
		s.fail(w, "library state", err)
		return
	}
	f := artForm{Dept: library.DefaultDept, Name: strings.TrimSpace(r.URL.Query().Get("name"))}
	if len([]rune(f.Name)) > library.MaxName {
		f.Name = string([]rune(f.Name)[:library.MaxName])
	}
	s.renderArticleForm(w, r, p, http.StatusOK, st, nil, f, nil, nil)
}

func (s *server) handleArticleCreate(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	s.submitArticle(w, r, p, "")
}

func (s *server) handleArticleSave(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	s.submitArticle(w, r, p, r.PathValue("id"))
}

// readForm reads the posted form, multipart when a photo may come with it.
func (s *server) readForm(w http.ResponseWriter, r *http.Request) (f artForm, file *multipart.FileHeader, err error) {
	r.Body = http.MaxBytesReader(w, r.Body, maxForm)
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		err = r.ParseMultipartForm(4 << 20)
		if err == nil && r.MultipartForm != nil {
			if fs := r.MultipartForm.File["photo"]; len(fs) > 0 && fs[0].Size > 0 {
				file = fs[0]
			}
		}
	} else {
		err = r.ParseForm()
	}
	if err != nil {
		return f, nil, err
	}
	v := r.PostFormValue
	f = artForm{
		Name: v("name"), Dept: v("dept"), Kind: v("kind"), Unit1: v("unit1"), Unit2: v("unit2"), Allergens: r.PostForm["allergens"],
		PicSource: v("pic_source"), Symbol: v("symbol"), SymQ: strings.TrimSpace(v("symq")), URL: strings.TrimSpace(v("url")), Token: v("picture_token"), Replacing: v("replacing") != "",
	}
	return f, file, nil
}

// submitArticle handles the posted form of a new (existingID empty) or an
// existing own article: pictures are fetched or processed, symbols searched,
// or the article saved.
func (s *server) submitArticle(w http.ResponseWriter, r *http.Request, p principal, existingID string) {
	lang := s.langOf(r)
	st, err := s.libState(p, lang)
	if err != nil {
		s.fail(w, "library state", err)
		return
	}
	var existing *store.OwnArticle
	if existingID != "" {
		o, err := s.db.Article(p.household.ID, existingID)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		existing = &o
	}
	f, file, err := s.readForm(w, r)
	errs := map[string]string{}
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			errs["photo"] = s.i18n.T(lang, "artf.err.toobig")
			s.renderArticleForm(w, r, p, http.StatusRequestEntityTooLarge, st, existing, artForm{Dept: library.DefaultDept}, nil, errs)
			return
		}
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	now := s.now()
	pend, havePend := s.pictures.get(f.Token, now)
	if !havePend {
		f.Token = ""
	}
	action := "save"
	switch {
	case r.PostFormValue("symsearch") != "" || r.PostFormValue("symclear") != "":
		action = "symsearch"
		if r.PostFormValue("symclear") != "" {
			f.SymQ = ""
		}
	case r.PostFormValue("fetch") != "":
		action = "fetch"
	case r.PostFormValue("picture") == "replace":
		action = "replace"
	case r.PostFormValue("picture") == "remove" && existing != nil:
		action = "remove"
	}
	if action == "replace" {
		s.pictures.drop(f.Token)
		f.Token, havePend, f.Replacing = "", false, true
		if f.PicSource == "" {
			f.PicSource = pend.Source
		}
	}

	// A photo that came with the form, or the address of a web image.
	if havePend && action != "replace" && (f.PicSource == "" || f.PicSource == pend.Source) {
		f.PicSource = pend.Source
	}
	if file != nil && f.PicSource == "upload" && action != "replace" {
		if res, perr := s.processUpload(r.Context(), file); perr != nil {
			errs["photo"] = s.photoError(lang, file, perr)
		} else {
			s.pictures.drop(f.Token)
			pend, havePend = pendingFrom(res, "upload", "", file.Filename), true
			f.Token = newToken()
			s.pictures.put(f.Token, pend, now)
		}
	}
	if f.PicSource == "web" && action != "replace" && (action == "fetch" || (action == "save" && f.URL != "" && !(havePend && pend.Source == "web"))) {
		switch {
		case f.URL == "":
			errs["url"] = s.i18n.T(lang, "artf.err.url.empty")
		default:
			if res, ferr := s.fetcher.Fetch(r.Context(), f.URL); ferr != nil {
				errs["url"] = s.urlError(lang, f.URL, ferr)
			} else {
				s.pictures.drop(f.Token)
				pend, havePend = pendingFrom(res.Result, "web", res.Host, ""), true
				f.Token = newToken()
				s.pictures.put(f.Token, pend, now)
				f.URL = ""
			}
		}
	}
	var pendPtr *pendingPicture
	if havePend {
		pendPtr = &pend
	}

	if action == "fetch" || action == "symsearch" || action == "replace" {
		status := http.StatusOK
		if len(errs) > 0 {
			status = http.StatusUnprocessableEntity
		}
		s.renderArticleForm(w, r, p, status, st, existing, f, pendPtr, errs)
		return
	}

	// Saving. A picture source counts only when something was chosen with it.
	eff := f.PicSource
	switch {
	case eff == "symbol" && f.Symbol == "":
		eff = ""
	case (eff == "upload" || eff == "web") && !(havePend && pend.Source == eff):
		eff = ""
	}
	in := library.Input{Name: f.Name, Dept: f.Dept, Kind: f.Kind, Allergens: f.Allergens, Unit1: f.Unit1, Unit2: f.Unit2}
	if eff == "symbol" {
		in.PicSource, in.Symbol = "symbol", f.Symbol
	}
	for _, e := range in.Validate(s.activeUnitsFor(p, existing)) {
		if _, dup := errs[e.Field]; !dup {
			errs[e.Field] = s.fieldError(lang, e)
		}
	}
	if len(errs) > 0 {
		s.renderArticleForm(w, r, p, http.StatusUnprocessableEntity, st, existing, f, pendPtr, errs)
		return
	}
	in = in.Normalized()
	own := store.OwnArticle{Name: in.Name, Dept: in.Dept, Kind: in.Kind, Allergens: in.Allergens, Unit1: in.Unit1, Unit2: in.Unit2, PicSource: in.PicSource, Symbol: in.Symbol}
	var pic *store.ArticlePicture
	if (eff == "upload" || eff == "web") && havePend {
		pc := pend.ArticlePicture
		pic = &pc
		own.PicSource = eff
	}
	var saved store.OwnArticle
	switch {
	case existing == nil:
		saved, err = s.db.CreateArticle(p.household.ID, p.person.ID, own, pic)
	default:
		own.ID = existing.ID
		change := store.KeepPicture
		switch {
		case action == "remove":
			change, own.PicSource, own.Symbol = store.RemovePicture, "", ""
		case pic != nil:
			change = store.SetPicture
		case eff == "symbol":
			change = store.RemovePicture
		}
		saved, err = s.db.UpdateArticle(p.household.ID, own, change, pic)
	}
	if err != nil {
		s.fail(w, "save article", err)
		return
	}
	s.pictures.drop(f.Token)
	if existing == nil {
		s.redirectSavedNamed(w, r, libraryPath, "flash.article_created", saved.Name)
		return
	}
	s.redirectSavedNamed(w, r, libraryPath+"/"+saved.ID, "flash.article_saved", saved.Name)
}

// activeUnitsFor is the units the form may use: the ones switched on, and the
// ones an existing article already has (they stay, so saving does not change them).
func (s *server) activeUnitsFor(p principal, existing *store.OwnArticle) []string {
	active := slices.Clone(s.activeUnits(p.household.ID))
	if existing != nil {
		for _, k := range []string{existing.Unit1, existing.Unit2} {
			if k != "" && !slices.Contains(active, k) {
				active = append(active, k)
			}
		}
	}
	return active
}

func (s *server) processUpload(ctx context.Context, h *multipart.FileHeader) (picture.Result, error) {
	if h.Size > picture.MaxBytes {
		return picture.Result{}, &picture.TooLargeError{Bytes: h.Size}
	}
	f, err := h.Open()
	if err != nil {
		return picture.Result{}, err
	}
	defer f.Close()
	return picture.ProcessContext(ctx, io.LimitReader(f, picture.MaxBytes+1))
}

func pendingFrom(res picture.Result, source, host, file string) pendingPicture {
	return pendingPicture{
		ArticlePicture: store.ArticlePicture{JPEG: res.JPEG, Source: source, Host: host, FromW: res.FromW, FromH: res.FromH, FromBytes: res.FromBytes},
		File:           file, ToW: res.ToW, ToH: res.ToH,
	}
}

// fieldError is the sentence for a refused field of the form.
func (s *server) fieldError(lang string, e library.FieldErr) string {
	switch e.Field + "." + e.Code {
	case "name.required":
		return s.i18n.T(lang, "artf.err.name.required")
	case "name.long":
		return s.i18n.T(lang, "artf.err.name.long")
	case "kind.required", "kind.unknown":
		return s.i18n.T(lang, "artf.err.kind")
	case "unit2.same":
		return s.i18n.T(lang, "artf.err.unit2")
	case "unit1.off", "unit2.off":
		return s.i18n.T(lang, "artf.err.unit.off")
	case "symbol.unknown":
		return s.i18n.T(lang, "artf.err.symbol")
	}
	return s.i18n.T(lang, "artf.err.unit1")
}

func (s *server) photoError(lang string, h *multipart.FileHeader, err error) string {
	file := h.Filename
	var big *picture.TooLargeError
	switch {
	case errors.As(err, &big):
		size := big.Bytes
		if h.Size > size {
			size = h.Size
		}
		return s.i18n.Format(lang, i18n.Msg("artf.err.photo.big", "file", file, "size", humanBytes(lang, size)))
	case errors.Is(err, picture.ErrWebPage):
		return s.i18n.Format(lang, i18n.Msg("artf.err.photo.webpage", "file", file))
	case errors.Is(err, picture.ErrTooManyPixels):
		return s.i18n.Format(lang, i18n.Msg("artf.err.photo.pixels", "file", file))
	}
	return s.i18n.Format(lang, i18n.Msg("artf.err.photo.notimage", "file", file))
}

func (s *server) urlError(lang, raw string, err error) string {
	var private *picture.PrivateError
	var big *picture.TooLargeError
	var status *picture.StatusError
	switch {
	case errors.Is(err, outbound.ErrRefused):
		return s.i18n.T(lang, "outside.note.image")
	case errors.As(err, &private):
		return s.i18n.Format(lang, i18n.Msg("artf.err.url.private", "addr", private.Addr))
	case errors.Is(err, picture.ErrWebPage):
		return s.i18n.T(lang, "artf.err.url.webpage")
	case errors.Is(err, picture.ErrScheme):
		return s.i18n.T(lang, "artf.err.url.scheme")
	case errors.Is(err, picture.ErrAddress):
		return s.i18n.T(lang, "artf.err.url.address")
	case errors.Is(err, picture.ErrTimeout):
		return s.i18n.T(lang, "artf.err.url.timeout")
	case errors.Is(err, picture.ErrTooManyRedirects):
		return s.i18n.T(lang, "artf.err.url.redirects")
	case errors.As(err, &big):
		return s.i18n.Format(lang, i18n.Msg("artf.err.url.big", "size", humanBytes(lang, big.Bytes)))
	case errors.As(err, &status):
		return s.i18n.Format(lang, i18n.Msg("artf.err.url.status", "code", strconv.Itoa(status.Code)))
	case errors.Is(err, picture.ErrTooManyPixels):
		return s.i18n.T(lang, "artf.err.url.pixels")
	case errors.Is(err, picture.ErrNotAnImage):
		return s.i18n.T(lang, "artf.err.url.notimage")
	}
	return s.i18n.Format(lang, i18n.Msg("artf.err.url.unreachable", "host", addrHost(raw)))
}

func addrHost(raw string) string {
	raw = strings.TrimSpace(raw)
	for _, p := range []string{"https://", "http://"} {
		if strings.HasPrefix(strings.ToLower(raw), p) {
			raw = raw[len(p):]
		}
	}
	if i := strings.IndexAny(raw, "/?#"); i >= 0 {
		raw = raw[:i]
	}
	return raw
}

// handlePendingPicture shows a picture that waits for its article to be saved.
func (s *server) handlePendingPicture(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.parentPage(w, r); !ok {
		return
	}
	pend, ok := s.pictures.get(r.PathValue("token"), s.now())
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(pend.JPEG)
}

// storedSize is the size in pixels of the stored picture of an own article.
func (s *server) storedSize(p principal, id string) (w, h int) {
	b, err := s.db.ArticlePicture(p.household.ID, id)
	if err != nil {
		return 0, 0
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(b))
	if err != nil {
		return 0, 0
	}
	return cfg.Width, cfg.Height
}
