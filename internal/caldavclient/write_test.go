package caldavclient_test

import (
	"context"
	"strings"
	"testing"

	"github.com/hejmo-home/hejmo/internal/caldavclient"
	"github.com/hejmo-home/hejmo/internal/caldavclient/caldavtest"
)

func TestPutCreatesThenReplacesWithIfMatchAndRefusesAStaleETag(t *testing.T) {
	srv := caldavtest.Start(t)
	cal := srv.MakeCalendar(t, "family", "Family")
	c := client(t, srv, caldavtest.Password)
	ctx := context.Background()
	obj := cal + "swim.ics"

	etag1, err := c.Put(ctx, obj, caldavtest.Event("swim@test", "Swimming", "20261120T150000", "20261120T160000"), "")
	if err != nil || etag1 == "" {
		t.Fatalf("create = %q, %v", etag1, err)
	}
	// Creating again with If-None-Match: * must fail as a conflict.
	if _, err := c.Put(ctx, obj, caldavtest.Event("swim@test", "Swimming", "20261120T150000", "20261120T160000"), ""); !caldavclient.IsConflict(err) {
		t.Fatalf("second create err = %v, want a conflict", err)
	}
	etag2, err := c.Put(ctx, obj, caldavtest.Event("swim@test", "Swim lesson", "20261120T150000", "20261120T160000"), etag1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Put(ctx, obj, caldavtest.Event("swim@test", "Stale", "20261120T150000", "20261120T160000"), etag1); !caldavclient.IsConflict(err) {
		t.Fatalf("stale put err = %v, want a conflict", err)
	}
	got, err := c.Fetch(ctx, cal, []string{obj})
	if err != nil || len(got) != 1 || !strings.Contains(string(got[0].Data), "Swim lesson") || got[0].ETag != etag2 {
		t.Fatalf("fetched = %+v, %v (want etag %q)", got, err, etag2)
	}
}

func TestDeleteHonoursIfMatchAndTreatsMissingAsGone(t *testing.T) {
	srv := caldavtest.Start(t)
	cal := srv.MakeCalendar(t, "family", "Family")
	c := client(t, srv, caldavtest.Password)
	ctx := context.Background()
	obj := cal + "swim.ics"
	etag, err := c.Put(ctx, obj, caldavtest.Event("swim@test", "Swimming", "20261120T150000", "20261120T160000"), "")
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Delete(ctx, obj, "\"other\""); !caldavclient.IsConflict(err) {
		t.Fatalf("stale delete err = %v, want a conflict", err)
	}
	if err := c.Delete(ctx, obj, etag); err != nil {
		t.Fatal(err)
	}
	if err := c.Delete(ctx, obj, ""); !caldavclient.IsNotFound(err) {
		t.Fatalf("delete of a gone object err = %v, want not found", err)
	}
}
