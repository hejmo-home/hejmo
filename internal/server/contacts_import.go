package server

import (
	"bytes"
	"context"
	"errors"
	"html"
	"html/template"
	"io"
	"mime/multipart"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/hejmo-home/hejmo/internal/contact"
	"github.com/hejmo-home/hejmo/internal/contactimport"
	"github.com/hejmo-home/hejmo/internal/i18n"
	"github.com/hejmo-home/hejmo/internal/picture"
	"github.com/hejmo-home/hejmo/internal/store"
	"github.com/hejmo-home/hejmo/internal/vcard"
)

// Contacts from a vCard file (ticket 300, preview-300.html): two steps and nothing saved before the parent
// confirms. The uploaded file waits in the store for an hour, for the parent who uploaded it only, and is deleted
// after the import. The review page is worked out from the file and from what Hejmo holds at that moment.

const (
	importPath   = contactsPath + "/import"
	importMax    = 25 << 20 // the biggest contact file
	importMB     = 25
	importTTL    = time.Hour
	freshCookie  = "hejmo_fresh"
	freshTextTTL = 2 * time.Minute
)

// homeRegion is the country of the household's home, the region phone numbers are read with (ADR 0026).
func homeRegion(h store.Household) string {
	country, _, _ := strings.Cut(h.Region, "-")
	if country == "" {
		return contact.DefaultRegion
	}
	return country
}

// importSweep deletes the files whose hour is over.
func (s *server) importSweep() {
	if err := s.db.ExpireContactImports(s.now().Add(-importTTL), s.now()); err != nil {
		s.logger.Error("expire contact imports", "err", err)
	}
}

// importState is an uploaded file read again: the cards (with their photos as the picture rules made them), and
// what they bring to the contacts Hejmo has now.
type importState struct {
	imp   store.ContactImport
	cards []vcard.Card
	plan  contactimport.Plan
}

// loadImport finds an uploaded file for this parent and reads it. found is false (and the answer is written)
// for a file that does not exist or belongs to another parent; expired is true for one whose hour is over (the
// file is then gone).
func (s *server) loadImport(p principal, id string) (st importState, found, expired bool, err error) {
	s.importSweep()
	imp, err := s.db.ContactImport(p.household.ID, id)
	if errors.Is(err, store.ErrNotFound) || (err == nil && imp.UploadedBy != p.person.ID) {
		return importState{}, false, false, nil
	} else if err != nil {
		return importState{}, false, false, err
	}
	if imp.Expired || !s.now().Before(imp.UploadedAt.Add(importTTL)) {
		return importState{imp: imp}, true, true, nil
	}
	st, err = s.readImport(p, imp)
	return st, true, false, err
}

func (s *server) readImport(p principal, imp store.ContactImport) (importState, error) {
	cards, err := vcard.Parse(imp.Data)
	if err != nil {
		return importState{}, err
	}
	photos, err := s.db.ContactImportPhotos(imp.ID)
	if err != nil {
		return importState{}, err
	}
	for i := range cards {
		cards[i].Photo = photos[cards[i].Number]
	}
	existing, err := s.db.Contacts(p.household.ID)
	if err != nil {
		return importState{}, err
	}
	return importState{imp: imp, cards: cards, plan: contactimport.Build(cards, existing, homeRegion(p.household))}, nil
}

// ---- step 1: the upload

func (s *server) importFormData(p principal, extra map[string]any) map[string]any {
	d := map[string]any{"Person": p.person, "SyncStale": s.syncStale(p.household.ID), "Max": importMB, "Accept": ".vcf,.vcard,text/vcard,text/x-vcard"}
	for k, v := range extra {
		d[k] = v
	}
	return d
}

func (s *server) handleContactImportForm(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	s.importSweep()
	s.render(w, r, http.StatusOK, "contact_import.html", s.importFormData(p, nil))
}

// importFailure is a file the box did not take: the sentence for the file field.
type importFailure struct {
	status int
	text   string
}

// readUpload reads the uploaded file of a request: its name, its content or the reason it was refused. Texts are
// in the language of the request.
func (s *server) readUpload(w http.ResponseWriter, r *http.Request, lang string) (name string, data []byte, fail *importFailure) {
	t := func(key string, kv ...string) string { return s.i18n.Format(lang, i18n.Msg(key, kv...)) }
	r.Body = http.MaxBytesReader(w, r.Body, importMax+(2<<20))
	if err := r.ParseMultipartForm(4 << 20); err != nil {
		var big *http.MaxBytesError
		if errors.As(err, &big) {
			return "", nil, &importFailure{http.StatusRequestEntityTooLarge, t("cimp.err.big", "file", "…", "size", ">"+strconv.Itoa(importMB)+" MB", "max", strconv.Itoa(importMB))}
		}
		return "", nil, &importFailure{http.StatusUnprocessableEntity, t("cimp.err.none_chosen")}
	}
	defer r.MultipartForm.RemoveAll()
	var h *multipart.FileHeader
	if fs := r.MultipartForm.File["file"]; len(fs) > 0 {
		h = fs[0]
	}
	if h == nil || h.Size == 0 && h.Filename == "" {
		return "", nil, &importFailure{http.StatusUnprocessableEntity, t("cimp.err.none_chosen")}
	}
	name = cleanFileName(h.Filename)
	if name == "" {
		name = "contacts.vcf"
	}
	if h.Size > importMax {
		return name, nil, &importFailure{http.StatusRequestEntityTooLarge, t("cimp.err.big", "file", name, "size", humanBytes(lang, h.Size), "max", strconv.Itoa(importMB))}
	}
	f, err := h.Open()
	if err != nil {
		return name, nil, &importFailure{http.StatusUnprocessableEntity, t("cimp.err.read")}
	}
	defer f.Close()
	data, err = io.ReadAll(io.LimitReader(f, importMax+1))
	if err != nil || len(data) > importMax {
		return name, nil, &importFailure{http.StatusRequestEntityTooLarge, t("cimp.err.big", "file", name, "size", humanBytes(lang, int64(len(data))), "max", strconv.Itoa(importMB))}
	}
	return name, data, nil
}

