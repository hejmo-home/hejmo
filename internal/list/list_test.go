package list_test

import (
	"testing"

	"github.com/hejmo-home/hejmo/internal/list"
)

func texts(es []list.Entry) []string {
	var out []string
	for _, e := range es {
		out = append(out, e.Text)
	}
	return out
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestAlphabeticalOrderIgnoresCaseAndFoldsGermanLetters(t *testing.T) {
	in := []list.Entry{{Text: "milk"}, {Text: "Äpfel"}, {Text: "Zucker"}, {Text: "Apfelsaft"}, {Text: "bread"}, {Text: "Öl"}, {Text: "Obst"}}
	got := texts(list.Order(in, list.Alphabetical))
	want := []string{"Äpfel", "Apfelsaft", "bread", "milk", "Obst", "Öl", "Zucker"}
	if !equal(got, want) {
		t.Fatalf("alphabetical = %v, want %v", got, want)
	}
}

func TestManualOrderFollowsPositionsAndKeepsInputUntouched(t *testing.T) {
	in := []list.Entry{{Text: "c", Position: 2}, {Text: "a", Position: 0}, {Text: "b", Position: 1}}
	if got := texts(list.Order(in, list.Manual)); !equal(got, []string{"a", "b", "c"}) {
		t.Fatalf("manual = %v", got)
	}
	if in[0].Text != "c" {
		t.Fatal("Order changed its input")
	}
}

func TestValidateList(t *testing.T) {
	ok := list.List{Name: "Shopping", Color: "green", Symbol: "square-filled", Sort: list.Manual}
	if err := ok.Validate(); err != nil {
		t.Fatalf("valid list: %v", err)
	}
	for field, mod := range map[string]func(*list.List){
		"name":   func(l *list.List) { l.Name = "  " },
		"color":  func(l *list.List) { l.Color = "pink" },
		"symbol": func(l *list.List) { l.Symbol = "star" },
		"sort":   func(l *list.List) { l.Sort = "random" },
	} {
		l := ok
		mod(&l)
		var bad *list.InvalidError
		if err := l.Validate(); !asInvalid(err, &bad) || bad.Field != field {
			t.Errorf("%s: err = %v", field, err)
		}
	}
}

func TestValidateEntryText(t *testing.T) {
	if err := list.ValidateText("Milk"); err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"", "   "} {
		if list.ValidateText(s) == nil {
			t.Errorf("%q accepted", s)
		}
	}
	long := make([]rune, list.MaxText+1)
	for i := range long {
		long[i] = 'x'
	}
	if list.ValidateText(string(long)) == nil {
		t.Error("overlong text accepted")
	}
}

func asInvalid(err error, target **list.InvalidError) bool {
	e, ok := err.(*list.InvalidError)
	if ok {
		*target = e
	}
	return ok
}
