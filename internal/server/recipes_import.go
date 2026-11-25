package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"html"
	"html/template"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/hejmo-home/hejmo/internal/library"
	"github.com/hejmo-home/hejmo/internal/nutrition"
	"github.com/hejmo-home/hejmo/internal/outbound"
	"github.com/hejmo-home/hejmo/internal/picture"
	"github.com/hejmo-home/hejmo/internal/recipe"
	"github.com/hejmo-home/hejmo/internal/recipeimport"
	"github.com/hejmo-home/hejmo/internal/recipephoto"
	"github.com/hejmo-home/hejmo/internal/store"
	"github.com/hejmo-home/hejmo/internal/unit"
)

// Importing a recipe from a web page (ticket 289, ADR 0021 and 0024, preview-289.html). Two steps and nothing is
// saved unchecked: the box reads the page into a draft that belongs to the parent who read it and lives an hour;
// the parent checks the recipe form, already filled in, and saves it. The page and its picture are read through
// the Gate as the connection "recipes".

const (
	recipeImportPath = recipesPath + "/import"
	draftTTL         = time.Hour
	// maxCandidates is how many recipes of one page are offered.
	maxCandidates = 10
	maxURL        = 2000
)

// draftData is what a draft holds: every recipe the page described, and the pictures made from their images (by
// recipe number), already as the JPEG that is stored.
type draftData struct {
	Candidates []recipeimport.Found `json:"candidates"`
	Pictures   map[int][]byte       `json:"pictures,omitempty"`
}

// importErr is a page that could not be read, as a kind the pages turn into a sentence.
type importErr struct {
	Kind  string // empty, notlink, private, dns, timeout, gone, refused, notpage, big, norecipe, unreachable
	Host  string
	Extra string
}

func (e *importErr) Error() string { return "recipe import: " + e.Kind }

// importOff says why the import is not possible now: "" when it is, else "off" (the connection is switched off)
// or "offline" (offline mode).
func (s *server) importOff() string {
	switch s.gate.Verdict(outbound.Recipes) {
	case outbound.VerdictOffline:
		return "offline"
	case outbound.VerdictSwitchedOff:
		return "off"
	}
	return ""
}

func (s *server) sweepDrafts() {
	if err := s.db.ExpireRecipeDrafts(s.now().Add(-draftTTL), s.now()); err != nil {
		s.logger.Error("expire recipe drafts", "err", err)
	}
}

// readRecipePage reads the page at the typed link into a draft's data. The returned address is the page's own
// after redirects, without a fragment: it is the recipe's source.
func (s *server) readRecipePage(ctx context.Context, typed string) (link, host string, data draftData, ierr *importErr) {
	typed = strings.TrimSpace(typed)
	if typed == "" {
		return "", "", data, &importErr{Kind: "empty"}
	}
	u, err := url.Parse(typed)
	if err != nil || utf8.RuneCountInString(typed) > maxURL || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || strings.ContainsAny(typed, " \t\r\n") {
		return "", "", data, &importErr{Kind: "notlink"}
	}
	host = strings.ToLower(u.Hostname())
	page, err := s.recipes.Fetch(ctx, typed)
	if err != nil {
		return "", host, data, classifyImport(err, host)
	}
	found, err := recipeimport.Extract(page.Body)
	if err != nil {
		return "", host, data, &importErr{Kind: "norecipe", Host: host}
	}
	if len(found) > maxCandidates {
		found = found[:maxCandidates]
	}
	data.Candidates = found
	final := *page.URL
	final.Fragment = ""
	link, host = final.String(), page.Host
	data.Pictures = s.draftPictures(ctx, page.URL, found)
	return link, host, data, nil
}

func classifyImport(err error, host string) *importErr {
	var private *recipeimport.PrivateError
	var status *recipeimport.StatusError
	var notHTML *recipeimport.NotHTMLError
	switch {
	case errors.As(err, &private):
		return &importErr{Kind: "private", Host: strings.Trim(private.Addr, "[]")}
	case errors.Is(err, recipeimport.ErrScheme), errors.Is(err, recipeimport.ErrAddress):
		return &importErr{Kind: "notlink"}
	case errors.Is(err, recipeimport.ErrTimeout):
		return &importErr{Kind: "timeout", Host: host}
	case errors.Is(err, recipeimport.ErrUnknownHost):
		return &importErr{Kind: "dns", Host: host}
	case errors.Is(err, recipeimport.ErrTooLarge):
		return &importErr{Kind: "big", Host: host}
	case errors.As(err, &status):
		if status.Code == http.StatusNotFound || status.Code == http.StatusGone {
			return &importErr{Kind: "gone", Host: host}
		}
		if status.Code == http.StatusForbidden || status.Code == http.StatusTooManyRequests || status.Code == http.StatusUnauthorized {
			return &importErr{Kind: "refused", Host: host}
		}
	case errors.As(err, &notHTML):
		kind := notHTML.Type
		if kind == "application/pdf" {
			kind = "PDF"
		}
		return &importErr{Kind: "notpage", Host: host, Extra: kind}
	case errors.Is(err, outbound.ErrRefused):
		return &importErr{Kind: "off"}
	}
	return &importErr{Kind: "unreachable", Host: host}
}

// picturesBudget is the longest all pictures of a page take together; each has its own shorter timeout.
const picturesBudget = 20 * time.Second