// checkUpload reads the cards of a file and makes the pictures of their photos. A file that holds no card, or
// none that can be read, comes back as a failure with the sentence for the field.
func (s *server) checkUpload(ctx context.Context, lang, name string, data []byte, readable func([]vcard.Card) bool) ([]vcard.Card, map[int][]byte, *importFailure) {
	t := func(key string, kv ...string) string { return s.i18n.Format(lang, i18n.Msg(key, kv...)) }
	cards, err := vcard.Parse(data)
	switch {
	case errors.Is(err, vcard.ErrEmpty):
		return nil, nil, &importFailure{http.StatusUnprocessableEntity, t("cimp.err.empty", "file", name)}
	case err != nil:
		return nil, nil, &importFailure{http.StatusUnprocessableEntity, t("cimp.err.notvcard", "file", name)}
	}
	if !readable(cards) {
		return nil, nil, &importFailure{http.StatusUnprocessableEntity, t("cimp.err.unreadable", "file", name)}
	}
	photos := map[int][]byte{}
	for _, c := range cards {
		if c.Problem != "" || len(c.Photo) == 0 {
			continue
		}
		if res, err := picture.ProcessSquare(ctx, bytes.NewReader(c.Photo), picture.ContactMaxSide); err == nil {
			photos[c.Number] = res.JPEG
		}
	}
	return cards, photos, nil
}

func anyReadable(cards []vcard.Card) bool {
	return slices.ContainsFunc(cards, func(c vcard.Card) bool { return c.Problem == "" })
}

func (s *server) handleContactImportUpload(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	lang := s.langOf(r)
	s.importSweep()
	fail := func(name string, f *importFailure) {
		data := s.importFormData(p, map[string]any{"ErrorField": "file", "ErrorText": f.text, "FormError": true})
		s.render(w, r, f.status, "contact_import.html", data)
	}
	name, data, f := s.readUpload(w, r, lang)
	if f != nil {
		fail(name, f)
		return
	}
	cards, photos, f := s.checkUpload(r.Context(), lang, name, data, anyReadable)
	if f != nil {
		fail(name, f)
		return
	}
	for i := range cards {
		cards[i].Photo = photos[cards[i].Number]
	}
	existing, err := s.db.Contacts(p.household.ID)
	if err != nil {
		s.fail(w, "contacts", err)
		return
	}
	plan := contactimport.Build(cards, existing, homeRegion(p.household))
	if len(plan.Dups) == 0 && len(plan.News) == 0 { // nothing to take over: the file is not kept
		e, ok := s.contactEnv(w, r, p)
		if !ok {
			return
		}
		s.render(w, r, http.StatusOK, "contact_import_review.html", e.nothingNewView(p, name, s.now(), plan))
		return
	}
	imp, err := s.db.AddContactImport(p.household.ID, p.person.ID, name, data, photos, s.now())
	if err != nil {
		s.fail(w, "add contact import", err)
		return
	}
	http.Redirect(w, r, importPath+"/"+imp.ID, http.StatusSeeOther)
}

// ---- step 2: the review

func (s *server) importDecisions(r *http.Request, plan contactimport.Plan) contactimport.Decisions {
	dec := contactimport.Decisions{Dup: map[string]string{}, Photo: map[string]string{}, Skip: map[string]bool{}, Groups: r.PostForm["group"]}
	for _, d := range plan.Dups {
		n := d.Key[1:]
		if v, ok := r.PostForm["d-"+n]; ok && len(v) > 0 {
			dec.Dup[d.Key] = v[0]
		} else {
			dec.Dup[d.Key] = "" // an empty choice is a missing one: the page was sent, so the preselection did not stand
		}
		if v := r.PostForm["ph-"+n]; len(v) > 0 {
			dec.Photo[d.Key] = v[0]
		}
	}
	taken := map[string]bool{}
	for _, v := range r.PostForm["take"] {
		taken[v] = true
	}
	for _, n := range plan.News {
		if !taken[n.Key[1:]] {
			dec.Skip[n.Key] = true
		}
	}
	return dec
}

// defaultDecisions is what the page shows when it is opened: the preselections, everything ticked, no group.
func defaultDecisions(plan contactimport.Plan) contactimport.Decisions {
	dec := contactimport.Decisions{Dup: map[string]string{}, Photo: map[string]string{}, Skip: map[string]bool{}}
	for _, d := range plan.Dups {
		dec.Dup[d.Key] = d.Preselect
	}
	return dec
}

func (s *server) handleContactImportReview(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	e, ok := s.contactEnv(w, r, p)
	if !ok {
		return
	}
	st, found, expired, err := s.loadImport(p, r.PathValue("id"))
	switch {
	case err != nil:
		s.fail(w, "contact import", err)
	case !found:
		http.NotFound(w, r)
	case expired:
		s.render(w, r, http.StatusOK, "contact_import_review.html", e.expiredView(p, st.imp.FileName))
	default:
		s.render(w, r, http.StatusOK, "contact_import_review.html", e.reviewView(p, st, defaultDecisions(st.plan), false))
	}
}

