package server

import (
	"errors"
	"html/template"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/hejmo-home/hejmo/internal/datefmt"
	"github.com/hejmo-home/hejmo/internal/i18n"
	"github.com/hejmo-home/hejmo/internal/library"
	"github.com/hejmo-home/hejmo/internal/picture"
	"github.com/hejmo-home/hejmo/internal/reward"
	"github.com/hejmo-home/hejmo/internal/store"
)

// The web pages of the reward catalogue (ticket 291, preview-291): the overview
// for parents or the child's own view at /tasks/rewards, the form and the page
// of one reward. They live in their own mux: Go's mux cannot hold
// /tasks/rewards/{id}/archive next to /tasks/{id}/subtasks/{sub_id}, because
// both would match /tasks/rewards/subtasks/archive.

const rewardsPath = "/tasks/rewards"

// withRewardPages sends /tasks/rewards and everything below it to rewards.
func withRewardPages(rest, rewards http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if p := r.URL.Path; p == rewardsPath || strings.HasPrefix(p, rewardsPath+"/") {
			rewards.ServeHTTP(w, r)
			return
		}
		rest.ServeHTTP(w, r)
	})
}

func (s *server) routeRewards() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /tasks/rewards", s.handleRewards)
	mux.HandleFunc("GET /tasks/rewards/new", s.handleRewardNew)
	mux.HandleFunc("POST /tasks/rewards", s.handleRewardCreate)
	mux.HandleFunc("GET /tasks/rewards/{id}", s.handleReward)
	mux.HandleFunc("POST /tasks/rewards/{id}", s.handleRewardSave)
	mux.HandleFunc("POST /tasks/rewards/{id}/archive", s.handleRewardArchive)
	mux.HandleFunc("POST /tasks/rewards/{id}/restore", s.handleRewardRestore)
	mux.HandleFunc("POST /tasks/rewards/{id}/delete", s.handleRewardDelete)
	mux.HandleFunc("GET /tasks/rewards/{id}/picture", s.handleRewardPicture)
	return mux
}

// rewardSymbols are the symbols offered first: what rewards often are.
var rewardSymbols = []string{"🎁", "🍦", "📺", "🎮", "🎲", "🍕", "🎬", "📚", "🧸", "🌙", "🛝", "🏊", "🚲", "🧗", "🍫", "🪙", "🧑‍🤝‍🧑", "⏰"}

// The limit as the form's radio buttons write it.
var limitCodes = map[string]reward.Period{"": reward.NoLimit, "d": reward.Day, "w": reward.Week, "m": reward.Month}

func limitCode(p reward.Period) string {
	for code, period := range limitCodes {
		if period == p {
			return code
		}
	}
	return ""
}

// starsWords is "1 star" or "8 stars".
func (s *server) starsWords(lang string, n int) string {
	key := "rwd.stars.other"
	if n == 1 {
		key = "rwd.stars.one"
	}
	return s.i18n.Format(lang, i18n.Msg(key, "n", strconv.Itoa(n)))
}

// limitWords is the limit in words: "once a week", "3 times a month", "no limit".
func (s *server) limitWords(lang string, l reward.Limit) string {
	if l.Period == reward.NoLimit || l.Period == "" {
		return s.i18n.T(lang, "rwd.limit.none")
	}
	key := "rwd.limit." + string(l.Period) + ".many"
	if l.Count == 1 {
		key = "rwd.limit." + string(l.Period) + ".one"
	}
	return s.i18n.Format(lang, i18n.Msg(key, "n", strconv.Itoa(l.Count)))
}

// limitSummary is the line of "More options": the limit, or a request to check it.
func (s *server) limitSummary(lang string, f rewardForm, bad bool) string {
	if bad {
		return s.i18n.T(lang, "rwd.limit.sum.check")
	}
	period, ok := limitCodes[f.Period]
	if !ok || period == reward.NoLimit {
		return s.i18n.T(lang, "rwd.limit.sum.none")
	}
	n, err := strconv.Atoi(strings.TrimSpace(f.Count))
	if err != nil {
		return s.i18n.T(lang, "rwd.limit.sum.check")
	}
	key := "rwd.limit.sum.many." + string(period)
	if n == 1 {
		key = "rwd.limit.sum.one." + string(period)
	}
	return s.i18n.Format(lang, i18n.Msg(key, "n", strconv.Itoa(n)))
}

