package store_test

import (
	"bytes"
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/store"
)

func TestPublicHTTPSRoundTripAndRenewalState(t *testing.T) {
	db, _ := openTemp(t)
	if _, ok, err := db.PublicHTTPS(); err != nil || ok {
		t.Fatalf("empty box: ok=%v err=%v", ok, err)
	}
	in := store.PublicHTTPS{
		Name: "mia.dedyn.io", Zone: "mia.dedyn.io", Provider: "desec", Issuer: "actalis", LANIP: "192.168.178.20",
		Email: "papa@example.org", Token: []byte("sealed-token"), EABKeyID: "kid", EABHMAC: []byte("sealed-hmac"),
	}
	if err := db.SavePublicHTTPS(in); err != nil {
		t.Fatal(err)
	}
	got, ok, err := db.PublicHTTPS()
	if err != nil || !ok {
		t.Fatalf("read back: ok=%v err=%v", ok, err)
	}
	if got.Name != in.Name || got.Issuer != "actalis" || !bytes.Equal(got.Token, in.Token) || got.EABKeyID != "kid" || got.LANIP != in.LANIP {
		t.Errorf("got %+v", got)
	}

	at := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	if err := db.SetRenewalResult(at, "acme: rate limited"); err != nil {
		t.Fatal(err)
	}
	got, _, _ = db.PublicHTTPS()
	if got.RenewalError != "acme: rate limited" || !got.RenewalFailedAt.Equal(at) {
		t.Errorf("failure not stored: %+v", got)
	}
	if err := db.SetRenewalResult(at, ""); err != nil {
		t.Fatal(err)
	}
	got, _, _ = db.PublicHTTPS()
	if got.RenewalError != "" || !got.RenewalFailedAt.IsZero() {
		t.Errorf("success did not clear the failure: %+v", got)
	}

	// Saving again replaces the single configuration.
	in.Name = "papa.dedyn.io"
	if err := db.SavePublicHTTPS(in); err != nil {
		t.Fatal(err)
	}
	if got, _, _ := db.PublicHTTPS(); got.Name != "papa.dedyn.io" {
		t.Errorf("name = %q", got.Name)
	}
}

func TestSetRenewalResultWithoutConfigurationIsHarmless(t *testing.T) {
	db, _ := openTemp(t)
	if err := db.SetRenewalResult(time.Now(), "x"); err != nil {
		t.Fatal(err)
	}
}