func (s *server) handleContactImportConfirm(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	e, ok := s.contactEnv(w, r, p)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxForm)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "the form could not be read", http.StatusBadRequest)
		return
	}
	st, found, expired, err := s.loadImport(p, r.PathValue("id"))
	switch {
	case err != nil:
		s.fail(w, "contact import", err)
		return
	case !found:
		http.NotFound(w, r)
		return
	case expired:
		s.render(w, r, http.StatusOK, "contact_import_review.html", e.expiredView(p, st.imp.FileName))
		return
	}
	if r.PostForm.Get("cancel") != "" {
		if err := s.db.DeleteContactImport(p.household.ID, st.imp.ID); err != nil && !errors.Is(err, store.ErrNotFound) {
			s.fail(w, "delete contact import", err)
			return
		}
		s.redirectSaved(w, r, contactsPath, "flash.contacts_import_cancelled")
		return
	}
	dec := s.importDecisions(r, st.plan)
	for _, g := range dec.Groups {
		if _, err := s.db.ContactGroup(p.household.ID, g); err != nil { // as REST: a group that is gone is no server error
			if !errors.Is(err, store.ErrNotFound) {
				s.fail(w, "contact group", err)
				return
			}
			view := e.reviewView(p, st, dec, false)
			view["Error"] = e.t("cimp.err.group")
			s.render(w, r, http.StatusUnprocessableEntity, "contact_import_review.html", view)
			return
		}
	}
	if missing := st.plan.Missing(dec); len(missing) > 0 {
		view := e.reviewView(p, st, dec, true)
		s.render(w, r, http.StatusUnprocessableEntity, "contact_import_review.html", view)
		return
	}
	res, ids, err := s.applyImport(p, st, dec, e.t("birthdays.calendar_name"))
	if err != nil {
		var taken *store.NameTakenError
		if errors.As(err, &taken) { // a contact was made with that name while the page was open
			view := e.reviewView(p, st, dec, false)
			view["Error"] = e.t("cimp.err.read")
			s.render(w, r, http.StatusConflict, "contact_import_review.html", view)
			return
		}
		s.fail(w, "apply contact import", err)
		return
	}
	s.setFresh(w, r, res, ids)
	summary := e.importSummary(res)
	s.redirectSavedFields(w, r, contactsPath, "flash.contacts_imported", st.imp.FileName, summary)
}

// applyImport writes the contacts the decisions ask for and deletes the file.
func (s *server) applyImport(p principal, st importState, dec contactimport.Decisions, calendarName string) (contactimport.Result, []string, error) {
	ops, res := st.plan.Ops(dec, st.imp.FileName)
	for i := range ops {
		ops[i].Input.CalendarName = calendarName
		ops[i].Input.Today = s.householdNow(p.household)
	}
	ids, err := s.db.ApplyContactImport(p.household.ID, st.imp.ID, p.person.ID, ops)
	return res, ids, err
}

// importSummary is the sentence of the success line after an import: the new contacts and the completed ones, by name.
func (e ctEnv) importSummary(res contactimport.Result) string {
	var parts []string
	switch len(res.New) {
	case 0:
		parts = append(parts, e.t("cimp.res.new.zero"))
	case 1:
		parts = append(parts, e.t("cimp.res.new.one"))
	default:
		parts = append(parts, e.t("cimp.res.new.other", "n", strconv.Itoa(len(res.New))))
	}
	if len(res.Completed) > 0 {
		parts = append(parts, e.t("cimp.res.completed", "names", contact.JoinNames(res.Completed, e.t("contact.rel.and"))))
	}
	return strings.Join(parts, " ")
}

// setFresh remembers which contacts the import just made and completed, for the next page load of the overview.
func (s *server) setFresh(w http.ResponseWriter, r *http.Request, res contactimport.Result, ids []string) {
	var b strings.Builder
	for i, e := range res.Entries {
		if i >= len(ids) {
			break
		}
		if i > 0 {
			b.WriteByte(',')
		}
		if e.Completed {
			b.WriteString("c:")
		} else {
			b.WriteString("n:")
		}
		b.WriteString(ids[i])
	}
	http.SetCookie(w, &http.Cookie{Name: freshCookie, Value: s.flashTexts.put(b.String(), s.now()), Path: "/", MaxAge: int(freshTextTTL / time.Second),
		HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: r.TLS != nil})
}

// takeFresh returns the contacts marked "just taken over" (value "new") and "just completed" (value "completed")
// for this browser, once.
func (s *server) takeFresh(w http.ResponseWriter, r *http.Request) map[string]string {
	c, err := r.Cookie(freshCookie)
	if err != nil {
		return nil
	}
	http.SetCookie(w, &http.Cookie{Name: freshCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: r.TLS != nil})
	text, ok := s.flashTexts.take(c.Value, s.now())
	if !ok {
		return nil
	}
	out := map[string]string{}
	for _, part := range strings.Split(text, ",") {
		kind, id, _ := strings.Cut(part, ":")
		switch kind {
		case "n":
			out[id] = "new"
		case "c":
			out[id] = "completed"
		}
	}
	return out
}

func (s *server) handleContactImportPhoto(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	imp, err := s.db.ContactImport(p.household.ID, r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) || (err == nil && (imp.UploadedBy != p.person.ID || imp.Expired || !s.now().Before(imp.UploadedAt.Add(importTTL)))) {
		http.NotFound(w, r)
		return
	} else if err != nil {
		s.fail(w, "contact import", err)
		return
	}
	n, err := strconv.Atoi(r.PathValue("n"))
	photos, perr := s.db.ContactImportPhotos(imp.ID)
	if err != nil || perr != nil || photos[n] == nil {
		http.NotFound(w, r)
		return
	}
	serveContactPhoto(w, r, store.ContactPhoto{JPEG: photos[n], At: imp.UploadedAt})
}

// ---- what the pages show

// impSide is a card of the file, or a contact in Hejmo, as the review shows it: photo, name, relations, every
// number and every e-mail address.
type impSide struct {
	Photo        ctPhotoView
	Name         string
	Person       bool
	Slot         int
	Shape        string
	Rels         []string
	Full         string
	Nums, Mails  []ctNum
	PhotoLink    bool
	More         string // title, birthday and address of a new contact
	NameNote     string
	NameNoteKind string
}

