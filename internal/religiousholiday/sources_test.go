package religiousholiday_test

import (
	"reflect"
	"testing"

	rh "github.com/hejmo-home/hejmo/internal/religiousholiday"
)

// Ticket 377: the sources a religion's days come from, worked out from the days it produces.
func TestSourcesOfNamesTheDistinctSourcesOfTheDaysAReligionProducesInOrderOfFirstAppearance(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		r    rh.Religion
		o    rh.Options
		want []string
	}{
		{rh.Christian, rh.Options{}, []string{rh.SrcChurch}},
		{rh.Orthodox, rh.Options{}, []string{rh.SrcOrthodox}},
		{rh.Orthodox, rh.Options{Orthodox: rh.Julian}, []string{rh.SrcOrthodox}},
		{rh.Jewish, rh.Options{}, []string{rh.SrcHebcal}},
		{rh.Muslim, rh.Options{}, []string{rh.SrcDiyanet}},
		{rh.Muslim, rh.Options{Islamic: rh.UmmAlQura}, []string{rh.SrcUmmQura}},
		{rh.Alevi, rh.Options{}, []string{rh.SrcDiyanet, rh.SrcAABF}},
		{rh.Alevi, rh.Options{Islamic: rh.UmmAlQura}, []string{rh.SrcUmmQura, rh.SrcAABF}},
		{rh.Bahai, rh.Options{}, []string{rh.SrcBahai}},
		{rh.Buddhist, rh.Options{}, []string{rh.SrcMoon, rh.SrcFixed}}, // Losar has no table yet, so it drops out
		{rh.Hindu, rh.Options{}, []string{rh.SrcPanchang}},             // no place of the household: a stand-in is used
		{rh.Sikh, rh.Options{}, []string{rh.SrcSGPC}},
	} {
		if got := rh.SourcesOf(c.r, c.o); !reflect.DeepEqual(got, c.want) {
			t.Errorf("SourcesOf(%s, %+v) = %v, want %v", c.r, c.o, got, c.want)
		}
	}
}

func TestEveryReligionHasAtLeastOneSource(t *testing.T) {
	t.Parallel()
	for _, r := range rh.Religions() {
		if len(rh.SourcesOf(r, rh.Options{})) == 0 {
			t.Errorf("%s has no source", r)
		}
	}
}