func rewardPictureURL(r store.Reward) string {
	return rewardsPath + "/" + r.ID + "/picture?v=" + strconv.FormatInt(r.PictureAt.Unix(), 10)
}

// pictureOf is the address of the picture, or "" when the reward shows its symbol.
func pictureOf(r store.Reward) string {
	if r.HasPicture {
		return rewardPictureURL(r)
	}
	return ""
}

// joinNames writes names as "Mama or Papa" ("Mama, Papa or Oma"); word is "rwd.or" or "rwd.and".
func (s *server) joinNames(lang, word string, names []string) string {
	switch len(names) {
	case 0:
		return ""
	case 1:
		return names[0]
	}
	return strings.Join(names[:len(names)-1], ", ") + s.i18n.T(lang, word) + names[len(names)-1]
}

// againWords is "Monday, 30.11." for the day a limit ends.
func (s *server) againWords(p principal, lang string, at time.Time) string {
	t := at.In(p.location())
	return s.i18n.T(lang, "weekday."+strconv.Itoa(int(t.Weekday()))) + ", " + datefmt.Short(p.household.DateFormat, t)
}

// Views.

type rewardRow struct {
	ID, Name, Symbol, Picture, Price, Limit string
	Whole                                   bool
	People                                  []personChip
	Archived                                bool
	ArchivedOn, Gone                        string
}

func (s *server) rewardRows(p principal, lang string, list []store.Reward, chips map[string]personChip) []rewardRow {
	rows := make([]rewardRow, 0, len(list))
	for _, r := range list {
		row := rewardRow{ID: r.ID, Name: r.Name, Symbol: r.Symbol, Picture: pictureOf(r), Price: s.starsWords(lang, r.Price), Limit: s.limitWords(lang, r.Limit),
			Whole: len(r.PersonIDs) == 0 && r.ArchivedReason != store.ArchivedAudienceGone, Archived: r.Archived()}
		for _, id := range r.PersonIDs {
			if c, ok := chips[id]; ok {
				row.People = append(row.People, c)
			}
		}
		if r.Archived() {
			row.ArchivedOn = s.i18n.Format(lang, i18n.Msg("rwd.arch.on", "date", p.day(r.ArchivedAt)))
			if r.ArchivedReason == store.ArchivedAudienceGone {
				row.Gone = s.goneText(lang, r.ArchivedFor, "rwd.arch.gone", "rwd.arch.gone.anon")
			}
		}
		rows = append(rows, row)
	}
	return rows
}

func (s *server) goneText(lang, name, key, anon string) string {
	if name == "" {
		return s.i18n.T(lang, anon)
	}
	return s.i18n.Format(lang, i18n.Msg(key, "name", name))
}

func chipMap(people []store.Person) map[string]personChip {
	m := map[string]personChip{}
	for _, c := range chipsOf(people) {
		m[c.ID] = c
	}
	return m
}

// handleRewards is /tasks/rewards: the catalogue for a parent, the child's own view for a child.
func (s *server) handleRewards(w http.ResponseWriter, r *http.Request) {
	p, ok := s.memberPage(w, r)
	if !ok {
		return
	}
	lang := s.langOf(r)
	people, err := s.db.People(p.household.ID)
	if err != nil {
		s.fail(w, "people", err)
		return
	}
	if isChild(p) {
		s.renderChildRewards(w, r, p, lang, people)
		return
	}
	all, err := s.db.Rewards(p.household.ID)
	if err != nil {
		s.fail(w, "rewards", err)
		return
	}
	var offered, archived []store.Reward
	for _, rw := range all {
		if rw.Archived() {
			archived = append(archived, rw)
		} else {
			offered = append(offered, rw)
		}
	}
	slices.SortStableFunc(archived, func(a, b store.Reward) int { return b.ArchivedAt.Compare(a.ArchivedAt) })
	chips := chipMap(people)
	s.render(w, r, http.StatusOK, "rewards.html", map[string]any{
		"Person": p.person, "SyncStale": s.syncStale(p.household.ID), "Parent": true,
		"Rows": s.rewardRows(p, lang, offered, chips), "Archived": s.rewardRows(p, lang, archived, chips), "HasAny": len(all) > 0,
	})
}

type childCard struct {
	Name, Symbol, Picture, Price, Limit string
	Missing, Progress, Label, Later     string
	Pct                                 int
}