// pageFetcher is the box's picture fetcher as the pictures of a page use it: the connection "recipes" through the
// box's gate, and number addresses refused exactly where the page's own fetcher refuses them.
func (s *server) pageFetcher() picture.Fetcher {
	pf := *s.fetcher
	pf.Gate = s.gate
	pf.Service = outbound.Recipes
	pf.NoIPLiteral = pf.NoIPLiteral || !s.recipes.AllowIPLiteral
	return pf
}

// draftPictures downloads the image of each recipe, once per address, as the stored picture (at most 1600 px).
// A picture that cannot be read is left out: the recipe is still imported.
func (s *server) draftPictures(ctx context.Context, base *url.URL, found []recipeimport.Found) map[int][]byte {
	out := map[int][]byte{}
	byURL := map[string][]byte{}
	// The picture is part of the page's connection: the box's own gate with the switch "recipes" (not
	// "images"), names only (the same rule as the page, ADR 0021). All pictures together get one budget.
	ctx, cancel := context.WithTimeout(ctx, picturesBudget)
	defer cancel()
	pf := s.pageFetcher()
	for i, f := range found {
		if f.ImageURL == "" {
			continue
		}
		ref, err := url.Parse(f.ImageURL)
		if err != nil {
			continue
		}
		abs := base.ResolveReference(ref).String()
		jpeg, seen := byURL[abs]
		if !seen {
			if res, err := pf.FetchMax(ctx, abs, picture.RecipeMaxSide); err == nil {
				jpeg = res.JPEG
			}
			byURL[abs] = jpeg
		}
		if len(jpeg) > 0 {
			out[i] = jpeg
		}
	}
	return out
}

// ---- the import page

type importPage struct {
	URL, Err, Host string
	ErrHTML        template.HTML
	NoRecipe       bool
}

func (c *itemCtx) importError(e *importErr, typed string) importPage {
	pg := importPage{URL: typed}
	if e == nil {
		return pg
	}
	text := html.EscapeString(c.t("recipes.import.err."+e.Kind, "host", e.Host, "type", e.Extra))
	// the website's name stands in bold
	for _, h := range []string{e.Host} {
		if h != "" {
			esc := html.EscapeString(h)
			text = strings.Replace(text, esc, `<span class="host">`+esc+`</span>`, 1)
		}
	}
	pg.ErrHTML = template.HTML(text) // #nosec G203 -- escaped, the markup is ours
	pg.NoRecipe = e.Kind == "norecipe"
	return pg
}

// importPageData is the import page: the form, or the notice that replaces it.
func (c *itemCtx) importData(why string, e *importErr, typed string) map[string]any {
	d := map[string]any{"Title": c.t("recipes.import.title"), "Lead": c.t("recipes.import.lead"), "NewHref": recipesPath + "/new"}
	if why != "" {
		link := `<a href="/settings/connections">` + html.EscapeString(c.t("recipes.import.settings.link")) + `</a>`
		key := "recipes.import.off"
		if why == "offline" {
			key = "recipes.import.offline"
		}
		d["Off"], d["OffOffline"] = true, why == "offline"
		d["OffTitle"] = c.t(key + ".title")
		d["OffText"] = template.HTML(strings.Replace(html.EscapeString(c.s.i18n.T(c.lang, key+".text")), "{link}", link, 1)) // #nosec G203 -- escaped, the link is ours
		return d
	}
	pg := c.importError(e, typed)
	d["Page"] = pg
	if pg.NoRecipe {
		d["WriteHref"] = recipesPath + "/new?source=" + url.QueryEscape(strings.TrimSpace(typed))
	}
	d["WaitFormat"] = c.t("recipes.import.wait", "host", "{host}")
	return d
}

func (s *server) handleImportForm(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	c, err := s.itemContext(p, s.langOf(r))
	if err != nil {
		s.fail(w, "recipe context", err)
		return
	}
	typed := ""
	if q := r.URL.Query().Get("url"); recipe.SourceLink(q) != "" {
		typed = q
	}
	s.render(w, r, http.StatusOK, "recipe_import.html", c.importData(s.importOff(), nil, typed))
}

func (s *server) handleImportRead(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	c, err := s.itemContext(p, s.langOf(r))
	if err != nil {
		s.fail(w, "recipe context", err)
		return
	}
	typed := r.PostFormValue("url")
	if why := s.importOff(); why != "" {
		s.render(w, r, http.StatusConflict, "recipe_import.html", c.importData(why, nil, typed))
		return
	}
	link, host, data, ierr := s.readRecipePage(r.Context(), typed)
	if ierr != nil && ierr.Kind == "off" {
		s.render(w, r, http.StatusConflict, "recipe_import.html", c.importData(s.importOff(), nil, typed))
		return
	}
	if ierr != nil {
		s.render(w, r, http.StatusUnprocessableEntity, "recipe_import.html", c.importData("", ierr, typed))
		return
	}
	s.sweepDrafts()
	draft, err := s.newDraft(p, link, host, data)
	if err != nil {
		s.fail(w, "recipe draft", err)
		return
	}
	http.Redirect(w, r, recipeImportPath+"/"+draft.ID, http.StatusSeeOther)
}

func (s *server) newDraft(p principal, link, host string, data draftData) (store.RecipeDraft, error) {
	raw, err := json.Marshal(data)
	if err != nil {
		return store.RecipeDraft{}, err
	}
	return s.db.AddRecipeDraft(p.household.ID, p.person.ID, link, host, raw, s.now())
}

// ---- the draft

// draftState is a draft of the signed-in parent, opened.
type draftState struct {
	d     store.RecipeDraft
	data  draftData
	which int
}

