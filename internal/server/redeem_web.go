package server

import (
	"context"
	"errors"
	"html"
	"html/template"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/hejmo-home/hejmo/internal/datefmt"
	"github.com/hejmo-home/hejmo/internal/i18n"
	"github.com/hejmo-home/hejmo/internal/reward"
	"github.com/hejmo-home/hejmo/internal/store"
)

// Redeeming rewards in the web interface (ticket 292, preview-292): the child
// asks, a parent says yes or no on Start and on the rewards page, or redeems
// directly from the page of a reward. Results are success lines after a
// redirect; a refusal is a calm notice or a field error on the page itself.

// askOutcome is what a refused or too-late action shows on the page that
// follows it: a notice at the top, an error at one row, an error at the
// chips of the redeem box.
type askOutcome struct {
	Notice template.HTML
	Err    map[string]string // by request id
	// RedeemErr is the sentence at the chips of the direct redemption box, which then stays open.
	RedeemErr string
	Redeem    bool
}

type outcomeKey struct{}

func withOutcome(r *http.Request, o askOutcome) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), outcomeKey{}, o))
}

func outcomeOf(r *http.Request) askOutcome {
	o, _ := r.Context().Value(outcomeKey{}).(askOutcome)
	return o
}

// The success lines of this ticket. Their values travel in the box's memory.
func init() {
	flashFields["flash.rwd_asked"] = []string{"reward", "parents"}
	flashFields["flash.rwd_asked_one"] = []string{"reward", "parents"}
	flashFields["flash.rwd_withdrawn"] = []string{"reward", "stars"}
	flashFields["flash.rwd_redeemed"] = []string{"reward", "name", "stars"}
	flashFields["flash.rwd_declined"] = []string{"reward", "name"}
}

// rwdFlashHTML is a success line of this ticket: the values escaped, the markup from the locale.
func (s *server) rwdFlashHTML(lang, key, token string) template.HTML {
	values, ok := s.fieldValues(key, token)
	if !ok {
		return ""
	}
	kv := make([]string, 0, 2*len(values))
	for _, n := range flashFields[key] {
		kv = append(kv, n, html.EscapeString(values[n]))
	}
	return template.HTML(s.i18n.Format(lang, i18n.Msg(key, kv...)))
}

// rwdWhen says when something happened: "today at 16:05", "yesterday at 18:20",
// else "Monday, 23.11., 17:42". Answer puts it at the start of a line.
func (s *server) rwdWhen(p principal, lang string, at time.Time, answer bool) string {
	loc := p.location()
	t, now := at.In(loc), s.now().In(loc)
	clock := datefmt.Clock(p.household.TimeFormat, t)
	midnight := func(x time.Time) time.Time { return time.Date(x.Year(), x.Month(), x.Day(), 0, 0, 0, 0, loc) }
	days := int(math.Round(midnight(now).Sub(midnight(t)).Hours() / 24))
	suffix := ""
	if answer {
		suffix = ".a"
	}
	switch days {
	case 0:
		return s.i18n.Format(lang, i18n.Msg("rwd.when.today"+suffix, "time", clock))
	case 1:
		return s.i18n.Format(lang, i18n.Msg("rwd.when.yesterday"+suffix, "time", clock))
	}
	return s.i18n.Format(lang, i18n.Msg("rwd.when.other", "weekday", s.i18n.T(lang, "weekday."+strconv.Itoa(int(t.Weekday()))), "date", datefmt.Short(p.household.DateFormat, t), "time", clock))
}

// limitAgain is when a used-up limit ends: "tomorrow, Tuesday 24.11." or "Monday, 30.11.".
func (s *server) limitAgain(p principal, lang string, period reward.Period, at time.Time) string {
	if period == reward.Day {
		return s.i18n.Format(lang, i18n.Msg("rwd.ask.tomorrow", "when", s.againWords(p, lang, at)))
	}
	return s.againWords(p, lang, at)
}

func negStars(n int) string {
	if n < 0 {
		return "−" + strconv.Itoa(-n)
	}
	return strconv.Itoa(n)
}

// starsText is "1 star" or "8 stars", with a real minus for a balance below zero.
func (s *server) starsText(lang string, n int) string {
	key := "rwd.stars.other"
	if n == 1 {
		key = "rwd.stars.one"
	}
	return s.i18n.Format(lang, i18n.Msg(key, "n", negStars(n)))
}