// renderChildRewards is the child's view: their balance and the rewards meant
// for them in three groups (enough stars, still saving, not right now).
func (s *server) renderChildRewards(w http.ResponseWriter, r *http.Request, p principal, lang string, people []store.Person) {
	list, err := s.db.RewardsFor(p.household.ID, p.person.ID)
	if err != nil {
		s.fail(w, "rewards", err)
		return
	}
	balance, states, err := s.rewardStates(p, p.person.ID, list)
	if err != nil {
		s.fail(w, "reward states", err)
		return
	}
	var ok, save, later []childCard
	for _, rw := range list {
		st := states[rw.ID]
		c := childCard{Name: rw.Name, Symbol: rw.Symbol, Picture: pictureOf(rw), Price: s.starsWords(lang, rw.Price)}
		if rw.Limit.Period != reward.NoLimit {
			c.Limit = s.limitWords(lang, rw.Limit)
		}
		switch {
		case st.LimitReached:
			key := "rwd.child.later." + string(rw.Limit.Period) + ".one"
			if rw.Limit.Count > 1 {
				key = "rwd.child.later." + string(rw.Limit.Period) + ".many"
			}
			c.Later = s.i18n.Format(lang, i18n.Msg(key, "n", strconv.Itoa(rw.Limit.Count)))
			c.Missing = s.i18n.Format(lang, i18n.Msg("rwd.child.later.again", "when", s.againWords(p, lang, st.AvailableAgain)))
			later = append(later, c)
		case st.Affordable:
			ok = append(ok, c)
		default:
			key := "rwd.child.missing.other"
			if st.Missing == 1 {
				key = "rwd.child.missing.one"
			}
			c.Missing = s.i18n.Format(lang, i18n.Msg(key, "n", strconv.Itoa(st.Missing)))
			c.Pct = max(0, min(100, balance*100/rw.Price))
			c.Label = s.i18n.Format(lang, i18n.Msg("rwd.child.progress", "have", strconv.Itoa(balance), "price", strconv.Itoa(rw.Price)))
			save = append(save, c)
		}
	}
	var parents []string
	for _, x := range people {
		if x.Role == store.RoleParent {
			parents = append(parents, x.Name)
		}
	}
	chips := chipsOf(people)
	slot := 0
	for _, c := range chips {
		if c.ID == p.person.ID {
			slot = c.Slot
		}
	}
	bal := template.HTML(s.i18n.Format(lang, i18n.Msg("rwd.child.bal", "stars", s.starsWords(lang, balance))))
	if balance <= 0 {
		bal = template.HTML(s.i18n.T(lang, "rwd.child.bal.none"))
	}
	s.render(w, r, http.StatusOK, "rewards.html", map[string]any{
		"Person": p.person, "SyncStale": s.syncStale(p.household.ID), "Parent": false, "ChildSlot": slot,
		"Balance": bal, "NoStars": balance <= 0, "Ok": ok, "Save": save, "Later": later, "Any": len(list) > 0,
		"Empty": s.i18n.Format(lang, i18n.Msg("rwd.child.empty", "parents", s.joinNames(lang, "rwd.or", parents))),
	})
}

// The form.

// rewardForm is what the form holds: the stored reward, or what was posted.
type rewardForm struct {
	Name, Price, Symbol, SymQ, Token, PicSource, Period, Count string
	Whole                                                      bool
	Persons                                                    []string
	Replacing                                                  bool
}

func (s *server) formOfReward(r store.Reward) rewardForm {
	f := rewardForm{Name: r.Name, Price: strconv.Itoa(r.Price), Symbol: r.Symbol, PicSource: r.PicSource, Period: limitCode(r.Limit.Period),
		Whole: len(r.PersonIDs) == 0, Persons: r.PersonIDs}
	if r.Limit.Period != reward.NoLimit {
		f.Count = strconv.Itoa(r.Limit.Count)
	}
	return f
}

func newRewardForm() rewardForm {
	return rewardForm{Symbol: reward.DefaultSymbol, PicSource: "symbol", Whole: true, Count: "1"}
}

func (s *server) readRewardForm(w http.ResponseWriter, r *http.Request) (f rewardForm, err error) {
	r.Body = http.MaxBytesReader(w, r.Body, maxForm)
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		err = r.ParseMultipartForm(4 << 20)
	} else {
		err = r.ParseForm()
	}
	if err != nil {
		return f, err
	}
	v := r.PostFormValue
	return rewardForm{Name: v("name"), Price: strings.TrimSpace(v("price")), Symbol: v("symbol"), SymQ: strings.TrimSpace(v("symq")), Token: v("picture_token"), PicSource: v("pic_source"),
		Period: v("limit_period"), Count: strings.TrimSpace(v("limit_count")), Whole: v("whole_family") != "", Persons: r.PostForm["person"], Replacing: v("replacing") != ""}, nil
}

