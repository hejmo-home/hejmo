package server_test

import (
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/auth"
	"github.com/hejmo-home/hejmo/internal/store"
)

// childSession returns a client with a session of the child Mia, created
// directly. Tests of the pairing flow itself use pairedPersonalDevice.
func childSession(t *testing.T, b *box) *box {
	t.Helper()
	h, err := b.db.HomeHousehold()
	if err != nil {
		t.Fatal(err)
	}
	mia, err := b.db.AddPerson(h.ID, store.Person{Name: "Mia", Role: store.RoleChild, Color: "#9A7420", Shape: "square-empty"})
	if err != nil {
		t.Fatal(err)
	}
	token := auth.NewToken()
	if err := b.db.CreateSession(auth.HashToken(token), mia.ID, "", b.now, b.now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	c := *b
	c.client = b.trustingClient()
	u, _ := url.Parse(b.srv.URL)
	c.client.Jar.SetCookies(u, []*http.Cookie{{Name: "hejmo_session", Value: token, Path: "/"}})
	return &c
}
