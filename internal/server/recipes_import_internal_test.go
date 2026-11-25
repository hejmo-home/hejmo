package server

import (
	"testing"

	"github.com/hejmo-home/hejmo/internal/outbound"
	"github.com/hejmo-home/hejmo/internal/picture"
	"github.com/hejmo-home/hejmo/internal/recipeimport"
)

// The picture of a page is fetched under the rule of the page: a number address is refused unless the page's
// own fetcher allows it (tests only), whatever the box's picture fetcher says (ADR 0021).
func TestThePictureFetcherOfAPageRefusesNumberAddressesLikeThePage(t *testing.T) {
	gate := outbound.AllowAll()
	for _, c := range []struct {
		name         string
		allowLiteral bool
		picsNoIP     bool
		want         bool
	}{
		{"production: page refuses numbers, pictures do not say so", false, false, true},
		{"production: both refuse", false, true, true},
		{"tests: the page allows numbers", true, false, false},
		{"the pictures refuse on their own", true, true, true},
	} {
		s := &server{gate: gate, fetcher: &picture.Fetcher{NoIPLiteral: c.picsNoIP}, recipes: &recipeimport.Fetcher{AllowIPLiteral: c.allowLiteral}}
		pf := s.pageFetcher()
		if pf.NoIPLiteral != c.want {
			t.Errorf("%s: NoIPLiteral = %v, want %v", c.name, pf.NoIPLiteral, c.want)
		}
		if pf.Service != outbound.Recipes || pf.Gate != gate {
			t.Errorf("%s: the picture is not the connection recipes through the box's gate", c.name)
		}
	}
}