type factRow struct{ Label, Text, Hint string }

type redeemedFact struct {
	Total, Intro, Last string
	By                 []redeemedBy
}

type redeemedBy struct {
	Sep   string
	Chip  personChip
	Known bool
	Times string
}

func (s *server) timesWords(lang string, n int) string {
	if n == 1 {
		return s.i18n.T(lang, "rwd.detail.times.one")
	}
	return s.i18n.Format(lang, i18n.Msg("rwd.detail.times.many", "n", strconv.Itoa(n)))
}

// redeemedOf says how often the reward was redeemed, by whom and when last.
func (s *server) redeemedOf(p principal, lang string, f store.RewardFacts, chips map[string]personChip) *redeemedFact {
	if f.Count == 0 {
		return nil
	}
	out := &redeemedFact{Total: s.timesWords(lang, f.Count)}
	out.Intro = out.Total + ": "
	for i, bp := range f.ByPerson {
		c, ok := chips[bp.PersonID]
		sep := ""
		if i > 0 {
			sep = ", "
		}
		out.By = append(out.By, redeemedBy{Sep: sep, Chip: c, Known: ok, Times: s.timesWords(lang, bp.Count)})
	}
	if c, ok := chips[f.LastBy]; ok {
		out.Last = s.i18n.Format(lang, i18n.Msg("rwd.detail.last", "date", p.day(f.Last), "name", c.Name))
	} else {
		out.Last = s.i18n.Format(lang, i18n.Msg("rwd.detail.last.anon", "date", p.day(f.Last)))
	}
	return out
}