// loadDraft finds the draft of this parent: found is false for one that does not exist or belongs to another
// parent (404); expired is true once its hour is over.
func (s *server) loadDraft(p principal, id string) (st draftState, found, expired bool, err error) {
	s.sweepDrafts()
	d, err := s.db.RecipeDraft(p.household.ID, id)
	if errors.Is(err, store.ErrNotFound) || (err == nil && d.ReadBy != p.person.ID) {
		return st, false, false, nil
	}
	if err != nil {
		return st, false, false, err
	}
	st.d = d
	if d.Expired || !s.now().Before(d.ReadAt.Add(draftTTL)) {
		return st, true, true, nil
	}
	if err := json.Unmarshal(d.Data, &st.data); err != nil || len(st.data.Candidates) == 0 {
		return st, true, true, nil
	}
	return st, true, false, nil
}

// which reads the recipe the parent chose: -1 while the page holds several and none was chosen.
func (st *draftState) choose(raw string) {
	n := len(st.data.Candidates)
	st.which = -1
	if n == 1 {
		st.which = 0
	}
	if v, err := strconv.Atoi(raw); err == nil && v >= 0 && v < n {
		st.which = v
	}
}

func (s *server) draftContext(w http.ResponseWriter, r *http.Request) (principal, *itemCtx, draftState, bool) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return p, nil, draftState{}, false
	}
	c, err := s.itemContext(p, s.langOf(r))
	if err != nil {
		s.fail(w, "recipe context", err)
		return p, nil, draftState{}, false
	}
	st, found, expired, err := s.loadDraft(p, r.PathValue("id"))
	switch {
	case err != nil:
		s.fail(w, "recipe draft", err)
		return p, c, st, false
	case !found:
		http.NotFound(w, r)
		return p, c, st, false
	case expired:
		d := c.importData("", nil, st.d.URL)
		d["Expired"], d["Title"] = true, c.t("recipes.import.review.title")
		d["AgainHref"] = recipeImportPath + "?" + url.Values{"url": {st.d.URL}}.Encode()
		d["Crumb"] = true
		s.render(w, r, http.StatusOK, "recipe_import.html", d)
		return p, c, st, false
	}
	raw := r.URL.Query().Get("which")
	if r.Method == http.MethodPost {
		raw = r.PostFormValue("which")
	}
	st.choose(raw)
	return p, c, st, true
}

func (s *server) handleImportDraft(w http.ResponseWriter, r *http.Request) {
	p, c, st, ok := s.draftContext(w, r)
	if !ok {
		return
	}
	if st.which < 0 {
		s.render(w, r, http.StatusOK, "recipe_import.html", c.whichData(st))
		return
	}
	known, err := s.db.RecipeTags(p.household.ID)
	if err != nil {
		s.fail(w, "recipe tags", err)
		return
	}
	f := c.formOfFound(st)
	c.formPage(w, r, "", "", f, nil, http.StatusOK, known)
}

func (s *server) handleImportCancel(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	if d, err := s.db.RecipeDraft(p.household.ID, r.PathValue("id")); err == nil && d.ReadBy == p.person.ID {
		_ = s.db.DeleteRecipeDraft(p.household.ID, d.ID)
	}
	http.Redirect(w, r, recipesPath, http.StatusSeeOther)
}

// whichData is the page that asks which of several recipes on one page to import.
func (c *itemCtx) whichData(st draftState) map[string]any {
	type opt struct {
		N       int
		Name    string
		Meta    string
		Checked bool
	}
	var opts []opt
	for i, f := range st.data.Candidates {
		var parts []string
		parts = append(parts, c.count("recipes.import.which.ings", len(f.Ingredients)), c.count("recipes.import.which.steps", len(f.Steps)))
		if f.Minutes > 0 {
			parts = append(parts, c.t("recipes.import.which.minutes", "n", strconv.Itoa(f.Minutes)))
		}
		if len(st.data.Pictures[i]) > 0 {
			parts = append(parts, c.t("recipes.import.which.picture"))
		}
		name := f.Name
		if name == "" {
			name = c.t("recipes.import.which.noname")
		}
		opts = append(opts, opt{i, name, strings.Join(parts, " · "), i == 0})
	}
	return map[string]any{
		"Which": true, "Title": c.t("recipes.import.which.title"), "Crumb": true, "CrumbTitle": c.t("recipes.import.which.crumb"),
		"Lead":    template.HTML(strings.Replace(html.EscapeString(c.t("recipes.import.which.lead", "host", "\x00", "n", strconv.Itoa(len(opts)))), "\x00", "<b>"+html.EscapeString(st.d.Host)+"</b>", 1)), // #nosec G203 -- escaped
		"Options": opts, "Action": recipeImportPath + "/" + st.d.ID, "CancelHref": recipeImportPath + "/" + st.d.ID + "/cancel",
	}
}

// ---- the form of a recipe read from a page

// webLine is what the page said about an ingredient line.
type webLine struct {
	Original, Change string
}

// webForm is what the review of an imported recipe adds to the recipe form.
type webForm struct {
	DraftID, Host, URL string
	Which              int
	Cand               recipeimport.Found
	Picture            bool // the switch "Bild übernehmen"
	HasPicture         bool
	NeedCount          int
	ReadAt             time.Time
	ReadBy             string
	Keywords           []string
	Ings               []recipeimport.Line
}

// clip cuts text to n letters.
func clip(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return strings.TrimSpace(string([]rune(s)[:n]))
}