func (e ctEnv) fileSide(imp store.ContactImport, c vcard.Card, name string) impSide {
	v := ctPhotoView{Initials: contact.Initials(name)}
	if len(c.Photo) > 0 {
		v.URL = importPath + "/" + imp.ID + "/photo/" + strconv.Itoa(c.Number) + "?v=1"
	}
	s := impSide{Photo: v, Name: name, PhotoLink: c.PhotoLink && len(c.Photo) == 0}
	if c.Full != "" && !strings.EqualFold(c.Full, name) {
		s.Full = c.Full
	} else if c.Full == "" && c.Org != "" && !strings.EqualFold(c.Org, name) {
		s.Full = c.Org
	}
	for _, l := range c.Numbers {
		s.Nums = append(s.Nums, ctNum{e.kindWord(l, false), l.Value})
	}
	for _, l := range c.Emails {
		s.Mails = append(s.Mails, ctNum{e.factLabel(l, true), l.Value})
	}
	return s
}

func (e ctEnv) hejmoSide(c store.Contact) impSide {
	row := e.row(c, false)
	s := impSide{Photo: row.Photo, Name: c.HejmoName, Person: row.Person, Slot: row.Slot, Shape: row.Shape, Rels: row.Rels, Full: row.Full, Nums: row.Nums, PhotoLink: false}
	for _, l := range c.Emails {
		s.Mails = append(s.Mails, ctNum{e.factLabel(l, true), l.Value})
	}
	return s
}

// moreLine is the line under a new contact: its title or organisation, its birthday and its address.
func (e ctEnv) moreLine(c vcard.Card) string {
	var parts []string
	switch {
	case c.Title != "":
		parts = append(parts, c.Title)
	case c.Note != "" && !strings.Contains(c.Note, "\n") && contact.Runes(c.Note) <= 40:
		parts = append(parts, c.Note)
	}
	if c.BdayDay != 0 {
		parts = append(parts, e.t("cimp.row.birthday", "date", e.bdayText(c.BdayDay, c.BdayMonth, c.BdayYear)))
	}
	var adr []string
	if c.Street != "" {
		adr = append(adr, c.Street)
	}
	if l := strings.TrimSpace(c.Postcode + " " + c.City); l != "" {
		adr = append(adr, l)
	}
	if len(adr) > 0 {
		parts = append(parts, strings.Join(adr, ", "))
	}
	return strings.Join(parts, " · ")
}

func (e ctEnv) bdayText(day, month, year int) string {
	return e.birthday(store.Contact{BdayDay: day, BdayMonth: month, BdayYear: year}, false)
}

// impBlock is one card or contact of a possible duplicate with the line above it and the reason below it.
type impBlock struct {
	K   string
	Who impSide
	Why template.HTML
}

type impChoice struct {
	Value, Label, Small string
	Checked, Invalid    bool
}

type impPhotoChoose struct {
	Field, Legend string
	Current, File ctPhotoView
	KeepChecked   bool
}

type impMergeOut struct {
	Head      string
	Add, Keep []string
	Photo     *impPhotoChoose
	NoPhoto   string
	HasAdd    bool
}

type impDupView struct {
	ID, Field, Legend string
	Blocks            []impBlock
	Need              string
	Invalid           bool
	Err               string
	Choices           []impChoice
	Merge             impMergeOut
	InFile            bool
	Cards             int
}

type impRow struct {
	Key, Value string
	Who        impSide
	Taken      bool
}

type impGroup struct {
	ID, Name string
	Checked  bool
}

type impLink struct{ Text, Anchor, Sep string }

type impUnread struct{ Title, Text string }