// renderRewardForm shows the form of a new reward (existing nil) or the page of a reward.
func (s *server) renderRewardForm(w http.ResponseWriter, r *http.Request, p principal, status int, existing *store.Reward, f rewardForm, pend *pendingPicture, errs map[string]string) {
	lang := s.langOf(r)
	if errs == nil {
		errs = map[string]string{}
	}
	people, err := s.db.People(p.household.ID)
	if err != nil {
		s.fail(w, "people", err)
		return
	}
	chips := chipsOf(people)

	// What the picture choice shows.
	source := f.PicSource
	var res *picResult
	switch {
	case pend != nil:
		source = "upload"
		res = &picResult{Source: "upload", URL: "/settings/lists/library/picture/" + f.Token, Title: s.i18n.T(lang, "rwd.res.title"), Text: s.i18n.T(lang, "rwd.res.text"), CanRemove: true}
	case existing != nil && existing.HasPicture && !f.Replacing:
		source = "upload"
		res = &picResult{Source: "upload", URL: rewardPictureURL(*existing), Title: s.i18n.T(lang, "rwd.res.title"), Text: s.i18n.T(lang, "rwd.res.text"), CanRemove: true}
	}
	if res != nil {
		f.Token = tokenOf(pend, f.Token)
	}
	if source != "upload" {
		source = "symbol"
	}

	// The symbol panel: what rewards often are, or what a search found.
	if f.Symbol == "" {
		f.Symbol = reward.DefaultSymbol
	}
	chosen := symTile{}
	if sy, ok := library.SymbolByEmoji(f.Symbol); ok {
		chosen = symTile{Emoji: f.Symbol, Name: s.rewardSymbolName(lang, f.Symbol, sy), Checked: true}
	}
	var tiles []symTile
	var symHead, symLegend, symNone string
	if q := strings.TrimSpace(f.SymQ); q != "" {
		found := library.SearchSymbols(q, lang)
		if len(found) == 0 {
			symNone = s.i18n.Format(lang, i18n.Msg("rwd.sym.none", "q", q))
		} else {
			key := "artf.sym.count.many"
			if len(found) == 1 {
				key = "artf.sym.count.one"
			}
			symHead = s.i18n.Format(lang, i18n.Msg(key, "n", strconv.Itoa(len(found)), "q", q))
			symLegend = s.i18n.Format(lang, i18n.Msg("artf.sym.results", "q", q))
			if len(found) > maxSymbolTiles {
				found = found[:maxSymbolTiles]
			}
			for _, x := range found {
				tiles = append(tiles, symTile{x.Emoji, library.SymbolName(x, lang), library.CanonEmoji(x.Emoji) == library.CanonEmoji(chosen.Emoji)})
			}
		}
	} else {
		symLegend = s.i18n.T(lang, "rwd.sym.legend")
		for i, e := range rewardSymbols {
			tiles = append(tiles, symTile{e, s.i18n.T(lang, "rwd.sym."+strconv.Itoa(i)), library.CanonEmoji(e) == library.CanonEmoji(chosen.Emoji)})
		}
		if !slices.ContainsFunc(tiles, func(t symTile) bool { return t.Checked }) && chosen.Emoji != "" {
			tiles = append([]symTile{chosen}, tiles...)
		}
	}
	chosenText := s.i18n.T(lang, "rwd.sym.shown")
	if existing == nil && f.Symbol == reward.DefaultSymbol {
		chosenText = s.i18n.T(lang, "rwd.sym.default")
	}

	// The limit, and whether its box is open.
	limitBad := errs["limit_period"] != "" || errs["limit_count"] != ""
	period := limitCodes[f.Period]
	first := ""
	for _, field := range []string{"name", "photo", "price", "person", "limit_count", "symbol"} {
		if errs[field] != "" {
			first = field
			break
		}
	}
	selected := f.Persons
	data := map[string]any{
		"Person": p.person, "SyncStale": s.syncStale(p.household.ID), "New": existing == nil, "F": f, "Errs": errs, "FirstErr": first,
		"Source": source, "Result": res, "Chosen": chosen, "ChosenText": chosenText, "ChosenLabel": s.i18n.Format(lang, i18n.Msg("rwd.sym.chosen", "name", chosen.Name)),
		"Tiles": tiles, "SymHead": symHead, "SymLegend": symLegend, "SymNone": symNone, "SymSearching": strings.TrimSpace(f.SymQ) != "",
		"People": chips, "Selected": selected, "WholeChecked": f.Whole && len(selected) == 0,
		"LimitSummary": s.limitSummary(lang, f, limitBad), "LimitOpen": limitBad || (period != reward.NoLimit && f.Period != ""), "Period": f.Period,
		"Title": s.i18n.T(lang, "rwd.form.title"), "Name": "", "ID": "", "Archived": false,
	}
	if existing != nil {
		rw := *existing
		data["ID"], data["Name"], data["Title"], data["Archived"] = rw.ID, rw.Name, rw.Name, rw.Archived()
		data["Symbol"], data["Picture"] = rw.Symbol, pictureOf(rw)
		facts, err := s.db.RewardFacts(p.household.ID, rw.ID)
		if err != nil {
			s.fail(w, "reward facts", err)
			return
		}
		cm := chipMap(people)
		data["Redeemed"] = s.redeemedOf(p, lang, facts, cm)
		created := s.i18n.Format(lang, i18n.Msg("rwd.detail.created.anon", "date", p.day(rw.CreatedAt)))
		if c, ok := cm[rw.CreatedBy]; ok {
			created = s.i18n.Format(lang, i18n.Msg("rwd.detail.created.by", "name", c.Name, "date", p.day(rw.CreatedAt)))
		}
		data["CreatedText"] = created
		data["PriceWords"], data["LimitWords"] = s.starsWords(lang, rw.Price), s.limitWords(lang, rw.Limit)
		data["Whole"] = len(rw.PersonIDs) == 0 && rw.ArchivedReason != store.ArchivedAudienceGone
		var who []personChip
		for _, id := range rw.PersonIDs {
			if c, ok := cm[id]; ok {
				who = append(who, c)
			}
		}
		data["Who"] = who
		name := rw.Name
		if rw.Archived() {
			on := p.day(rw.ArchivedAt)
			key := "rwd.archived.note"
			if rw.ArchivedReason == store.ArchivedAudienceGone {
				key = "rwd.archived.note.gone"
				if rw.ArchivedFor == "" {
					key = "rwd.archived.note.gone.anon"
				}
			}
			data["ArchivedNote"] = template.HTML(s.i18n.Format(lang, i18n.Msg(key, "date", on, "name", template.HTMLEscapeString(rw.ArchivedFor))))
			data["Gone"] = rw.ArchivedReason == store.ArchivedAudienceGone
		}
		if facts.Count > 0 {
			var names []string
			for _, bp := range facts.ByPerson {
				if c, ok := cm[bp.PersonID]; ok {
					names = append(names, c.Name)
				}
			}
			key := "rwd.archive.stays.many"
			if facts.Count == 1 {
				key = "rwd.archive.stays.one"
			}
			data["ArchiveExplain"] = template.HTML(s.i18n.Format(lang, i18n.Msg("rwd.archive.explain", "name", template.HTMLEscapeString(name))))
			data["ArchiveStays"] = s.i18n.Format(lang, i18n.Msg(key, "n", strconv.Itoa(facts.Count), "people", s.joinNames(lang, "rwd.and", names)))
			data["ArchiveConfirm"] = s.i18n.Format(lang, i18n.Msg("rwd.archive.confirm", "name", name))
		}
		data["DeleteExplain"] = s.i18n.Format(lang, i18n.Msg("rwd.delete.explain", "name", name))
		data["DeleteConfirm"] = s.i18n.Format(lang, i18n.Msg("rwd.delete.confirm", "name", name))
		s.render(w, r, status, "reward.html", data)
		return
	}
	s.render(w, r, status, "reward_form.html", data)
}

