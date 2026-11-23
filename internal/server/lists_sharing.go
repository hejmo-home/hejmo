package server

import (
	"errors"
	"html"
	"html/template"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/hejmo-home/hejmo/internal/i18n"
	"github.com/hejmo-home/hejmo/internal/list"
	"github.com/hejmo-home/hejmo/internal/store"
)

// Sharing lists with other apps (ticket 167, preview-167): Settings, Lists,
// Share with other apps, and the page that sets up an iPhone or iPad. The
// CalDAV side is in caldav_lists.go.

const (
	sharingPath      = "/settings/lists/sharing"
	sharingSetupPath = "/settings/lists/sharing/setup"
)

// shareRow is one list of the form "Welche Listen?".
type shareRow struct {
	ID, Name, Symbol string
	Slot             int
	On               bool
	OnText, OffText  string
	Aria             string
	Seen             string // what the form showed, to name a list deleted meanwhile
	Error            string
}

// sampleLine is one reminder of the example.
type sampleLine struct {
	Title, Note string
	Done        bool
}

// sharingForm is what the page shows: the stored state, or what was posted
// when saving was refused.
type sharingForm struct {
	Rows  []shareRow
	Error string
}

func (s *server) quotedName(lang, name string) string {
	return s.i18n.Format(lang, i18n.Msg("share.quoted", "name", name))
}

// linkLine is a sentence that ends in a link: text, the link and what follows
// (a full stop, or more text). Everything is escaped.
func (s *server) linkLine(lang, preKey, href, linkText, after string) template.HTML {
	out := html.EscapeString(s.i18n.T(lang, preKey)) + ` <a href="` + html.EscapeString(href) + `">` + html.EscapeString(linkText) + `</a>`
	if after != "" && !strings.HasPrefix(after, ".") {
		out += " "
	}
	return template.HTML(out + html.EscapeString(after)) // #nosec G203 -- escaped above
}

func (s *server) openWords(lang string, n int) string {
	switch n {
	case 0:
		return s.i18n.T(lang, "share.open.none")
	case 1:
		return s.i18n.T(lang, "share.open.one")
	}
	return s.i18n.Format(lang, i18n.Msg("share.open.many", "n", strconv.Itoa(n)))
}

func (s *server) shareRowOf(lang string, l list.List, on bool, open int) shareRow {
	return shareRow{
		ID: l.ID, Name: l.Name, Symbol: l.Symbol, Slot: list.Slot(l.Color), On: on,
		OnText:  s.i18n.Format(lang, i18n.Msg("share.state.on", "name", l.Name, "open", s.openWords(lang, open))),
		OffText: s.i18n.T(lang, "share.state.off"),
		Aria:    s.i18n.Format(lang, i18n.Msg("share.aria", "name", l.Name)),
		Seen:    url.Values{"id": {l.ID}, "name": {l.Name}, "color": {l.Color}, "symbol": {l.Symbol}}.Encode(),
	}
}

// sharingRowLines are the two lines of the row on Settings, Lists: which lists
// are in Apple Reminders and which only in Hejmo, every one by name.
func (s *server) sharingRowLines(lang string, lists []list.List) []string {
	if len(lists) == 0 {
		return []string{s.i18n.T(lang, "share.row.no_lists")}
	}
	var on, off []string
	for _, l := range lists {
		if l.Shared {
			on = append(on, l.Name)
		} else {
			off = append(off, l.Name)
		}
	}
	line := func(key, noneKey string, names []string) string {
		if len(names) == 0 {
			return s.i18n.T(lang, noneKey)
		}
		return s.i18n.Format(lang, i18n.Msg(key, "names", s.joinWords(lang, names)))
	}
	return []string{line("share.row.on", "share.row.on_none", on), line("share.row.off", "share.row.off_none", off)}
}

// sampleOf shows how a list looks in Reminders: its first entries, quantity
// after a dot, the note below, ticked-off ones done.
func (s *server) sampleOf(p principal, lang string, l list.List, es []list.Entry) (lines []sampleLine, more int) {
	c := &itemCtx{s: s, p: p, lang: lang, active: s.activeUnits(p.household)}
	var open, done []list.Entry
	for _, e := range es {
		if e.Done {
			done = append(done, e)
		} else {
			open = append(open, e)
		}
	}
	const showOpen = 5
	shown := open[:min(len(open), showOpen)]
	more = len(open) - len(shown)
	if len(done) > 0 {
		shown = append(slices.Clone(shown), done[0])
	}
	for _, e := range shown {
		lines = append(lines, sampleLine{Title: c.itemTitle(e), Note: e.Note, Done: e.Done})
	}
	return lines, more
}