// reviewView builds the data of the review page. dec is what the page shows as chosen; showMissing marks the
// duplicates without a choice as errors.
func (e ctEnv) reviewView(p principal, st importState, dec contactimport.Decisions, showMissing bool) map[string]any {
	plan := st.plan
	one := plan.Cards == 1
	data := map[string]any{"Person": p.person, "SyncStale": e.s.syncStale(p.household.ID), "ID": st.imp.ID, "File": st.imp.FileName, "Review": true}
	data["Title"] = e.t("cimp.review.title")
	data["Lead"] = e.t("cimp.review.lead")
	if one {
		data["Title"], data["Lead"] = e.t("cimp.review.title.one"), e.t("cimp.review.lead.one")
	}
	if len(plan.Dups)+len(plan.News) == 0 {
		return e.nothingNewView(p, st.imp.FileName, st.imp.UploadedAt, plan)
	}
	cards := e.t("cimp.cards.other", "n", strconv.Itoa(plan.Cards))
	if one {
		cards = e.t("cimp.cards.one")
	}
	data["Meta"] = e.t("cimp.sum.file", "name", st.imp.UploadedByName, "time", p.clockSeconds(st.imp.UploadedAt), "cards", cards)
	data["Lines"] = e.summaryLines(plan)
	var dups []impDupView
	var missing []impLink
	for _, d := range plan.Dups {
		v := e.dupView(st.imp, d, dec, showMissing)
		if v.Invalid {
			missing = append(missing, impLink{Text: v.Blocks[0].Who.Name, Anchor: v.ID})
		}
		dups = append(dups, v)
	}
	data["Dups"] = dups
	var rows []impRow
	for _, n := range plan.News {
		w := e.fileSide(st.imp, n.Card, n.Name)
		w.More = e.moreLine(n.Card)
		if n.Note != nil {
			w.NameNoteKind, w.NameNote = n.Note.Kind, e.nameNote(n.Note, n.Name)
		}
		rows = append(rows, impRow{Key: n.Key, Value: n.Key[1:], Who: w, Taken: !dec.Skip[n.Key]})
	}
	data["News"] = rows
	data["NewTitle"] = e.t("cimp.new.title.other")
	if len(rows) == 1 {
		data["NewTitle"] = e.t("cimp.new.title.one")
	}
	if len(plan.Same) > 0 {
		names := make([]string, len(plan.Same))
		for i, s := range plan.Same {
			names[i] = html.EscapeString(s.Name)
		}
		key := "cimp.same.other"
		if len(names) == 1 {
			key = "cimp.same.one"
		}
		and := html.EscapeString(e.t("contact.rel.and"))
		data["Same"] = template.HTML(e.t(key, "names", strings.Join(joinBold(names, and), ""))) // #nosec G203 -- the names are escaped above, the text is ours
	}
	var unread []impUnread
	for _, u := range plan.Unread {
		title := e.t("cimp.skip.card", "n", strconv.Itoa(u.Card.Number))
		text := e.t("cimp.skip.empty")
		if u.Problem == vcard.ProblemCut {
			if name := u.Card.Name(); name != "" {
				title = e.t("cimp.skip.card.named", "n", strconv.Itoa(u.Card.Number), "name", name)
			}
			text = e.t("cimp.skip.cut", "line", strconv.Itoa(u.Card.Line))
		}
		unread = append(unread, impUnread{title, text})
	}
	data["Unread"] = unread
	var groups []impGroup
	picked := map[string]bool{}
	for _, g := range dec.Groups {
		picked[g] = true
	}
	var pickedNames []string
	for _, g := range e.groups {
		groups = append(groups, impGroup{g.ID, e.groupName(g), picked[g.ID]})
		if picked[g.ID] {
			pickedNames = append(pickedNames, e.groupName(g))
		}
	}
	data["Groups"] = groups
	data["GroupSummary"] = e.t("cimp.grp.summary")
	data["GroupLegend"] = e.t("cimp.grp.legend")
	data["GroupNone"] = e.t("cimp.grp.none")
	data["GroupNames"] = strings.Join(pickedNames, ", ")
	if len(pickedNames) == 0 {
		data["GroupNames"] = e.t("cimp.grp.none")
	}
	data["GroupOpen"] = len(pickedNames) > 0
	if len(plan.Dups)+len(plan.News) == 1 && len(plan.News) == 1 {
		data["GroupSummary"], data["GroupLegend"] = e.t("cimp.grp.summary.one"), e.t("cimp.grp.legend.one")
	}
	data["HasNew"] = len(rows) > 0
	data["Save"] = e.t("cimp.save")
	if len(plan.Dups)+len(plan.News) == 1 {
		data["Save"] = e.t("cimp.save.one")
	} else {
		data["State"] = e.stateText(countState(plan, dec))
		data["StateTexts"] = map[string]string{"new": e.t("cimp.state.new", "n", "{n}"), "completed": e.t("cimp.state.completed", "n", "{n}"), "skipped": e.t("cimp.state.skipped", "n", "{n}"),
			"nothing": e.t("cimp.state.nothing"), "missing": e.t("cimp.state.missing", "n", "{n}"), "taken": e.t("cimp.new.taken"), "untaken": e.t("cimp.new.untaken")}
	}
	data["Cancel"] = e.t("cimp.cancel")
	data["TakenText"], data["UntakenText"] = e.t("cimp.new.taken"), e.t("cimp.new.untaken")
	if len(missing) > 0 {
		for i := range missing {
			switch {
			case i == len(missing)-2:
				missing[i].Sep = " " + e.t("contact.rel.and") + " "
			case i < len(missing)-2:
				missing[i].Sep = ", "
			}
		}
		data["ErrLinks"] = missing
		if len(missing) == 1 {
			data["ErrLead"] = e.t("cimp.err.form.one")
		} else {
			data["ErrLead"] = e.t("cimp.err.form.other", "n", strconv.Itoa(len(missing)))
		}
	}
	return data
}

// joinBold writes names as "<b>A</b>, <b>B</b> und <b>C</b>"; names are already escaped.
func joinBold(names []string, and string) []string {
	var out []string
	for i, n := range names {
		switch {
		case i == 0:
		case i == len(names)-1:
			out = append(out, " "+and+" ")
		default:
			out = append(out, ", ")
		}
		out = append(out, "<b>"+n+"</b>")
	}
	return out
}

// ---- the summary at the top

func (e ctEnv) summaryLines(plan contactimport.Plan) []template.HTML {
	var out []template.HTML
	h := func(key string, kv ...string) template.HTML { return formatHTML(e.s.i18n, e.lang, key, kv...) }
	plural := func(n int, one, other string) template.HTML {
		if n == 1 {
			return h(one)
		}
		return h(other, "n", strconv.Itoa(n))
	}
	// Both are only anchors for the sections: the page has one section for each of them.
	if plan.Cards == 1 && len(plan.News) == 1 {
		return []template.HTML{h("cimp.sum.new.only")}
	}
	if n := len(plan.Dups); n > 0 {
		out = append(out, template.HTML(`<a href="#dup">`)+plural(n, "cimp.sum.dups.one", "cimp.sum.dups.other")+`</a>`)
	}
	if n := len(plan.News); n > 0 {
		out = append(out, template.HTML(`<a href="#new">`)+plural(n, "cimp.sum.new.one", "cimp.sum.new.other")+`</a>`)
	}
	if len(plan.Same) > 0 {
		names := make([]string, len(plan.Same))
		for i, s := range plan.Same {
			names[i] = s.Name
		}
		out = append(out, template.HTML(`<a href="#same">`)+h("cimp.sum.same", "names", contact.JoinNames(names, e.t("contact.rel.and")))+`</a>`)
	}
	if n := len(plan.Unread); n > 0 {
		out = append(out, template.HTML(`<a href="#skip">`)+plural(n, "cimp.sum.skip.one", "cimp.sum.skip.other")+`</a>`)
	}
	return out
}