func (s *server) rewardSymbolName(lang, emoji string, sy library.Symbol) string {
	if i := slices.Index(rewardSymbols, emoji); i >= 0 {
		return s.i18n.T(lang, "rwd.sym."+strconv.Itoa(i))
	}
	return library.SymbolName(sy, lang)
}

func (s *server) handleRewardNew(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	s.renderRewardForm(w, r, p, http.StatusOK, nil, newRewardForm(), nil, nil)
}

func (s *server) handleRewardCreate(w http.ResponseWriter, r *http.Request) {
	if p, ok := s.parentPage(w, r); ok {
		s.submitReward(w, r, p, "")
	}
}

func (s *server) handleRewardSave(w http.ResponseWriter, r *http.Request) {
	if p, ok := s.parentPage(w, r); ok {
		s.submitReward(w, r, p, r.PathValue("id"))
	}
}

// loadReward finds the reward of the page, or answers 404.
func (s *server) loadReward(w http.ResponseWriter, r *http.Request, p principal) (store.Reward, bool) {
	rw, err := s.db.Reward(p.household.ID, r.PathValue("id"))
	switch {
	case errors.Is(err, store.ErrNotFound):
		http.NotFound(w, r)
		return rw, false
	case err != nil:
		s.fail(w, "reward", err)
		return rw, false
	}
	return rw, true
}

func (s *server) handleReward(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	rw, ok := s.loadReward(w, r, p)
	if !ok {
		return
	}
	s.renderRewardForm(w, r, p, http.StatusOK, &rw, s.formOfReward(rw), nil, nil)
}