// formOfFound fills the recipe form with what the page described. A line whose head names an article is matched
// with it (the rest after a comma or bracket is its variety); any other line stays as the page wrote it.
func (c *itemCtx) formOfFound(st draftState) rcpForm {
	cand := st.data.Candidates[st.which]
	f := rcpForm{Name: clip(cand.Name, recipe.MaxName), Category: cand.Category}
	if !recipe.ValidCategory(f.Category) {
		f.Category = string(recipe.Main)
	}
	n := cand.Portions
	if n < 1 {
		n = c.householdPortions()
	}
	f.Portions = strconv.Itoa(n)
	if cand.Minutes > 0 {
		f.Minutes = strconv.Itoa(cand.Minutes)
	}
	units := c.recipeUnits()
	for i, l := range cand.Ingredients {
		if i >= recipe.MaxIngredients {
			break
		}
		line := rcpIngLine{From: strconv.Itoa(i), Unit: l.Unit}
		if !slices.Contains(units, line.Unit) {
			line.Unit = ""
		}
		if l.Amount > 0 && l.Amount <= recipe.MaxAmount {
			line.Amount = recipe.FormatAmount(l.Amount, c.lang)
		}
		line.Name = clip(l.Head, recipe.MaxIngredient) // what follows a comma or bracket stays on the line of the page
		if _, art, err := c.recipeIngredient(recipe.IngredientInput{Amount: line.Amount, Unit: line.Unit, Name: l.Head}); err == nil && art != nil {
			if utf8.RuneCountInString(l.Rest) <= recipe.MaxVariety {
				line.Variety = l.Rest
			}
		}
		f.Ings = append(f.Ings, line)
	}
	for i, s := range cand.Steps {
		if i >= recipe.MaxSteps {
			break
		}
		f.Steps = append(f.Steps, rcpStepLine{Text: clip(s, recipe.MaxStep)})
	}
	f.Web = &webForm{DraftID: st.d.ID, Host: st.d.Host, URL: st.d.URL, Which: st.which, ReadAt: st.d.ReadAt, ReadBy: st.d.ReadByName, Cand: cand, Picture: true, HasPicture: len(st.data.Pictures[st.which]) > 0,
		Keywords: cand.Keywords, Ings: cand.Ingredients}
	return f
}

// decorateWeb adds what the page said and the choice to each line of a form made from a page: the line of the
// page, what Hejmo interpreted, and for a line Hejmo does not know the proposals. Called after the lines were resolved.
func (c *itemCtx) decorateWeb(f *rcpForm) {
	w := f.Web
	w.NeedCount = 0
	for i := range f.Ings {
		l := &f.Ings[i]
		l.Web, l.Needs, l.PickV, l.WebVariety = nil, false, nil, false
		k, err := strconv.Atoi(l.From)
		if l.From == "" || err != nil || k < 0 || k >= len(w.Ings) {
			continue
		}
		pl := w.Ings[k]
		l.Web = &webLine{Original: pl.Original}
		switch pl.Change {
		case "range":
			l.Web.Change = c.t("recipes.import.chg.range", "n", recipe.FormatAmount(pl.Amount, c.lang))
		case "some":
			l.Web.Change = c.t("recipes.import.chg.some")
		case "zero":
			l.Web.Change = c.t("recipes.import.chg.zero")
		}
		if l.State == ingArticle {
			l.WebVariety = l.Variety != "" && l.Variety == pl.Rest
			continue
		}
		if l.Blank || l.State != ingFree {
			continue
		}
		l.Needs = true
		if l.Pick == "" {
			w.NeedCount++
		}
		l.PickV = c.pickView(*l)
	}
}

type propView struct {
	Value, Name, Variety, Sub string
	Warns                     []template.HTML
	Checked                   bool
}

// pickView is the question under a line Hejmo does not know: up to three proposals of the library, an own
// article, or free text. Nothing is preselected: a silent default would weaken the allergy check.
type pickView struct {
	Legend, Hint, OwnName, OwnSub, FreeName, FreeSub, NeedText string
	Props                                                      []propView
	OwnChecked, FreeChecked                                    bool
	OwnValue, FreeValue                                        string
}

func (c *itemCtx) pickView(l rcpIngLine) *pickView {
	name := l.Name
	v := &pickView{
		NeedText: c.t("recipes.import.need.text", "name", name), Legend: c.t("recipes.import.pick.legend", "name", name), Hint: c.t("recipes.import.pick.hint"),
		OwnName: c.t("recipes.import.pick.own", "name", name), OwnSub: c.t("recipes.import.pick.own.sub", "name", name),
		FreeName: c.t("recipes.import.pick.free"), FreeSub: c.t("recipes.import.pick.free.sub", "name", name),
		OwnValue: "own", FreeValue: "free", OwnChecked: l.Pick == "own", FreeChecked: l.Pick == "free",
	}
	for _, pr := range recipe.Propose(name, c.visibleArticles(), c.lang, 3) {
		it, ok := c.article(pr.Article.ID)
		if !ok {
			continue
		}
		value := "art:" + it.ID + "|" + pr.Variety
		pv := propView{Value: value, Name: it.Name(c.lang), Variety: pr.Variety, Checked: l.Pick == value, Warns: c.warnLines(library.Warnings(it.Article, c.st.persons))}
		pv.Sub = c.t("recipes.import.pick.article.plain")
		if pr.Variety != "" {
			pv.Sub = c.t("recipes.import.pick.article", "variety", pr.Variety)
		}
		v.Props = append(v.Props, pv)
	}
	return v
}