// reviewCount is what the state line of the save bar counts.
type reviewCount struct{ New, Completed, Skipped, Missing int }

func countState(plan contactimport.Plan, dec contactimport.Decisions) reviewCount {
	var c reviewCount
	for _, d := range plan.Dups {
		v, ok := dec.Dup[d.Key]
		if !ok {
			v = d.Preselect
		}
		switch {
		case !slices.Contains(d.Choices, v):
			c.Missing++
		case v == contactimport.ChoiceSkip:
			c.Skipped++
		case v == contactimport.ChoiceNew:
			c.New += len(d.Cards)
		case d.InFile:
			c.New++
		default:
			c.Completed++
		}
	}
	for _, n := range plan.News {
		if dec.Skip[n.Key] {
			c.Skipped++
		} else {
			c.New++
		}
	}
	return c
}

// stateText says in words what a tap on "Kontakte übernehmen" does now. The script of the page writes the same.
func (e ctEnv) stateText(c reviewCount) string {
	var parts []string
	for _, p := range []struct {
		n   int
		key string
	}{{c.New, "cimp.state.new"}, {c.Completed, "cimp.state.completed"}, {c.Skipped, "cimp.state.skipped"}} {
		if p.n > 0 {
			parts = append(parts, e.t(p.key, "n", strconv.Itoa(p.n)))
		}
	}
	text := e.t("cimp.state.nothing")
	if len(parts) > 0 {
		text = strings.Join(parts, ", ")
	}
	text += e.t("cimp.state.end")
	if c.Missing > 0 {
		text += " " + e.t("cimp.state.missing", "n", strconv.Itoa(c.Missing))
	}
	return text
}

// nameNote says why a new contact is not called what the file calls it.
func (e ctEnv) nameNote(n *contactimport.NameNote, name string) string {
	switch {
	case n.Kind == contactimport.NameLong:
		return e.t("cimp.name.long")
	case n.ByPerson:
		return e.t("cimp.name.taken.person", "wanted", n.Wanted, "name", name)
	}
	return e.t("cimp.name.taken", "wanted", n.Wanted, "name", name)
}

// ---- a possible duplicate

func fileName(c vcard.Card) string {
	if c.Full != "" {
		return c.Full
	}
	return c.Name()
}

func (e ctEnv) dupSide(imp store.ContactImport, c vcard.Card) impSide {
	s := e.fileSide(imp, c, fileName(c))
	s.Full = ""
	return s
}

// reasonHTML says in words why the cards match, with the matching values in bold.
func (e ctEnv) reasonHTML(d contactimport.Dup) template.HTML {
	if len(d.Matches) == 0 || d.Matches[0].Kind == contactimport.MatchName {
		return formatHTML(e.s.i18n, e.lang, "cimp.why.name")
	}
	var parts []string
	for _, kind := range []string{contactimport.MatchEmail, contactimport.MatchNumber} {
		for _, m := range d.Matches {
			if m.Kind != kind {
				continue
			}
			side := "first"
			if len(parts) > 0 {
				side = "next"
			}
			var part template.HTML
			if kind == contactimport.MatchEmail {
				part = formatHTML(e.s.i18n, e.lang, "cimp.why.email."+side, "value", m.Hejmo.Value)
			} else {
				part = formatHTML(e.s.i18n, e.lang, "cimp.why.number."+side, "value", e.kindWord(m.Hejmo, false)+" "+m.Hejmo.Value)
				switch {
				case strings.TrimSpace(m.File) == strings.TrimSpace(m.Hejmo.Value):
				case m.Code != "":
					part += " " + formatHTML(e.s.i18n, e.lang, "cimp.why.code", "code", m.Code)
				default:
					part += " " + formatHTML(e.s.i18n, e.lang, "cimp.why.spelled", "file", m.File)
				}
			}
			parts = append(parts, string(part))
			break
		}
	}
	return template.HTML(strings.Join(parts, " "+html.EscapeString(e.t("cimp.why.and"))+" ")) // #nosec G203 -- every part is made by formatHTML
}

func firstCardWith(cards []vcard.Card, f func(vcard.Card) bool) (vcard.Card, bool) {
	for _, c := range cards {
		if f(c) {
			return c, true
		}
	}
	return vcard.Card{}, false
}

