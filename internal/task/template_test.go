package task_test

import (
	"errors"
	"testing"

	"github.com/hejmo-home/hejmo/internal/task"
)

func TestShippedTemplatesAreValidAndMatchTheIssue(t *testing.T) {
	want := map[string]struct {
		part  task.PartOfDay
		steps int
	}{
		"school-morning":   {task.Morning, 5},
		"daycare-morning":  {task.Morning, 5},
		"school-afternoon": {task.AfterSchool, 4},
		"evening":          {task.Evening, 5},
	}
	if len(task.Shipped) != len(want) {
		t.Fatalf("%d shipped templates, want %d", len(task.Shipped), len(want))
	}
	seen := map[string]bool{}
	for _, tpl := range task.Shipped {
		w, ok := want[tpl.ID]
		if !ok || seen[tpl.ID] {
			t.Fatalf("unexpected or repeated shipped template %q", tpl.ID)
		}
		seen[tpl.ID] = true
		if tpl.Part != w.part || len(tpl.Steps) != w.steps {
			t.Errorf("%s: part %q with %d steps, want %q with %d", tpl.ID, tpl.Part, len(tpl.Steps), w.part, w.steps)
		}
		keys := map[string]bool{}
		for _, st := range tpl.Steps {
			if st.Key == "" || keys[st.Key] || st.Emoji == "" {
				t.Errorf("%s: step %+v needs a unique key and an emoji", tpl.ID, st)
			}
			keys[st.Key] = true
			step := task.TemplateStep{Title: "x", Emoji: st.Emoji, Stars: st.Stars, Repeat: st.Repeat(), Weekdays: st.Weekdays}
			if err := step.Validate(); err != nil {
				t.Errorf("%s/%s: %v", tpl.ID, st.Key, err)
			}
		}
	}
}

// The evening template ends with a story that earns no stars; the afternoon
// template is for school days only.
func TestShippedTemplatesKeepTheirSpecialSteps(t *testing.T) {
	byID := map[string]task.ShippedTemplate{}
	for _, tpl := range task.Shipped {
		byID[tpl.ID] = tpl
	}
	ev := byID["evening"].Steps
	if last := ev[len(ev)-1]; last.Stars != 0 || last.Weekdays != nil {
		t.Errorf("last evening step = %+v, want 0 stars, every day", last)
	}
	for _, st := range byID["school-afternoon"].Steps {
		if len(st.Weekdays) != 5 {
			t.Errorf("afternoon step %s weekdays = %v, want the five school days", st.Key, st.Weekdays)
		}
	}
}

func TestTemplateStepValidation(t *testing.T) {
	ok := task.TemplateStep{Title: "Duschen", Emoji: "🚿", Stars: 1, Repeat: task.Weekly, Weekdays: []string{"MO", "WE"}}
	if err := ok.Validate(); err != nil {
		t.Fatalf("valid step: %v", err)
	}
	for name, mod := range map[string]func(*task.TemplateStep){
		"no title":        func(s *task.TemplateStep) { s.Title = " " },
		"negative stars":  func(s *task.TemplateStep) { s.Stars = -1 },
		"once":            func(s *task.TemplateStep) { s.Repeat, s.Weekdays = task.Once, nil },
		"weekly no days":  func(s *task.TemplateStep) { s.Weekdays = nil },
		"daily with days": func(s *task.TemplateStep) { s.Repeat = task.Daily },
	} {
		s := ok
		mod(&s)
		var bad *task.InvalidError
		if err := s.Validate(); !errors.Is(err, task.ErrInvalid) || !errors.As(err, &bad) {
			t.Errorf("%s: err = %v, want an InvalidError", name, err)
		}
	}
}

func TestTemplateValidation(t *testing.T) {
	ok := task.RoutineTemplate{Name: "Abend", Part: task.Evening}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	for name, tpl := range map[string]task.RoutineTemplate{
		"no name":  {Part: task.Evening},
		"any time": {Name: "x", Part: task.AnyTime},
		"too long": {Name: string(make([]rune, 61)), Part: task.Morning},
	} {
		if err := tpl.Validate(); !errors.Is(err, task.ErrInvalid) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}