// deviceRows are the devices that fetch lists: the household's device tokens,
// with the last fetch to the second. A token from before 0.2 has no lists
// scope: it is flagged in words, with what to do.
func (s *server) deviceRows(r *http.Request, p principal, lang string) ([]rowView, error) {
	tokens, err := s.deviceTokens(p.household.ID)
	if err != nil {
		return nil, err
	}
	var rows []rowView
	for _, t := range tokens {
		row := rowView{Href: "/caldav/tokens/" + t.ID, Title: t.Name}
		last := ""
		if !t.LastUsed.IsZero() {
			last = p.when(t.LastUsed)
		}
		if scopedTo(t.Scopes, "lists") {
			text := s.i18n.T(lang, "share.dev.fetches")
			if last != "" {
				text = s.i18n.Format(lang, i18n.Msg("share.dev.fetches_last", "when", last))
			} else {
				row.Flag = &flagView{Kind: "warn", Text: s.i18n.T(lang, "caldav.token.flag.never")}
			}
			row.Lines = []rowLine{{Text: text}}
		} else {
			key, kv := "share.dev.old", []string{"name", t.PersonName, "day", p.day(t.Created)}
			if last != "" {
				key, kv = "share.dev.old_last", append(kv, "when", last)
			}
			row.Lines = []rowLine{{Text: s.i18n.Format(lang, i18n.Msg(key, kv...))}}
			row.Flag = &flagView{Kind: "warn", Icon: "triangle", Text: s.i18n.T(lang, "share.dev.flag_old")}
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func (s *server) renderSharing(w http.ResponseWriter, r *http.Request, p principal, status int, form *sharingForm) {
	lang := s.langOf(r)
	lists, err := s.db.Lists(p.household.ID)
	if err != nil {
		s.fail(w, "lists", err)
		return
	}
	counts, err := s.db.EntryCounts(p.household.ID)
	if err != nil {
		s.fail(w, "entry counts", err)
		return
	}
	if form == nil {
		form = &sharingForm{}
		for _, l := range lists {
			form.Rows = append(form.Rows, s.shareRowOf(lang, l, l.Shared, counts[l.ID].Open))
		}
	}
	data := map[string]any{"AndroidHint": s.linkLine(lang, "share.android.pre", "/caldav", s.i18n.T(lang, "caldav.title"), "."), "Person": p.person, "Rows": form.Rows, "HasLists": len(lists) > 0, "SyncStale": s.syncStale(p.household.ID)}
	if len(lists) > 0 {
		first := lists[0]
		es, err := s.db.Entries(p.household.ID, first.ID)
		if err != nil {
			s.fail(w, "entries", err)
			return
		}
		sample, more := s.sampleOf(p, lang, first, es)
		data["SampleTitle"] = s.i18n.Format(lang, i18n.Msg(map[bool]string{true: "share.sample.title_on", false: "share.sample.title_off"}[first.Shared], "name", first.Name))
		data["Sample"], data["SampleMore"] = sample, ""
		if more > 0 {
			data["SampleMore"] = s.i18n.Format(lang, i18n.Msg("share.sample.more", "n", strconv.Itoa(counts[first.ID].Open)))
		}
	}
	devs, err := s.deviceRows(r, p, lang)
	if err != nil {
		s.fail(w, "devices", err)
		return
	}
	data["Devices"] = devs
	s.render(w, r, status, "list_sharing.html", data)
}

func (s *server) handleSharing(w http.ResponseWriter, r *http.Request) {
	if p, ok := s.parentPage(w, r); ok {
		s.renderSharing(w, r, p, http.StatusOK, nil)
	}
}

// handleSharingSave stores one switch per list: the lists the form showed
// ("seen") are on when ticked and off otherwise. A list deleted meanwhile
// stores nothing and shows the error at its row.
func (s *server) handleSharingSave(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	_ = r.ParseForm()
	lang := s.langOf(r)
	on := map[string]bool{}
	for _, id := range r.PostForm["share"] {
		on[id] = true
	}
	switched := map[string]bool{}
	var order []url.Values
	for _, raw := range r.PostForm["seen"] {
		q, err := url.ParseQuery(raw)
		if err != nil || q.Get("id") == "" {
			continue
		}
		switched[q.Get("id")] = on[q.Get("id")]
		order = append(order, q)
	}
	err := s.db.SetListsShared(p.household.ID, switched)
	var missing *store.MissingListError
	switch {
	case err == nil:
		s.redirectSharingSaved(w, r, p, lang)
	case errors.As(err, &missing):
		counts, _ := s.db.EntryCounts(p.household.ID)
		form := &sharingForm{}
		for _, q := range order {
			l := list.List{ID: q.Get("id"), Name: q.Get("name"), Color: q.Get("color"), Symbol: q.Get("symbol")}
			row := s.shareRowOf(lang, l, on[l.ID], counts[l.ID].Open)
			if l.ID == missing.ID {
				row.Error = s.i18n.T(lang, "share.error.gone")
			}
			form.Rows = append(form.Rows, row)
		}
		s.renderSharing(w, r, p, http.StatusUnprocessableEntity, form)
	default:
		s.fail(w, "share lists", err)
	}
}

// redirectSharingSaved says in the success line which lists are in Reminders now.
func (s *server) redirectSharingSaved(w http.ResponseWriter, r *http.Request, p principal, lang string) {
	lists, err := s.db.Lists(p.household.ID)
	if err != nil {
		s.fail(w, "lists", err)
		return
	}
	var names []string
	for _, l := range lists {
		if l.Shared {
			names = append(names, s.quotedName(lang, l.Name))
		}
	}
	switch len(names) {
	case 0:
		s.redirectSaved(w, r, sharingPath, "flash.lists_shared.none")
	case 1:
		s.redirectSavedFields(w, r, sharingPath, "flash.lists_shared.one", names[0])
	default:
		s.redirectSavedFields(w, r, sharingPath, "flash.lists_shared", s.joinWords(lang, names))
	}
}

// handleSharingSetup shows the page that sets up an iPhone or iPad.
func (s *server) handleSharingSetup(w http.ResponseWriter, r *http.Request) {
	if p, ok := s.parentPage(w, r); ok {
		s.renderSharingSetup(w, r, p, http.StatusOK, nil)
	}
}

// renderSharingSetup shows the steps; form carries a refused profile name and
// its error (ErrorField "profile_name"), or the HTTP refusal.
func (s *server) renderSharingSetup(w http.ResponseWriter, r *http.Request, p principal, status int, form map[string]any) {
	lang := s.langOf(r)
	lists, err := s.db.Lists(p.household.ID)
	if err != nil {
		s.fail(w, "lists", err)
		return
	}
	var names []string
	for _, l := range lists {
		if l.Shared {
			names = append(names, s.quotedName(lang, l.Name))
		}
	}
	data := map[string]any{
		"Person": p.person, "Form": form, "SyncStale": s.syncStale(p.household.ID),
		"NoneShared":  len(names) == 0,
		"NoneLine":    s.linkLine(lang, "iphone.s4.none", sharingPath, s.i18n.T(lang, "share.title"), s.i18n.T(lang, "iphone.s4.none_after")),
		"OnceLine":    s.linkLine(lang, "iphone.once.pre", "/caldav", s.i18n.T(lang, "iphone.once.link"), "."),
		"HTTPSLine":   s.linkLine(lang, "iphone.s2.https", s.httpsOrigin(r)+sharingSetupPath, s.httpsOrigin(r), "."),
		"Lead":        s.i18n.Format(lang, i18n.Msg("iphone.lead", "name", p.person.Name)),
		"Placeholder": s.i18n.Format(lang, i18n.Msg("iphone.s2.placeholder", "name", p.person.Name)),
		"NameError":   s.i18n.Format(lang, i18n.Msg("iphone.s2.error", "name", p.person.Name)),
		"ListsLine":   s.i18n.Format(lang, i18n.Msg("iphone.s4.lists", "names", s.joinWords(lang, names))),
		"CAFirst":     s.pki.Ready() && !s.publicInUse(),
		// Without a public name the box is reachable at home only.
		"HomeOnly": !s.publicInUse(),
		"HTTPS":    r.TLS != nil,
	}
	if form != nil {
		data["ErrorField"], data["Error"] = form["ErrorField"], form["Error"]
	}
	s.render(w, r, status, "list_sharing_setup.html", data)
}
