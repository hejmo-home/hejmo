package server

import (
	"errors"

	"github.com/hejmo-home/hejmo/internal/i18n"
	"github.com/hejmo-home/hejmo/internal/store"
)

// shopAuto puts the ingredients of the meals of the next days on the list
// (ticket 285, ADR 0021), when the household chose "automatic". It is the same
// work as the tap of "Zutaten auf die Liste" for those days: the one adding
// rule, pantry articles left out, and what each meal contributed recorded. A
// meal that is on a list already is never added twice, only portions that are
// missing; the runner calls it once a day, after the rules, and not during the
// holiday.
func (a *AlwaysRunner) shopAuto(h store.Household) error {
	set, err := a.opts.DB.MealShopAuto(h.ID)
	if err != nil || !set.On {
		return err
	}
	c, err := a.s.itemContext(principal{household: h}, i18n.Default)
	if err != nil {
		return err
	}
	pg, err := c.buildShop(shopIn{Sel: shopSelection{Week: true, Days: set.Days}, ListID: set.ListID})
	if err != nil {
		return err
	}
	if pg.ListID == "" || pg.Ticked == 0 {
		return nil // no list to put it on, or nothing to add
	}
	if _, err := c.addShop(pg); err != nil && !errors.Is(err, store.ErrShopStale) {
		return err
	}
	return nil
}