func (s *server) parentNames(people []store.Person) []string {
	var out []string
	for _, x := range people {
		if x.Role == store.RoleParent {
			out = append(out, x.Name)
		}
	}
	return out
}

func requestPictureURL(r store.Redemption) string {
	if !r.HasPicture {
		return ""
	}
	return rewardsPath + "/requests/" + r.ID + "/picture?v=" + strconv.FormatInt(r.PictureAt.Unix(), 10)
}

// The parents' rows.

type askRow struct {
	ID, Name, Shape           string
	Slot                      int
	Symbol, Picture, Reward   string
	Price, Asked              string
	Bal                       template.HTML
	Warn, Err                 string
	NoLabel, NoHint, NoButton string
	Back                      string
}

// askRows builds the open requests of the household for a parent, oldest first.
func (s *server) askRows(p principal, lang, back string, o askOutcome) ([]askRow, error) {
	open, err := s.db.OpenRedemptions(p.household.ID)
	if err != nil || len(open) == 0 {
		return nil, err
	}
	people, err := s.db.People(p.household.ID)
	if err != nil {
		return nil, err
	}
	chips := chipMap(people)
	sums, err := s.db.StarSummaries(p.household.ID)
	if err != nil {
		return nil, err
	}
	rewards, err := s.db.Rewards(p.household.ID)
	if err != nil {
		return nil, err
	}
	byID := map[string]store.Reward{}
	for _, r := range rewards {
		byID[r.ID] = r
	}
	times, err := s.db.ConfirmedRedemptions(p.household.ID)
	if err != nil {
		return nil, err
	}
	var rows []askRow
	for _, r := range open {
		c, ok := chips[r.PersonID]
		if !ok {
			continue
		}
		bal := sums[r.PersonID].Balance
		name := html.EscapeString(c.Name)
		row := askRow{ID: r.ID, Name: c.Name, Shape: c.Shape, Slot: c.Slot, Symbol: r.Symbol, Picture: requestPictureURL(r), Reward: r.Name, Back: back,
			Price: s.starsWords(lang, r.Price), Asked: s.i18n.Format(lang, i18n.Msg("rwd.ask.asked", "when", s.rwdWhen(p, lang, r.AskedAt, false))),
			Bal:     template.HTML(s.i18n.Format(lang, i18n.Msg("rwd.ask.bal", "name", name, "have", s.starsText(lang, bal), "after", negStars(bal-r.Price)))),
			Err:     o.Err[r.ID],
			NoLabel: s.i18n.Format(lang, i18n.Msg("rwd.ask.no.note", "name", c.Name)), NoHint: s.i18n.Format(lang, i18n.Msg("rwd.ask.no.hint", "name", c.Name)),
			NoButton: s.i18n.Format(lang, i18n.Msg("rwd.ask.no.button", "name", c.Name))}
		rw := byID[r.RewardID]
		switch chk := reward.CanRedeem(r.Price, bal, rw.Limit, times[r.RewardID][r.PersonID], s.now(), p.location()); chk.Reason {
		case reward.NotEnoughStars:
			row.Warn = s.i18n.Format(lang, i18n.Msg("rwd.ask.warn.stars", "name", c.Name, "have", s.starsText(lang, bal), "missing", strconv.Itoa(chk.Missing)))
		case reward.LimitReached:
			row.Warn = s.i18n.Format(lang, i18n.Msg("rwd.ask.warn.limit."+string(rw.Limit.Period), "limit", s.limitWords(lang, rw.Limit), "again", s.limitAgain(p, lang, rw.Limit.Period, chk.AvailableAgain)))
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// asksData fills the page data of Start and the rewards page for a parent.
func (s *server) asksData(r *http.Request, p principal, back string, data map[string]any) error {
	o := outcomeOf(r)
	rows, err := s.askRows(p, s.langOf(r), back, o)
	if err != nil {
		return err
	}
	data["Asks"], data["AskNotice"] = rows, o.Notice
	return nil
}

// Where a decision goes back to: Start or the rewards page.
func (s *server) goBack(w http.ResponseWriter, r *http.Request, p principal, status int) {
	if r.PostFormValue("back") == "rewards" {
		s.renderRewards(w, r, p, status)
		return
	}
	s.renderHome(w, r, status, p.person, p.household, nil)
}

func backPath(r *http.Request) string {
	if r.PostFormValue("back") == "rewards" {
		return rewardsPath
	}
	return "/"
}

func (s *server) notice(lang, key string, kv ...string) template.HTML {
	for i := 1; i < len(kv); i += 2 {
		kv[i] = html.EscapeString(kv[i])
	}
	return template.HTML(s.i18n.Format(lang, i18n.Msg(key, kv...)))
}

// decidedNotice says who decided a request and how, calmly.
func (s *server) decidedNotice(p principal, lang string, d store.Redemption, child bool) template.HTML {
	by := d.DecidedByName
	if by == "" {
		by = s.i18n.T(lang, "rwd.someone")
	}
	switch {
	case child && d.Status == store.RedemptionConfirmed:
		return s.notice(lang, "rwd.late.yes", "name", by)
	case child && d.Status == store.RedemptionDeclined && d.DeclineReason == store.DeclinedRewardGone:
		return s.notice(lang, "rwd.late.gone")
	case child && d.Status == store.RedemptionDeclined:
		return s.notice(lang, "rwd.late.no", "name", by)
	case child:
		return s.notice(lang, "rwd.late.withdrawn")
	}
	who := s.rwdPerson(p, d.PersonID)
	when := s.rwdWhen(p, lang, d.DecidedAt, false)
	switch {
	case d.Status == store.RedemptionConfirmed:
		return s.notice(lang, "rwd.decided.yes", "by", by, "reward", d.Name, "name", who, "when", when)
	case d.Status == store.RedemptionDeclined && d.DeclineReason == store.DeclinedRewardGone:
		return s.notice(lang, "rwd.decided.gone", "reward", d.Name, "name", who)
	case d.Status == store.RedemptionDeclined:
		return s.notice(lang, "rwd.decided.no", "by", by, "reward", d.Name, "name", who, "when", when)
	}
	return s.notice(lang, "rwd.decided.withdrawn", "reward", d.Name, "name", who)
}

func (s *server) rwdPerson(p principal, id string) string {
	if person, err := s.db.Person(p.household.ID, id); err == nil {
		return person.Name
	}
	return ""
}

// refusal is the sentence for a refused confirmation, at the row.
func (s *server) refusal(lang, name string, re *store.RedeemError, rw store.Redemption, p principal) string {
	if re.Check.Reason == reward.LimitReached {
		return s.i18n.Format(lang, i18n.Msg("rwd.ask.err.limit", "name", name, "reward", rw.Name, "again", s.againWords(p, lang, re.Check.AvailableAgain)))
	}
	return s.i18n.Format(lang, i18n.Msg("rwd.ask.err.stars", "name", name, "have", strconv.Itoa(re.Have), "reward", rw.Name, "price", strconv.Itoa(re.Price)))
}

// Handlers.

func (s *server) handleRequestConfirm(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	lang := s.langOf(r)
	got, err := s.db.ConfirmRedemption(p.household.ID, r.PathValue("id"), p.person.ID, s.now(), p.location())
	var re *store.RedeemError
	var de *store.DecidedError
	switch {
	case errors.Is(err, store.ErrNotFound):
		http.NotFound(w, r)
	case errors.As(err, &de):
		s.goBack(w, withOutcome(r, askOutcome{Notice: s.decidedNotice(p, lang, de.Redemption, false)}), p, http.StatusConflict)
	case errors.As(err, &re):
		cur, _ := s.db.Redemption(p.household.ID, r.PathValue("id"))
		s.goBack(w, withOutcome(r, askOutcome{Err: map[string]string{cur.ID: s.refusal(lang, s.rwdPerson(p, cur.PersonID), re, cur, p)}}), p, http.StatusConflict)
	case err != nil:
		s.fail(w, "confirm request", err)
	default:
		bal, _ := s.balanceOfPerson(p, got.PersonID)
		s.redirectSavedFields(w, r, backPath(r), "flash.rwd_redeemed", got.Name, s.rwdPerson(p, got.PersonID), s.starsText(lang, bal))
	}
}

func (s *server) balanceOfPerson(p principal, id string) (int, error) {
	acc, err := s.db.StarAccount(p.household.ID, id, 1)
	return acc.Balance, err
}

func (s *server) handleRequestDecline(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	lang := s.langOf(r)
	got, err := s.db.DeclineRedemption(p.household.ID, r.PathValue("id"), p.person.ID, r.PostFormValue("note"), s.now())
	var de *store.DecidedError
	switch {
	case errors.Is(err, store.ErrNotFound):
		http.NotFound(w, r)
	case errors.As(err, &de):
		s.goBack(w, withOutcome(r, askOutcome{Notice: s.decidedNotice(p, lang, de.Redemption, false)}), p, http.StatusConflict)
	case errors.Is(err, store.ErrInvalid):
		http.Error(w, "the note is too long", http.StatusUnprocessableEntity)
	case err != nil:
		s.fail(w, "decline request", err)
	default:
		s.redirectSavedFields(w, r, backPath(r), "flash.rwd_declined", got.Name, s.rwdPerson(p, got.PersonID))
	}
}

func (s *server) handleRewardAsk(w http.ResponseWriter, r *http.Request) {
	p, ok := s.memberPage(w, r)
	if !ok {
		return
	}
	if !isChild(p) {
		http.Error(w, http.StatusText(http.StatusForbidden), http.StatusForbidden)
		return
	}
	lang := s.langOf(r)
	got, err := s.db.AskReward(p.household.ID, p.person.ID, r.PathValue("id"), s.now(), p.location())
	var re *store.RedeemError
	switch {
	case errors.Is(err, store.ErrNotFound), errors.Is(err, store.ErrArchived):
		http.NotFound(w, r)
	case errors.As(err, &re):
		key := "rwd.ask.child.err.stars"
		if re.Check.Reason == reward.LimitReached {
			key = "rwd.ask.child.err.limit"
		}
		s.renderRewards(w, withOutcome(r, askOutcome{Notice: s.notice(lang, key)}), p, http.StatusConflict)
	case errors.Is(err, store.ErrAlreadyAsked):
		s.renderRewards(w, withOutcome(r, askOutcome{Notice: s.notice(lang, "rwd.ask.child.err.asked")}), p, http.StatusConflict)
	case err != nil:
		s.fail(w, "ask reward", err)
	default:
		people, perr := s.db.People(p.household.ID)
		if perr != nil {
			s.fail(w, "people", perr)
			return
		}
		parents := s.parentNames(people)
		key := "flash.rwd_asked"
		if len(parents) == 1 {
			key = "flash.rwd_asked_one"
		}
		s.redirectSavedFields(w, r, rewardsPath, key, got.Name, s.joinNames(lang, "rwd.or", parents))
	}
}

func (s *server) handleRequestWithdraw(w http.ResponseWriter, r *http.Request) {
	p, ok := s.memberPage(w, r)
	if !ok {
		return
	}
	if !isChild(p) {
		http.Error(w, http.StatusText(http.StatusForbidden), http.StatusForbidden)
		return
	}
	lang := s.langOf(r)
	got, err := s.db.WithdrawRedemption(p.household.ID, r.PathValue("id"), p.person.ID, s.now())
	var de *store.DecidedError
	switch {
	case errors.Is(err, store.ErrNotFound):
		http.NotFound(w, r)
	case errors.As(err, &de):
		s.renderRewards(w, withOutcome(r, askOutcome{Notice: s.decidedNotice(p, lang, de.Redemption, true)}), p, http.StatusConflict)
	case err != nil:
		s.fail(w, "withdraw request", err)
	default:
		s.redirectSavedFields(w, r, rewardsPath, "flash.rwd_withdrawn", got.Name, s.starsWords(lang, got.Price))
	}
}

func (s *server) handleRewardRedeem(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	rw, ok := s.loadReward(w, r, p)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	lang := s.langOf(r)
	show := func(status int, text string) {
		s.renderRewardForm(w, withOutcome(r, askOutcome{Redeem: true, RedeemErr: text}), p, status, &rw, s.formOfReward(rw), nil, nil)
	}
	person := r.PostFormValue("person")
	if person == "" {
		show(http.StatusUnprocessableEntity, s.i18n.T(lang, "rwd.redeem.err.person"))
		return
	}
	got, err := s.db.RedeemNow(p.household.ID, rw.ID, person, p.person.ID, s.now(), p.location())
	var re *store.RedeemError
	switch {
	case errors.Is(err, store.ErrNotFound), errors.Is(err, store.ErrArchived):
		http.NotFound(w, r)
	case errors.Is(err, store.ErrInvalid):
		show(http.StatusUnprocessableEntity, s.i18n.T(lang, "rwd.redeem.err.person"))
	case errors.As(err, &re):
		show(http.StatusConflict, s.refusal(lang, s.rwdPerson(p, person), re, store.Redemption{Name: rw.Name}, p))
	case err != nil:
		s.fail(w, "redeem reward", err)
	default:
		bal, _ := s.balanceOfPerson(p, got.PersonID)
		s.redirectSavedFields(w, r, rewardsPath+"/"+rw.ID, "flash.rwd_redeemed", got.Name, s.rwdPerson(p, got.PersonID), s.starsText(lang, bal))
	}
}

func (s *server) handleRewardSettings(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	on := r.PostFormValue("below_zero") != ""
	if err := s.db.SetBelowZero(p.household.ID, on); err != nil {
		s.fail(w, "below zero", err)
		return
	}
	key := "flash.rwd_below_off"
	if on {
		key = "flash.rwd_below_on"
	}
	s.redirectSaved(w, r, rewardsPath, key)
}

// handleRequestPicture serves the picture a reward had when it was asked for,
// to parents and to the child who asked.
func (s *server) handleRequestPicture(w http.ResponseWriter, r *http.Request) {
	p, ok := s.memberPage(w, r)
	if !ok {
		return
	}
	d, err := s.db.Redemption(p.household.ID, r.PathValue("id"))
	if err != nil || (isChild(p) && d.PersonID != p.person.ID) {
		http.NotFound(w, r)
		return
	}
	b, err := s.db.RedemptionPicture(p.household.ID, d.ID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Content-Length", strconv.Itoa(len(b)))
	w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(b)
}

// The direct redemption box on the page of a reward.

type redeemChip struct {
	ID, Name, Shape, Small string
	Slot                   int
	Disabled, Checked      bool
}

// redeemChips is one chip per child the reward is meant for, with what would
// happen to their stars, or why they cannot redeem now.
func (s *server) redeemChips(p principal, lang string, rw store.Reward, people []store.Person) ([]redeemChip, error) {
	sums, err := s.db.StarSummaries(p.household.ID)
	if err != nil {
		return nil, err
	}
	times, err := s.db.ConfirmedRedemptions(p.household.ID)
	if err != nil {
		return nil, err
	}
	open, err := s.db.OpenRequestsOfReward(p.household.ID, rw.ID)
	if err != nil {
		return nil, err
	}
	asked := map[string]bool{}
	for _, d := range open {
		asked[d.PersonID] = true
	}
	chips := chipsOf(people)
	var out []redeemChip
	for i, person := range people {
		if person.Role != store.RoleChild || !rw.MeantFor(person.ID) {
			continue
		}
		c := redeemChip{ID: person.ID, Name: person.Name, Shape: chips[i].Shape, Slot: chips[i].Slot}
		bal := sums[person.ID].Balance
		switch chk := reward.CanRedeem(rw.Price, bal, rw.Limit, times[rw.ID][person.ID], s.now(), p.location()); chk.Reason {
		case reward.NotEnoughStars:
			c.Disabled = true
			c.Small = s.i18n.Format(lang, i18n.Msg("rwd.redeem.chip.stars", "have", s.starsText(lang, bal), "missing", strconv.Itoa(chk.Missing)))
		case reward.LimitReached:
			c.Disabled = true
			c.Small = s.i18n.Format(lang, i18n.Msg("rwd.redeem.chip.limit."+string(rw.Limit.Period), "again", datefmt.Short(p.household.DateFormat, chk.AvailableAgain.In(p.location()))))
		default:
			c.Small = s.i18n.Format(lang, i18n.Msg("rwd.redeem.chip.ok", "have", s.starsText(lang, bal), "after", negStars(bal-rw.Price)))
			if asked[person.ID] {
				c.Small += s.i18n.T(lang, "rwd.redeem.chip.asked")
			}
		}
		out = append(out, c)
	}
	return out, nil
}

// askGone says what archiving or deleting a reward does to its open requests; "" when there are none.
func (s *server) askGone(p principal, lang string, rw store.Reward) (template.HTML, error) {
	open, err := s.db.OpenRequestsOfReward(p.household.ID, rw.ID)
	if err != nil || len(open) == 0 {
		return "", err
	}
	var names []string
	for _, d := range open {
		names = append(names, html.EscapeString(s.rwdPerson(p, d.PersonID)))
	}
	key := "rwd.gone.other"
	if len(names) == 1 {
		key = "rwd.gone.one"
	}
	return template.HTML(s.i18n.Format(lang, i18n.Msg(key, "names", s.joinNames(lang, "rwd.and", names)))), nil
}