// submitReward handles the posted form of a new (existingID empty) or an
// existing reward: a photo is processed, symbols searched, or the reward saved.
func (s *server) submitReward(w http.ResponseWriter, r *http.Request, p principal, existingID string) {
	lang := s.langOf(r)
	var existing *store.Reward
	if existingID != "" {
		rw, err := s.db.Reward(p.household.ID, existingID)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		existing = &rw
		if rw.Archived() {
			http.Redirect(w, r, rewardsPath+"/"+rw.ID, http.StatusSeeOther)
			return
		}
	}
	errs := map[string]string{}
	f, err := s.readRewardForm(w, r)
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			errs["photo"] = s.i18n.T(lang, "rwd.err.toobig")
			form := newRewardForm()
			if existing != nil {
				form = s.formOfReward(*existing)
			}
			s.renderRewardForm(w, r, p, http.StatusRequestEntityTooLarge, existing, form, nil, errs)
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
	case r.PostFormValue("picture") == "replace":
		action = "replace"
	case r.PostFormValue("picture") == "remove":
		action = "remove"
	}
	if action == "replace" || action == "remove" {
		s.pictures.drop(f.Token)
		f.Token, havePend = "", false
		f.Replacing = true
		if action == "remove" {
			f.PicSource = "symbol"
		}
	}

	// A photo that came with the form.
	if r.MultipartForm != nil && f.PicSource == "upload" && action != "replace" && action != "remove" {
		if fs := r.MultipartForm.File["photo"]; len(fs) > 0 && fs[0].Size > 0 {
			if res, perr := s.processUpload(r.Context(), fs[0]); perr != nil {
				errs["photo"] = s.rewardPhotoError(lang, fs[0].Filename, fs[0].Size, perr)
			} else {
				s.pictures.drop(f.Token)
				pend, havePend = pendingFrom(res, "upload", "", fs[0].Filename), true
				f.Token = newToken()
				s.pictures.put(f.Token, pend, now)
			}
		}
	}
	var pendPtr *pendingPicture
	if havePend {
		pendPtr = &pend
	}
	if action == "symsearch" || action == "replace" || action == "remove" {
		status := http.StatusOK
		if len(errs) > 0 {
			status = http.StatusUnprocessableEntity
		}
		s.renderRewardForm(w, r, p, status, existing, f, pendPtr, errs)
		return
	}

	// Saving. The upload source counts only when a photo is there to keep.
	keepsPhoto := existing != nil && existing.HasPicture && !f.Replacing
	uploads := f.PicSource == "upload" && (havePend || keepsPhoto)
	in := reward.Input{Name: f.Name, Symbol: f.Symbol, Limit: reward.Limit{Period: limitCodes[f.Period]}}
	if period, ok := limitCodes[f.Period]; !ok {
		in.Limit.Period = reward.Period("?")
	} else if period != reward.NoLimit {
		n, err := strconv.Atoi(f.Count)
		if err != nil {
			n = 0
		}
		in.Limit.Count = n
	}
	price, perr := strconv.Atoi(f.Price)
	if perr != nil {
		price = 0
	}
	in.Price = price
	for _, e := range in.Validate() {
		if _, dup := errs[e.Field]; !dup {
			errs[e.Field] = s.rewardFieldError(lang, e)
		}
	}
	if price > reward.MaxPrice {
		errs["price"] = s.i18n.T(lang, "rwd.err.price.high")
	}
	if _, ok := library.SymbolByEmoji(in.Normalized().Symbol); !ok {
		errs["symbol"] = s.i18n.T(lang, "rwd.err.symbol")
	}
	var audience []string
	switch {
	case len(f.Persons) > 0:
		audience = f.Persons
	case !f.Whole:
		errs["person"] = s.i18n.T(lang, "rwd.err.person")
	}
	if len(errs) > 0 {
		s.renderRewardForm(w, r, p, http.StatusUnprocessableEntity, existing, f, pendPtr, errs)
		return
	}
	in = in.Normalized()
	rw := store.Reward{Name: in.Name, Symbol: in.Symbol, Price: in.Price, Limit: in.Limit, PersonIDs: audience}
	var pic *store.ArticlePicture
	if uploads && havePend {
		pc := pend.ArticlePicture
		pic = &pc
	}
	var saved store.Reward
	switch {
	case existing == nil:
		saved, err = s.db.CreateReward(p.household.ID, p.person.ID, rw, pic)
	default:
		rw.ID = existing.ID
		change := store.KeepPicture
		switch {
		case pic != nil:
			change = store.SetPicture
		case !uploads && existing.HasPicture, !uploads && f.Symbol != existing.Symbol:
			change = store.RemovePicture
		}
		saved, err = s.db.UpdateReward(p.household.ID, rw, change, pic)
	}
	var fe *store.FieldError
	switch {
	case errors.As(err, &fe):
		errs[fe.Field] = s.rewardFieldError(lang, reward.FieldErr{Field: fe.Field})
		s.renderRewardForm(w, r, p, http.StatusUnprocessableEntity, existing, f, pendPtr, errs)
		return
	case errors.Is(err, store.ErrArchived):
		http.Redirect(w, r, rewardsPath+"/"+existing.ID, http.StatusSeeOther)
		return
	case err != nil:
		s.fail(w, "save reward", err)
		return
	}
	s.pictures.drop(f.Token)
	if existing == nil {
		s.redirectSavedNamed(w, r, rewardsPath, "flash.reward_created", saved.Name)
		return
	}
	s.redirectSavedNamed(w, r, rewardsPath+"/"+saved.ID, "flash.reward_saved", saved.Name)
}

// rewardFieldError is the sentence for a refused field of the form.
func (s *server) rewardFieldError(lang string, e reward.FieldErr) string {
	switch e.Field + "." + e.Code {
	case "name.required":
		return s.i18n.T(lang, "rwd.err.name.required")
	case "name.long":
		return s.i18n.T(lang, "rwd.err.name.long")
	case "price.range":
		return s.i18n.T(lang, "rwd.err.price")
	case "symbol.":
		return s.i18n.T(lang, "rwd.err.symbol")
	case "person.":
		return s.i18n.T(lang, "rwd.err.person")
	}
	switch e.Field {
	case "limit_period", "limit_count":
		return s.i18n.T(lang, "rwd.err.count")
	case "price":
		return s.i18n.T(lang, "rwd.err.price")
	}
	return s.i18n.T(lang, "rwd.err.name.required")
}