// ---- saving

func (s *server) handleImportSave(w http.ResponseWriter, r *http.Request) {
	p, c, st, ok := s.draftContext(w, r)
	if !ok {
		return
	}
	if st.which < 0 { // the choice page posted: continue with the chosen one
		http.Redirect(w, r, recipeImportPath+"/"+st.d.ID, http.StatusSeeOther)
		return
	}
	known, err := s.db.RecipeTags(p.household.ID)
	if err != nil {
		s.fail(w, "recipe tags", err)
		return
	}
	base := c.formOfFound(st)
	f := readRcpForm(r)
	f.Web = base.Web
	f.Web.Picture = r.PostForm.Has("picture")
	f.Source = ""
	if f.act(r.PostFormValue("act")) {
		c.formPage(w, r, "", "", f, nil, http.StatusOK, known)
		return
	}
	// a proposal of the library that was chosen makes the line that article
	for i := range f.Ings {
		l := &f.Ings[i]
		if id, variety, _ := strings.Cut(strings.TrimPrefix(l.Pick, "art:"), "|"); strings.HasPrefix(l.Pick, "art:") && l.From != "" {
			if it, found := c.article(id); found {
				l.Name, l.Article, l.Variety = it.Name(c.lang), id, variety
			}
		}
	}
	existing, err := s.db.Recipes(p.household.ID)
	if err != nil {
		s.fail(w, "recipes", err)
		return
	}
	rec, is := c.buildRecipe(&f, known, existing, "")
	c.decorateWeb(&f)
	for i := range f.Ings {
		l := &f.Ings[i]
		if l.Needs && l.Pick == "" {
			l.PickErr = c.t("recipes.import.err.pick", "name", l.Name)
			if is == nil {
				is = &rcpIssues{More: map[string]string{}}
			}
			is.add("ing"+strconv.Itoa(l.N), c.t("recipes.err.link.ing", "n", strconv.Itoa(l.N)))
		}
	}
	if is != nil {
		c.formPage(w, r, "", "", f, is, http.StatusUnprocessableEntity, known)
		return
	}
	// the own articles the choices ask for, once per name
	var made []store.OwnArticle
	byName := map[string]string{}
	for i := range f.Ings {
		l := &f.Ings[i]
		if !l.Needs || l.Pick != "own" {
			continue
		}
		key := library.Fold(l.Name)
		if id, ok := byName[key]; ok {
			l.Article = id
			continue
		}
		own, err := s.db.CreateArticle(p.household.ID, p.person.ID, store.OwnArticle{Name: l.Name, Dept: library.DefaultDept, Kind: string(library.Veg), Unit1: c.ownUnit()}, nil)
		if err != nil {
			s.undoArticles(p, made)
			s.fail(w, "create article", err)
			return
		}
		made = append(made, own)
		byName[key] = own.ID
		l.Article = own.ID
	}
	if len(made) > 0 {
		if c, err = s.itemContext(p, s.langOf(r)); err != nil { // the library knows the new articles now
			s.undoArticles(p, made)
			s.fail(w, "recipe context", err)
			return
		}
		rec, is = c.buildRecipe(&f, known, existing, "")
		if is != nil {
			s.undoArticles(p, made)
			c.formPage(w, r, "", "", f, is, http.StatusUnprocessableEntity, known)
			return
		}
	}
	imp := store.Imported{URL: st.d.URL, Host: st.d.Host, Lang: st.data.Candidates[st.which].Lang, DraftID: st.d.ID}
	imp.Nutrition = pageNutrition(st.data.Candidates[st.which].Nutrition)
	saved, err := s.db.CreateImportedRecipe(p.household.ID, p.person.ID, rec, imp)
	var taken *store.RecipeNameTakenError
	switch {
	case errors.As(err, &taken):
		s.undoArticles(p, made)
		is := &rcpIssues{More: map[string]string{}}
		is.Name = c.nameTaken(f.Name, taken.ExistingID, nil)
		is.add("rcp", c.t("recipes.err.link.name"))
		for i := range f.Ings { // the own articles were taken back: ask again
			if f.Ings[i].Pick == "own" {
				f.Ings[i].Article = ""
			}
		}
		c.formPage(w, r, "", "", f, is, http.StatusUnprocessableEntity, known)
		return
	case err != nil:
		s.undoArticles(p, made)
		s.fail(w, "save imported recipe", err)
		return
	}
	if f.Web.Picture {
		s.addWebRecipePhoto(r.Context(), p, saved.ID, st.data.Pictures[st.which])
	}
	s.setNewArticles(w, r, saved.ID, made)
	s.redirectSavedFields(w, r, recipesPath+"/"+saved.ID, "flash.recipe_imported", saved.Name, st.d.Host)
}

// ownUnit is the unit a new own article starts with: the piece, else the household's first unit.
func (c *itemCtx) ownUnit() string {
	if slices.Contains(c.active, unit.Piece) || len(c.active) == 0 {
		return unit.Piece
	}
	return c.active[0]
}

func (s *server) undoArticles(p principal, made []store.OwnArticle) {
	for _, a := range made {
		_ = s.db.DeleteArticle(p.household.ID, a.ID)
	}
}

func pageNutrition(n *recipeimport.Nutrition) *store.PageNutrition {
	if n == nil {
		return nil
	}
	return &store.PageNutrition{KCal: n.KCal, KJ: n.KJ, Fat: n.Fat, Saturates: n.Saturates, Carbs: n.Carbs, Sugars: n.Sugars, Protein: n.Protein, Salt: n.Salt}
}

