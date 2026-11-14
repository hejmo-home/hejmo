package region_test

import (
	"testing"

	"github.com/hejmo-home/hejmo/internal/region"
)

func TestDACHRegions(t *testing.T) {
	all := region.All()
	count := map[string]int{}
	for _, r := range all {
		count[r.Country]++
	}
	if count["DE"] != 16 || count["AT"] != 9 || count["CH"] != 26 {
		t.Fatalf("regions per country = %v, want DE 16, AT 9, CH 26", count)
	}
	for code, name := range map[string]string{"DE-BY": "Bayern", "AT-9": "Wien", "CH-ZH": "Zürich"} {
		r, ok := region.Lookup(code)
		if !ok || r.Name != name {
			t.Errorf("Lookup(%s) = %+v, %v", code, r, ok)
		}
	}
	if _, ok := region.Lookup("XX-YY"); ok {
		t.Error("unknown region found")
	}
}