func (e ctEnv) dupView(imp store.ContactImport, d contactimport.Dup, dec contactimport.Decisions, showMissing bool) impDupView {
	n := d.Key[1:]
	v := impDupView{ID: "d-" + n, Field: "d-" + n, InFile: d.InFile, Cards: len(d.Cards)}
	name := fileName(d.Cards[0])
	v.Legend = e.t("cimp.legend", "name", name)
	first := impBlock{K: e.t("cimp.k.file"), Who: e.dupSide(imp, d.Cards[0]), Why: e.reasonHTML(d)}
	if d.InFile {
		first.K = e.t("cimp.k.twice")
	}
	v.Blocks = append(v.Blocks, first)
	person := false
	if d.Existing != nil {
		person = d.Existing.IsPerson()
		k := e.t("cimp.k.hejmo")
		if person {
			k = e.t("cimp.k.hejmo.person")
		}
		v.Blocks = append(v.Blocks, impBlock{K: k, Who: e.hejmoSide(*d.Existing)})
	}
	for i := 1; i < len(d.Cards); i++ {
		k := e.t("cimp.k.second")
		if i > 1 {
			k = e.t("cimp.k.further")
		}
		v.Blocks = append(v.Blocks, impBlock{K: k, Who: e.dupSide(imp, d.Cards[i])})
	}
	if d.Preselect == "" {
		v.Need = e.t("cimp.need")
	}
	chosen := dec.Dup[d.Key]
	valid := slices.Contains(d.Choices, chosen)
	v.Invalid = showMissing && !valid
	if v.Invalid {
		v.Err = e.t("cimp.err.choose", "name", name)
	}
	for _, c := range d.Choices {
		ch := impChoice{Value: c, Checked: valid && chosen == c, Invalid: v.Invalid}
		switch {
		case d.InFile:
			switch c {
			case contactimport.ChoiceMerge:
				ch.Label, ch.Small = e.t("cimp.c.merge.file", "name", d.NewName), e.t("cimp.c.merge.file.small")
			case contactimport.ChoiceNew:
				ch.Label = e.t("cimp.c.new.many", "n", strconv.Itoa(len(d.Cards)))
				if len(d.Cards) == 2 {
					ch.Label = e.t("cimp.c.new.two")
				}
			default:
				ch.Label = e.t("cimp.c.skip.many")
				if len(d.Cards) == 2 {
					ch.Label = e.t("cimp.c.skip.two")
				}
			}
		default:
			switch c {
			case contactimport.ChoiceMerge:
				if person {
					ch.Label, ch.Small = e.t("cimp.c.merge.person", "name", d.Existing.HejmoName), e.t("cimp.c.merge.small.person")
				} else {
					ch.Label, ch.Small = e.t("cimp.c.merge", "name", d.Existing.HejmoName), e.t("cimp.c.merge.small")
				}
			case contactimport.ChoiceNew:
				ch.Label = e.t("cimp.c.new")
				if len(d.Cards) > 1 {
					// Every card becomes a contact of its own: say so, with every name.
					ch.Label = e.t("cimp.c.new.many", "n", strconv.Itoa(len(d.Cards)))
					if len(d.Cards) == 2 {
						ch.Label = e.t("cimp.c.new.two")
					}
					quoted := make([]string, len(d.NewNames))
					for i, n := range d.NewNames {
						quoted[i] = "“" + n + "”"
					}
					ch.Small = e.t("cimp.c.new.names", "names", contact.JoinNames(quoted, e.t("contact.rel.and")))
				} else if d.NewNote != nil && d.NewNote.Kind == contactimport.NameTaken {
					ch.Small = e.t("cimp.c.new.taken", "name", d.NewName)
				} else {
					ch.Small = e.t("cimp.c.new.name", "name", d.NewName)
				}
			default:
				ch.Label = e.t("cimp.c.skip")
				if person {
					ch.Small = e.t("cimp.c.skip.person", "name", d.Existing.HejmoName)
				}
			}
		}
		v.Choices = append(v.Choices, ch)
	}
	if d.InFile {
		v.Merge = e.inFileMerge(d)
	} else {
		v.Merge = e.mergeOut(imp, d, dec)
	}
	return v
}

// inFileMerge says what the contact made of several cards of the file holds.
func (e ctEnv) inFileMerge(d contactimport.Dup) impMergeOut {
	m := impMergeOut{Head: e.t("cimp.merge.h.file"), HasAdd: true}
	pv := d.Merged
	line := e.t("cimp.keep.file.name", "name", pv.Name, "full", pv.Full)
	if pv.Full == "" || strings.EqualFold(pv.Full, pv.Name) {
		line = e.t("cimp.keep.name", "name", pv.Name)
	}
	m.Add = append(m.Add, line)
	for _, l := range pv.Numbers {
		m.Add = append(m.Add, e.kindWord(l, false)+" "+l.Value)
	}
	for _, l := range pv.Emails {
		m.Add = append(m.Add, e.factLabel(l, true)+" "+l.Value)
	}
	if pv.HasPhoto {
		m.Add = append(m.Add, e.t("cimp.photo.file"))
	}
	return m
}