// ---- the own articles an import made, told once on the recipe page

const newArticlesCookie = "hejmo_new_articles"

func (s *server) setNewArticles(w http.ResponseWriter, r *http.Request, recipeID string, made []store.OwnArticle) {
	if len(made) == 0 {
		return
	}
	parts := []string{recipeID}
	for _, a := range made {
		parts = append(parts, a.ID)
	}
	http.SetCookie(w, &http.Cookie{Name: newArticlesCookie, Value: s.flashTexts.put(strings.Join(parts, ","), s.now()), Path: "/", MaxAge: int(flashTextTTL / time.Second),
		HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: r.TLS != nil})
}

// takeNewArticles returns the own articles the import of this recipe made, once, for the browser that saved it.
func (s *server) takeNewArticles(w http.ResponseWriter, r *http.Request, p principal, recipeID string) []store.OwnArticle {
	ck, err := r.Cookie(newArticlesCookie)
	if err != nil {
		return nil
	}
	text, ok := s.flashTexts.peek(ck.Value, s.now())
	ids := strings.Split(text, ",")
	if !ok || len(ids) < 2 || ids[0] != recipeID {
		return nil
	}
	s.flashTexts.take(ck.Value, s.now())
	http.SetCookie(w, &http.Cookie{Name: newArticlesCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: r.TLS != nil})
	var out []store.OwnArticle
	for _, id := range ids[1:] {
		if a, err := s.db.Article(p.household.ID, id); err == nil {
			out = append(out, a)
		}
	}
	return out
}

// srcLine is the fixed source of an imported recipe in its edit form: a line, not a field.
func (c *itemCtx) srcLine(imp store.RecipeImport) map[string]string {
	text := c.t("recipes.import.source.edit.anon", "date", c.p.day(imp.At))
	if imp.ByName != "" {
		text = c.t("recipes.import.source.edit", "name", imp.ByName, "date", c.p.day(imp.At))
	}
	return map[string]string{"Label": c.t("recipes.import.source.label"), "URL": imp.URL, "Hint": text}
}

type webNutView struct {
	Hint string
	Rows []nutRowView
}

func plainHTML(s string) template.HTML { return template.HTML(html.EscapeString(s)) } // #nosec G203 -- escaped