func (s *server) rewardPhotoError(lang, file string, size int64, err error) string {
	var big *picture.TooLargeError
	switch {
	case errors.As(err, &big):
		if size > big.Bytes {
			big.Bytes = size
		}
		return s.i18n.Format(lang, i18n.Msg("rwd.err.photo.big", "file", file, "size", humanBytes(lang, big.Bytes)))
	case errors.Is(err, picture.ErrWebPage):
		return s.i18n.Format(lang, i18n.Msg("rwd.err.photo.webpage", "file", file))
	case errors.Is(err, picture.ErrTooManyPixels):
		return s.i18n.Format(lang, i18n.Msg("rwd.err.photo.pixels", "file", file))
	}
	return s.i18n.Format(lang, i18n.Msg("rwd.err.photo.notimage", "file", file))
}

func (s *server) handleRewardArchive(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	saved, err := s.db.ArchiveReward(p.household.ID, r.PathValue("id"), s.now())
	switch {
	case errors.Is(err, store.ErrNotFound):
		http.NotFound(w, r)
	case err != nil:
		s.fail(w, "archive reward", err)
	default:
		s.redirectSavedNamed(w, r, rewardsPath, "flash.reward_archived", saved.Name)
	}
}

func (s *server) handleRewardRestore(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	// A reward the box archived because its people left needs a new audience: the form asks.
	var aud *store.Audience
	if r.PostForm.Has("whole_family") || r.PostForm.Has("person") || r.PostForm.Has("audience") {
		aud = &store.Audience{Whole: len(r.PostForm["person"]) == 0 && r.PostForm.Has("whole_family"), PersonIDs: r.PostForm["person"]}
	}
	saved, err := s.db.RestoreReward(p.household.ID, r.PathValue("id"), aud)
	switch {
	case errors.Is(err, store.ErrNotFound):
		http.NotFound(w, r)
	case errors.Is(err, store.ErrInvalid):
		rw, gerr := s.db.Reward(p.household.ID, r.PathValue("id"))
		if gerr != nil {
			http.NotFound(w, r)
			return
		}
		s.renderRewardForm(w, r, p, http.StatusUnprocessableEntity, &rw, s.formOfReward(rw), nil, map[string]string{"restore": s.i18n.T(s.langOf(r), "rwd.err.person")})
	case err != nil:
		s.fail(w, "restore reward", err)
	default:
		s.redirectSavedNamed(w, r, rewardsPath+"/"+saved.ID, "flash.reward_restored", saved.Name)
	}
}

func (s *server) handleRewardDelete(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	rw, ok := s.loadReward(w, r, p)
	if !ok {
		return
	}
	switch err := s.db.DeleteReward(p.household.ID, rw.ID); {
	case errors.Is(err, store.ErrNotFound):
		http.NotFound(w, r)
	case errors.Is(err, store.ErrRedeemed):
		// A redeemed reward is archived, never deleted (Y): the page offers no delete box for it.
		http.Error(w, s.i18n.Format(s.langOf(r), i18n.Msg("rwd.conflict.redeemed", "name", rw.Name)), http.StatusConflict)
	case err != nil:
		s.fail(w, "delete reward", err)
	default:
		s.redirectSavedNamed(w, r, rewardsPath, "flash.reward_deleted", rw.Name)
	}
}

// handleRewardPicture serves the stored photo of a reward to a member of the
// household; a child gets only the pictures of rewards offered to them.
func (s *server) handleRewardPicture(w http.ResponseWriter, r *http.Request) {
	p, ok := s.memberPage(w, r)
	if !ok {
		return
	}
	rw, err := s.db.Reward(p.household.ID, r.PathValue("id"))
	if err != nil || (isChild(p) && (rw.Archived() || !rw.MeantFor(p.person.ID))) {
		http.NotFound(w, r)
		return
	}
	b, err := s.db.RewardPicture(p.household.ID, rw.ID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Content-Length", strconv.Itoa(len(b)))
	w.Header().Set("Cache-Control", "private, max-age=31536000, immutable") // the address changes with the picture
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(b)
}