func clipText(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

// mergeOut says what merging adds to the contact in Hejmo and what stays as it is.
func (e ctEnv) mergeOut(imp store.ContactImport, d contactimport.Dup, dec contactimport.Decisions) impMergeOut {
	m := impMergeOut{Head: e.t("cimp.merge.h")}
	c := *d.Existing
	mg := d.Merge
	for _, l := range mg.AddNumbers {
		m.Add = append(m.Add, e.kindWord(l, false)+" "+l.Value)
	}
	for _, l := range mg.AddEmails {
		m.Add = append(m.Add, e.factLabel(l, true)+" "+l.Value)
	}
	if mg.AddFull != "" {
		m.Add = append(m.Add, e.t("cimp.add.full", "name", mg.AddFull))
	}
	if mg.AddAddress {
		m.Add = append(m.Add, e.t("cimp.add.address", "address", mg.Address))
	}
	if mg.AddBirthday {
		m.Add = append(m.Add, e.t("cimp.add.birthday", "date", e.bdayText(mg.Birthday[0], mg.Birthday[1], mg.Birthday[2])))
	}
	if mg.AddYear {
		m.Add = append(m.Add, e.t("cimp.add.year", "from", e.bdayText(c.BdayDay, c.BdayMonth, 0), "to", e.bdayText(mg.Birthday[0], mg.Birthday[1], mg.Birthday[2])))
	}
	if mg.AddNote {
		m.Add = append(m.Add, e.t("cimp.add.note", "text", clipText(mg.Note, 60)))
	}
	if mg.AddPhoto {
		m.Add = append(m.Add, e.t("cimp.add.photo"))
	}
	m.HasAdd = len(m.Add) > 0
	// What stays.
	person := c.IsPerson()
	parts := []string{e.t("cimp.keep.name", "name", c.HejmoName)}
	if c.FullName != "" && !strings.EqualFold(c.FullName, c.HejmoName) {
		parts = append(parts, e.t("cimp.keep.full"))
	}
	if len(c.Numbers) > 0 {
		parts = append(parts, e.t("cimp.keep.numbers"))
	}
	if len(c.Emails) > 0 {
		parts = append(parts, e.t("cimp.keep.emails"))
	}
	if c.HasPhoto && !mg.PhotoChoice {
		parts = append(parts, e.t("cimp.keep.photo"))
	}
	if !person {
		var groups []string
		for _, id := range c.GroupIDs {
			if g, ok := e.groupByID(id); ok {
				groups = append(groups, e.groupName(g))
			}
		}
		switch len(groups) {
		case 0:
		case 1:
			parts = append(parts, e.t("cimp.keep.groups.one", "names", groups[0]))
		default:
			parts = append(parts, e.t("cimp.keep.groups.other", "names", contact.JoinNames(groups, e.t("contact.rel.and"))))
		}
	}
	m.Keep = append(m.Keep, strings.Join(parts, ", "))
	if sents := e.relSentences(c, ""); len(sents) > 0 {
		key := "cimp.keep.rel.other"
		if len(sents) == 1 {
			key = "cimp.keep.rel.one"
		}
		m.Keep = append(m.Keep, e.t(key, "list", strings.Join(sents, e.t("cimp.keep.rel.sep"))))
	}
	differs := func(what, home, file string) {
		m.Keep = append(m.Keep, e.t("cimp.keep.differs", "what", e.t("cimp.keep.what."+what), "home", home, "file", file))
	}
	if mg.FileFull != "" {
		differs("full", "„"+c.FullName+"“", "„"+mg.FileFull+"“")
	}
	if mg.FileAddress != "" {
		differs("address", addressText(c.Street, c.Postcode, c.City, c.Country), mg.FileAddress)
	}
	if mg.FileBirthday != ([3]int{}) {
		differs("birthday", e.bdayText(c.BdayDay, c.BdayMonth, c.BdayYear), e.bdayText(mg.FileBirthday[0], mg.FileBirthday[1], mg.FileBirthday[2]))
	}
	if mg.FileNote != "" {
		differs("note", clipText(c.Note, 40), clipText(mg.FileNote, 40))
	}
	if mg.LockedName {
		m.Keep = append(m.Keep, e.t("cimp.keep.locked.name"))
	}
	if mg.LockedBirthday {
		m.Keep = append(m.Keep, e.t("cimp.keep.locked.birthday"))
	}
	if mg.LockedEmails {
		m.Keep = append(m.Keep, e.t("cimp.keep.locked.emails"))
	}
	if mg.Dropped > 0 {
		m.Keep = append(m.Keep, e.t("cimp.keep.dropped", "n", strconv.Itoa(mg.Dropped)))
	}
	card, _ := firstCardWith(d.Cards, func(c vcard.Card) bool { return len(c.Photo) > 0 })
	switch {
	case mg.PhotoChoice:
		w := e.fileSide(imp, card, c.HejmoName)
		m.Photo = &impPhotoChoose{Field: "ph-" + d.Key[1:], Legend: e.t("cimp.photo.legend", "name", c.HejmoName), Current: photoViewOf(c), File: w.Photo,
			KeepChecked: dec.Photo[d.Key] != "file"}
	case !c.HasPhoto && len(card.Photo) == 0:
		m.NoPhoto = e.t("cimp.photo.none")
	}
	return m
}

func addressText(street, postcode, city, country string) string {
	var parts []string
	if street != "" {
		parts = append(parts, street)
	}
	if l := strings.TrimSpace(postcode + " " + city); l != "" {
		parts = append(parts, l)
	}
	if country != "" {
		parts = append(parts, country)
	}
	return strings.Join(parts, ", ")
}

// ---- the pages without a form

func (e ctEnv) nothingNewView(p principal, file string, at time.Time, plan contactimport.Plan) map[string]any {
	data := map[string]any{"Person": p.person, "SyncStale": e.s.syncStale(p.household.ID), "File": file, "Nothing": true, "Title": e.t("cimp.review.title")}
	cards := e.t("cimp.cards.other", "n", strconv.Itoa(plan.Cards))
	if plan.Cards == 1 {
		cards = e.t("cimp.cards.one")
	}
	data["Meta"] = e.t("cimp.sum.file", "name", p.person.Name, "time", p.clockSeconds(at), "cards", cards)
	names := make([]string, len(plan.Same))
	for i, s := range plan.Same {
		names[i] = s.Name
	}
	switch len(names) {
	case 0:
		data["NothingText"] = e.t("cimp.nothing.text.none")
	case 1:
		data["NothingText"] = e.t("cimp.nothing.text.one", "names", names[0])
	default:
		data["NothingText"] = e.t("cimp.nothing.text", "names", contact.JoinNames(names, e.t("contact.rel.and")))
	}
	var unread []impUnread
	for _, u := range plan.Unread {
		title := e.t("cimp.skip.card", "n", strconv.Itoa(u.Card.Number))
		text := e.t("cimp.skip.empty")
		if u.Problem == vcard.ProblemCut {
			if name := u.Card.Name(); name != "" {
				title = e.t("cimp.skip.card.named", "n", strconv.Itoa(u.Card.Number), "name", name)
			}
			text = e.t("cimp.skip.cut", "line", strconv.Itoa(u.Card.Line))
		}
		unread = append(unread, impUnread{title, text})
	}
	data["Unread"] = unread
	return data
}

func (e ctEnv) expiredView(p principal, file string) map[string]any {
	return map[string]any{"Person": p.person, "SyncStale": e.s.syncStale(p.household.ID), "Title": e.t("cimp.review.title"), "Expired": true,
		"ExpiredText": e.t("cimp.expired.text", "file", file)}
}
