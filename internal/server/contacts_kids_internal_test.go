package server

import (
	"testing"

	"github.com/hejmo-home/hejmo/internal/contact"
	"github.com/hejmo-home/hejmo/internal/store"
)

func TestKidsFlashSaysTheChildrenNoLongerSeeAContactOnlyIfTheyDidSee(t *testing.T) {
	t.Parallel()
	num := []contact.Line{{Value: "0171 3456 789"}}
	shown := &store.Contact{ForChildren: true, Numbers: num}
	waiting := &store.Contact{ForChildren: true}
	if got := kidsFlash(shown, store.Contact{Numbers: num}, false); got != "flash.contact_saved_kids_off" {
		t.Errorf("shown, now off = %q, want the off line", got)
	}
	if got := kidsFlash(waiting, store.Contact{}, false); got != "" {
		t.Errorf("waiting for a number, now off = %q, want no line: the children never saw the contact", got)
	}
}