// webData adds to the page data of the form what only the review of a page has: the summary of what was read,
// the hints under the fields, the picture, the values of the page and the keywords.
func (c *itemCtx) webData(data map[string]any, f *rcpForm, is *rcpIssues) {
	w := f.Web
	cand := w.Cand
	data["Web"] = w
	data["Action"] = recipeImportPath + "/" + w.DraftID
	data["CancelHref"] = recipeImportPath + "/" + w.DraftID + "/cancel"
	// lines read from the page
	var imported, inLib int
	var unassigned []rcpIngLine
	for _, l := range f.Ings {
		if l.From == "" || l.Blank {
			continue
		}
		imported++
		switch {
		case l.State == ingArticle:
			inLib++
		case l.Needs && l.Pick == "":
			unassigned = append(unassigned, l)
		}
	}
	need := 0
	for _, l := range f.Ings {
		if l.Needs && l.Pick == "" {
			need++
		}
	}
	data["NeedCount"] = need
	data["DirtyText"] = c.dirtyText(need)
	data["DirtyNone"], data["DirtyOne"], data["DirtyOther"] = c.t("recipes.import.dirty.none"), c.t("recipes.import.dirty.one"), c.t("recipes.import.dirty.other", "n", "{n}")
	// the file card
	read := c.t("recipes.import.sum.read", "name", w.ReadBy, "date", c.p.day(w.ReadAt), "time", c.p.clockSeconds(w.ReadAt))
	if lang := cand.Lang; lang != "" && lang != c.lang {
		if name := c.s.i18n.T(c.lang, "recipes.import.lang."+lang); name != "recipes.import.lang."+lang {
			read += " · " + c.t("recipes.import.sum.lang", "lang", name)
		}
	}
	data["WebRead"] = read
	// the summary rows, each a jump link
	bold := func(s string) string { return "<b>" + html.EscapeString(s) + "</b>" }
	name := "„" + f.Name + "“"
	recipeRow := c.t("recipes.import.sum.recipe", "name", "\x00", "portions", c.portionsText(atoiOr(f.Portions, 1)))
	if m := atoiOr(f.Minutes, 0); m > 0 {
		recipeRow = c.t("recipes.import.sum.recipe.time", "name", "\x00", "portions", c.portionsText(atoiOr(f.Portions, 1)), "time", c.minutesLong(m))
	}
	rows := []template.HTML{template.HTML(`<a href="#rcp">` + strings.Replace(html.EscapeString(recipeRow), "\x00", bold(name), 1) + `</a>`)} // #nosec G203 -- escaped
	switch {
	case imported == 0:
		rows = append(rows, template.HTML(`<a href="#ings">`+bold(c.t("recipes.import.sum.ings.none"))+html.EscapeString(c.t("recipes.import.sum.ings.none.after"))+`</a>`)) // #nosec G203 -- escaped
	case len(unassigned) == 0:
		text := html.EscapeString(c.t("recipes.import.sum.ings.lib", "count", "\x00", "lib", strconv.Itoa(inLib)))
		rows = append(rows, template.HTML(`<a href="#ings">`+strings.Replace(text, "\x00", bold(c.count("recipes.import.sum.ings", imported)), 1)+`</a>`)) // #nosec G203 -- escaped
	default:
		var links []string
		for _, l := range unassigned {
			links = append(links, `<a href="#ing`+strconv.Itoa(l.N)+`"><b>`+html.EscapeString(l.Name)+`</b></a>`)
		}
		text := html.EscapeString(c.t("recipes.import.sum.ings.need", "count", "\x00", "lib", strconv.Itoa(inLib), "m", strconv.Itoa(len(unassigned)), "links", "\x01"))
		text = strings.Replace(text, "\x00", bold(c.count("recipes.import.sum.ings", imported)), 1)
		rows = append(rows, template.HTML(`<span class="need-line">`+strings.Replace(text, "\x01", strings.Join(links, ", "), 1)+`</span>`)) // #nosec G203 -- escaped, the links are built with escaped text
	}
	if n := len(f.Steps); n > 0 {
		nonBlank := 0
		for _, st := range f.Steps {
			if strings.TrimSpace(st.Text) != "" {
				nonBlank++
			}
		}
		rows = append(rows, template.HTML(`<a href="#steps">`+bold(c.count("recipes.import.sum.steps", nonBlank))+`</a>`)) // #nosec G203 -- escaped
	}
	if w.HasPicture {
		rows = append(rows, template.HTML(`<a href="#pic">`+bold(c.t("recipes.import.sum.pic"))+`</a>`)) // #nosec G203 -- escaped
	} else {
		rows = append(rows, plainHTML(c.t("recipes.import.sum.nopic")))
	}
	nutRows, stated := c.pageNutRows(cand.Nutrition)
	if cand.Nutrition != nil {
		rows = append(rows, template.HTML(`<a href="#nut">`+bold(c.t("recipes.import.sum.nut"))+html.EscapeString(c.t("recipes.import.sum.nut.of", "n", strconv.Itoa(stated)))+`</a>`)) // #nosec G203 -- escaped
	} else {
		rows = append(rows, plainHTML(c.t("recipes.import.sum.nonut")))
	}
	data["SumRows"] = rows
	// a recipe from this page that the household already has
	if ref, ok, err := c.s.db.RecipeImportedFrom(c.p.household.ID, w.URL); err == nil && ok {
		link := htmlLink(recipesPath+"/"+ref.ID, "„"+ref.Name+"“")
		key := "recipes.import.again"
		if ref.ByName == "" {
			key = "recipes.import.again.anon"
		}
		text := html.EscapeString(c.t(key, "link", "\x00", "name", ref.ByName, "date", c.p.day(ref.At)))
		data["Again"] = template.HTML(strings.Replace(text, "\x00", link, 1)) // #nosec G203 -- escaped, the link is built with escaped text
	}
	// hints
	if cand.CategoryWord != "" {
		data["CatHint"] = c.t("recipes.import.cat.hint", "word", cand.CategoryWord)
	}
	switch {
	case cand.PortionsGiven && cand.Minutes > 0:
		data["YieldHint"] = c.t("recipes.import.yield.time", "yield", cand.YieldText, "time", c.minutesLong(cand.Minutes))
	case cand.PortionsGiven:
		data["YieldHint"] = c.t("recipes.import.yield.notime", "yield", cand.YieldText)
	case cand.YieldText != "":
		data["YieldHint"], data["YieldWarn"] = c.t("recipes.import.yield.other", "yield", cand.YieldText, "n", f.Portions), true
	default:
		data["YieldHint"], data["YieldWarn"] = c.t("recipes.import.yield.none", "n", f.Portions), true
	}
	data["WarnText"] = c.t("recipes.import.check")
	switch {
	case imported == 0:
		data["IngsHint"] = c.t("recipes.import.ings.hint.none")
	case need == 0 && inLib == imported:
		data["IngsHint"] = c.t("recipes.import.ings.hint.ok", "n", strconv.Itoa(imported))
	case need == 0:
		data["IngsHint"] = c.t("recipes.import.ings.hint.chosen", "lib", strconv.Itoa(inLib), "n", strconv.Itoa(imported))
	default:
		data["IngsHint"] = c.t("recipes.import.ings.hint", "lib", strconv.Itoa(inLib), "n", strconv.Itoa(imported), "m", strconv.Itoa(need))
	}
	data["StepsHint"] = c.t("recipes.import.steps.hint")
	// the picture
	if w.HasPicture {
		data["Pic"] = map[string]any{"Src": recipeImportPath + "/" + w.DraftID + "/picture?which=" + strconv.Itoa(w.Which), "Alt": c.t("recipes.import.pic.alt", "name", f.Name),
			"Caption": c.t("recipes.import.pic.caption", "host", w.Host), "On": w.Picture, "OnText": c.t("recipes.import.pic.on"), "OffText": c.t("recipes.import.pic.off")}
	}
	// the values of the page
	if cand.Nutrition != nil {
		data["WebNut"] = webNutView{Hint: c.t("recipes.import.nut.hint", "host", w.Host), Rows: nutRows}
	}
	// the keywords of the page, none chosen
	var chips []rcpTagChip
	for _, k := range w.Keywords {
		chips = append(chips, rcpTagChip{k, slices.ContainsFunc(f.Tags, func(t string) bool { return strings.EqualFold(t, k) })})
	}
	data["TagChips"] = chips
	chosen := 0
	for _, ch := range chips {
		if ch.Checked {
			chosen++
		}
	}
	tags := c.t("recipes.import.more.tags.none")
	if len(chips) > 0 {
		pick := c.t("recipes.import.more.tags.chosen.none")
		if chosen > 0 {
			pick = c.t("recipes.import.more.tags.chosen.n", "n", strconv.Itoa(chosen))
		}
		tags = c.t("recipes.import.more.tags.some", "n", strconv.Itoa(len(chips)), "chosen", pick)
	}
	data["MoreSum"] = c.t("recipes.import.more.sum", "tags", tags, "host", w.Host)
	data["MoreOpen"] = true
	data["SrcLine"] = map[string]string{"Label": c.t("recipes.import.source.label"), "URL": w.URL, "Hint": c.t("recipes.import.source.hint")}
}

