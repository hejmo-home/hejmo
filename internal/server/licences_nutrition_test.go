package server_test

import (
	"net/http"
	"strings"
	"testing"
)

// Ticket 288, acceptance: license and availability of BLS 4.0 checked before
// shipping; credits file updated. CC BY 4.0 needs the publisher named, the
// licence and the changes; the licences page and the NOTICE file carry them.

func TestTheLicencesPageHasCardsForBLSAndUSDA(t *testing.T) {
	t.Parallel()
	b := signedInBox(t)
	for lang, want := range map[string][]string{
		"de": {`<li class="card" data-licence="bls">`, "<h2>Nährwerte: Bundeslebensmittelschlüssel (BLS) 4.0</h2>", "Max Rubner-Institut (MRI), Karlsruhe. Lizenz CC BY 4.0.", "Auszug, umgerechnet auf die sieben Werte der Nährwertdeklaration.",
			"DOI: 10.25826/Data20251217-134202-0", `href="https://doi.org/10.25826/Data20251217-134202-0"`, `href="https://blsdb.de"`, `href="https://creativecommons.org/licenses/by/4.0/"`,
			`<li class="card" data-licence="usda">`, "<h2>Nährwerte: USDA FoodData Central</h2>", "Gemeinfrei (CC0 1.0).", `href="https://fdc.nal.usda.gov"`, `href="https://creativecommons.org/publicdomain/zero/1.0/"`},
		"en": {"<h2>Nutrition values: German Nutrient Database (BLS) 4.0</h2>", "Max Rubner-Institut (MRI), Karlsruhe. Licence CC BY 4.0.", "Extract, reduced to the seven values of the nutrition declaration.", "Public domain (CC0 1.0)."},
	} {
		body := b.do(http.MethodGet, "/settings/licences", nil, map[string]string{"Accept-Language": lang}).body
		for _, w := range want {
			if !strings.Contains(body, w) {
				t.Errorf("%s: licences page lacks %q", lang, w)
			}
		}
		if !(strings.Index(body, `data-licence="bls"`) < strings.Index(body, `data-licence="usda"`) && strings.Index(body, `data-licence="usda"`) < strings.Index(body, `data-licence="geonames"`)) {
			t.Errorf("%s: the cards are BLS, USDA, then the rest", lang)
		}
	}
}
