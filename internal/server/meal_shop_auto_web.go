package server

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/hejmo-home/hejmo/internal/i18n"
	"github.com/hejmo-home/hejmo/internal/list"
	"github.com/hejmo-home/hejmo/internal/store"
)

// Settings, Lists, "Einkaufsliste aus dem Essensplan" (ticket 285, ADR 0021 and
// 0015): by hand (default) or automatic, how many days ahead and which list.
// The same setting as "meal_shopping" of the API. The run itself is
// AlwaysRunner.shopAuto.

const shopAutoPath = "/settings/lists/meal-shopping"

// shopAutoForm is what the form shows: the stored setting, or what was typed.
type shopAutoForm struct {
	On     bool
	Days   string
	ListID string
}

type shopAutoErrs struct{ Days, List string }

// shopAutoLists are the lists the automatic run may use: those with suggestions, as the tick list offers.
func (s *server) shopAutoLists(p principal) ([]list.List, error) {
	all, err := s.db.Lists(p.household.ID)
	if err != nil {
		return nil, err
	}
	var out []list.List
	for _, l := range all {
		if l.Suggestions {
			out = append(out, l)
		}
	}
	return out, nil
}

// shopAutoState is the line under the row in Settings, Lists.
func (s *server) shopAutoState(lang string, a store.MealShopAuto) string {
	if !a.On {
		return s.i18n.T(lang, "shopauto.row.off")
	}
	key := "shopauto.row.on.other"
	if a.Days == 1 {
		key = "shopauto.row.on.one"
	}
	return s.i18n.Format(lang, i18n.Msg(key, "n", strconv.Itoa(a.Days)))
}

func (s *server) renderShopAuto(w http.ResponseWriter, r *http.Request, p principal, status int, posted *shopAutoForm, errs shopAutoErrs) {
	lang := s.langOf(r)
	a, err := s.alwaysContext(p, lang)
	if err != nil {
		s.fail(w, "always context", err)
		return
	}
	stored, err := s.db.MealShopAuto(p.household.ID)
	if err != nil {
		s.fail(w, "meal shop settings", err)
		return
	}
	lists, err := s.shopAutoLists(p)
	if err != nil {
		s.fail(w, "lists", err)
		return
	}
	// the chosen list lost its suggestions: it is not offered, so say so rather than show another choice quietly
	stale := ""
	if stored.ListID != "" {
		offered := false
		for _, l := range lists {
			offered = offered || l.ID == stored.ListID
		}
		if !offered {
			all, err := s.db.Lists(p.household.ID)
			if err != nil {
				s.fail(w, "lists", err)
				return
			}
			for _, l := range all {
				if l.ID == stored.ListID {
					stale = s.i18n.Format(lang, i18n.Msg("shopauto.stale", "name", l.Name))
				}
			}
		}
	}
	form := shopAutoForm{On: stored.On, Days: strconv.Itoa(stored.Days), ListID: stored.ListID}
	if posted != nil {
		form = *posted
	}
	s.render(w, r, status, "meal_shop_auto.html", map[string]any{
		"Person": p.person, "SyncStale": s.syncStale(p.household.ID),
		"BackHref": listsSettingsPath, "BackText": s.i18n.T(lang, "lists.set.back"),
		"Stale": stale, "Form": form, "Errs": errs, "Lists": lists, "MaxDays": store.MaxShopDays,
		"At": a.at(), "HolidayNote": a.holidayNote(""), "AlwaysHref": alwaysPath,
		"Hint": s.i18n.Format(lang, i18n.Msg("shopauto.hint", "at", a.at())),
	})
}

func (s *server) handleShopAuto(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	s.renderShopAuto(w, r, p, http.StatusOK, nil, shopAutoErrs{})
}

func (s *server) handleShopAutoSave(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	lang := s.langOf(r)
	_ = r.ParseForm()
	form := shopAutoForm{On: r.PostFormValue("auto") != "", Days: strings.TrimSpace(r.PostFormValue("days")), ListID: r.PostFormValue("list")}
	var errs shopAutoErrs
	days, err := strconv.Atoi(form.Days)
	if err != nil || days < 1 || days > store.MaxShopDays {
		errs.Days = s.i18n.Format(lang, i18n.Msg("shopauto.err.days", "max", strconv.Itoa(store.MaxShopDays)))
	}
	lists, err := s.shopAutoLists(p)
	if err != nil {
		s.fail(w, "lists", err)
		return
	}
	if form.ListID != "" {
		found := false
		for _, l := range lists {
			found = found || l.ID == form.ListID
		}
		if !found {
			errs.List = s.i18n.T(lang, "shopauto.err.list")
		}
	}
	if errs != (shopAutoErrs{}) {
		s.renderShopAuto(w, r, p, http.StatusUnprocessableEntity, &form, errs)
		return
	}
	if err := s.db.SetMealShopAuto(p.household.ID, store.MealShopAuto{On: form.On, Days: days, ListID: form.ListID, Lang: lang}); err != nil {
		s.fail(w, "save meal shop settings", err)
		return
	}
	key := "flash.shop_auto_off"
	if form.On {
		key = "flash.shop_auto_on"
	}
	s.redirectSaved(w, r, shopAutoPath, key)
}