func (c *itemCtx) dirtyText(need int) string {
	switch need {
	case 0:
		return c.t("recipes.import.dirty.none")
	case 1:
		return c.t("recipes.import.dirty.one")
	}
	return c.t("recipes.import.dirty.other", "n", strconv.Itoa(need))
}

func atoiOr(s string, def int) int {
	if n, err := strconv.Atoi(strings.TrimSpace(s)); err == nil && n > 0 {
		return n
	}
	return def
}

// pageNutRows are the seven values the page stated, per portion; a value it did not state is "not stated", never 0.
func (c *itemCtx) pageNutRows(n *recipeimport.Nutrition) (rows []nutRowView, stated int) {
	if n == nil {
		return nil, 0
	}
	for _, row := range nutritionRows {
		rv := nutRowView{Label: c.t("nutrition.row." + nutName(row.Key)), Sub: row.Sub, Big: row.Key == nutrition.KCal, Web: true}
		var v *float64
		switch row.Key {
		case nutrition.KCal:
			v = n.KCal
		case nutrition.Fat:
			v = n.Fat
		case nutrition.Saturates:
			v = n.Saturates
		case nutrition.Carbs:
			v = n.Carbs
		case nutrition.Sugars:
			v = n.Sugars
		case nutrition.Protein:
			v = n.Protein
		case nutrition.Salt:
			v = n.Salt
		}
		if v == nil {
			rv.NA = true
		} else {
			stated++
			rv.Value = nutrition.Format(row.Key, *v, c.lang)
			if row.Key == nutrition.KCal && n.KJ != nil {
				rv.KJ = nutrition.Format(nutrition.KJ, *n.KJ, c.lang)
			}
		}
		rows = append(rows, rv)
	}
	return rows, stated
}

// handleImportPicture shows the picture of a draft in its review.
func (s *server) handleImportPicture(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	st, found, expired, err := s.loadDraft(p, r.PathValue("id"))
	if err != nil || !found || expired {
		http.NotFound(w, r)
		return
	}
	n, _ := strconv.Atoi(r.URL.Query().Get("which"))
	b := st.data.Pictures[n]
	if len(b) == 0 {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Content-Length", strconv.Itoa(len(b)))
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(b)
}

// handleRecipeAction routes the pages below one recipe: cooking, changing, its photos and deleting.
func (s *server) handleRecipeAction(w http.ResponseWriter, r *http.Request) {
	action := r.PathValue("action")
	if rest := r.PathValue("rest"); rest != "" || strings.HasSuffix(r.URL.Path, "/") {
		// "photos/{pid}" and "photos/{pid}/cover|delete" (ticket 280)
		parts := strings.Split(rest, "/")
		if action != "photos" || len(parts) > 2 || parts[0] == "" {
			http.NotFound(w, r)
			return
		}
		r.SetPathValue("pid", parts[0])
		sub := ""
		if len(parts) == 2 {
			sub = parts[1]
		}
		switch r.Method + " " + sub {
		case "GET ":
			s.handleRecipePhotoFile(w, r)
		case "POST cover":
			s.handleRecipePhotoCover(w, r)
		case "POST delete":
			s.handleRecipePhotoDelete(w, r)
		default:
			http.NotFound(w, r)
		}
		return
	}
	switch r.Method + " " + action {
	case "GET cook":
		s.handleRecipeCook(w, r)
	case "GET edit":
		s.handleRecipeEdit(w, r)
	case "GET photos":
		s.handleRecipePhotos(w, r)
	case "POST photos":
		s.handleRecipePhotosUpload(w, r)
	case "POST delete":
		s.handleRecipeDelete(w, r)
	case "POST favourites":
		s.handleRecipeFavourites(w, r)
	default:
		http.NotFound(w, r)
	}
}

// addWebRecipePhoto keeps the picture of an imported page as the first photo of the recipe (origin web, so the
// recipe page says where it came from; ticket 280 holds the photos). A picture that cannot be kept is left out:
// the recipe is saved either way.
func (s *server) addWebRecipePhoto(ctx context.Context, p principal, recipeID string, jpeg []byte) {
	if len(jpeg) == 0 || s.recipePhotos == nil {
		return
	}
	set, err := recipephoto.Prepare(ctx, bytes.NewReader(jpeg))
	if err != nil {
		s.logger.Warn("imported recipe picture", "err", err)
		return
	}
	ph, err := s.db.AddRecipePhoto(p.household.ID, recipeID, store.NewRecipePhoto{FileName: "", Origin: store.PhotoFromWeb, Width: set.Width, Height: set.Height})
	if err != nil {
		s.logger.Warn("imported recipe picture", "err", err)
		return
	}
	if err := s.recipePhotos.Put(ph.ID, set); err != nil {
		_, _, _ = s.db.DeleteRecipePhoto(p.household.ID, recipeID, ph.ID)
		s.logger.Warn("imported recipe picture", "err", err)
	}
}
