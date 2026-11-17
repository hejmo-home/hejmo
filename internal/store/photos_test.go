package store_test

import (
	"errors"
	"testing"

	"github.com/hejmo-home/hejmo/internal/store"
)

const gb = int64(1) << 30

func photoBox(t *testing.T) (*store.DB, store.Household, store.Album) {
	t.Helper()
	db, h := householdForDisplays(t)
	a, err := db.EnsureDefaultAlbum(h.ID, "Unsere Fotos")
	if err != nil {
		t.Fatal(err)
	}
	return db, h, a
}

func addPhoto(t *testing.T, db *store.DB, h store.Household, a store.Album, name string, bytes int64) store.Photo {
	t.Helper()
	p, err := db.AddPhoto(h.ID, store.Photo{AlbumID: a.ID, Name: name, Bytes: bytes, Width: 1600, Height: 1200})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func savedDisplay(t *testing.T, db *store.DB, h store.Household, name string, typ store.DisplayType) store.Display {
	t.Helper()
	d, err := db.OpenPairing(h.ID, "hash-"+name, pairT0)
	if err != nil {
		t.Fatal(err)
	}
	d, err = db.ConfirmPairing(h.ID, d.Code, store.Display{Name: name, Type: typ}, pairT0)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestTheDefaultAlbumExistsOnceAndIsNotAFolderAlbum(t *testing.T) {
	db, h, a := photoBox(t)
	again, err := db.EnsureDefaultAlbum(h.ID, "Other name")
	if err != nil || again.ID != a.ID || again.Name != "Unsere Fotos" {
		t.Fatalf("second call = %+v, %v; want the same album", again, err)
	}
	if !a.Default || a.Folder != "" {
		t.Errorf("album = %+v, want the default album, not from a folder", a)
	}
	if err := db.DeleteAlbum(h.ID, a.ID); !errors.Is(err, store.ErrInvalid) {
		t.Errorf("deleting the default album = %v, want ErrInvalid", err)
	}
}

func TestAlbumNamesAreUniquePerHouseholdIgnoringCase(t *testing.T) {
	db, h, _ := photoBox(t)
	if _, err := db.CreateAlbum(h.ID, "  Oma und Opa "); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"oma und opa", "Unsere Fotos"} {
		var fe *store.FieldError
		if _, err := db.CreateAlbum(h.ID, name); !errors.As(err, &fe) || fe.Field != "album_name" {
			t.Errorf("CreateAlbum(%q) = %v, want a field error on album_name", name, err)
		}
	}
	for _, name := range []string{"", "   ", string(make([]byte, 81))} {
		if _, err := db.CreateAlbum(h.ID, name); !errors.Is(err, store.ErrInvalid) {
			t.Errorf("CreateAlbum(%q) = %v, want ErrInvalid", name, err)
		}
	}
}

func TestAlbumsListWithCountsAndCover(t *testing.T) {
	db, h, a := photoBox(t)
	p1 := addPhoto(t, db, h, a, "a.jpg", 100)
	p2 := addPhoto(t, db, h, a, "b.jpg", 200)
	if err := db.SetPhotoHidden(h.ID, p2.ID, true); err != nil {
		t.Fatal(err)
	}
	empty, _ := db.CreateAlbum(h.ID, "Leer")
	list, err := db.Albums(h.ID)
	if err != nil || len(list) != 2 {
		t.Fatalf("Albums = %+v, %v", list, err)
	}
	if list[0].ID != a.ID || list[0].Photos != 2 || list[0].Hidden != 1 {
		t.Errorf("first album = %+v, want 2 photos of which 1 hidden", list[0])
	}
	if list[0].Cover != p1.ID {
		t.Errorf("cover = %q, want the newest visible photo %q", list[0].Cover, p1.ID)
	}
	if list[1].ID != empty.ID || list[1].Photos != 0 || list[1].Cover != "" {
		t.Errorf("empty album = %+v", list[1])
	}
}

func TestPhotosAreNewestFirstAndHideable(t *testing.T) {
	db, h, a := photoBox(t)
	first := addPhoto(t, db, h, a, "a.jpg", 1)
	second := addPhoto(t, db, h, a, "b.jpg", 1)
	got, err := db.Photos(h.ID, a.ID)
	if err != nil || len(got) != 2 || got[0].ID != second.ID || got[1].ID != first.ID {
		t.Fatalf("Photos = %+v, %v; want newest first", got, err)
	}
	if err := db.SetPhotoHidden(h.ID, first.ID, true); err != nil {
		t.Fatal(err)
	}
	p, _ := db.Photo(h.ID, first.ID)
	if !p.Hidden {
		t.Error("photo is not hidden")
	}
	if err := db.SetPhotoHidden(h.ID, "no-such-photo", true); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("hiding an unknown photo = %v, want ErrNotFound", err)
	}
}

func TestPhotosBelongToOneHousehold(t *testing.T) {
	db, h, a := photoBox(t)
	p := addPhoto(t, db, h, a, "a.jpg", 1)
	other, _ := db.CreateHousehold(store.Household{Name: "Other"})
	if _, err := db.Photo(other.ID, p.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("another household reads the photo: %v", err)
	}
	if _, err := db.AddPhoto(other.ID, store.Photo{AlbumID: a.ID, Name: "x.jpg", Bytes: 1}); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("adding to another household's album = %v, want ErrNotFound", err)
	}
}

func TestMovingPhotosKeepsEachInExactlyOneAlbum(t *testing.T) {
	db, h, a := photoBox(t)
	b, _ := db.CreateAlbum(h.ID, "Urlaub")
	p1, p2, p3 := addPhoto(t, db, h, a, "1.jpg", 1), addPhoto(t, db, h, a, "2.jpg", 1), addPhoto(t, db, h, a, "3.jpg", 1)
	if err := db.MovePhotos(h.ID, []string{p1.ID, p3.ID}, b.ID); err != nil {
		t.Fatal(err)
	}
	inA, _ := db.Photos(h.ID, a.ID)
	inB, _ := db.Photos(h.ID, b.ID)
	if len(inA) != 1 || inA[0].ID != p2.ID || len(inB) != 2 {
		t.Errorf("after the move A holds %d and B %d photos", len(inA), len(inB))
	}
	if err := db.MovePhotos(h.ID, []string{p2.ID}, "no-such-album"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("moving to an unknown album = %v, want ErrNotFound", err)
	}
}

func TestDeletingPhotosReturnsThemSoTheirFilesCanGo(t *testing.T) {
	db, h, a := photoBox(t)
	p1, p2 := addPhoto(t, db, h, a, "1.jpg", 10), addPhoto(t, db, h, a, "2.jpg", 20)
	gone, err := db.DeletePhotos(h.ID, []string{p1.ID, "unknown"})
	if err != nil || len(gone) != 1 || gone[0] != p1.ID {
		t.Fatalf("DeletePhotos = %v, %v; want only %s", gone, err, p1.ID)
	}
	if rest, _ := db.Photos(h.ID, a.ID); len(rest) != 1 || rest[0].ID != p2.ID {
		t.Errorf("remaining = %+v", rest)
	}
}

func TestDeletingAnAlbumDeletesItsPhotosAndReturnsThem(t *testing.T) {
	db, h, _ := photoBox(t)
	b, _ := db.CreateAlbum(h.ID, "Urlaub")
	p := addPhoto(t, db, h, b, "1.jpg", 10)
	ids, err := db.AlbumPhotoIDs(h.ID, b.ID)
	if err != nil || len(ids) != 1 || ids[0] != p.ID {
		t.Fatalf("AlbumPhotoIDs = %v, %v", ids, err)
	}
	if err := db.DeleteAlbum(h.ID, b.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Photo(h.ID, p.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("photo of the deleted album: %v", err)
	}
}

func TestUsageCountsStoredBytesAndTheLimitRefusesNewPhotos(t *testing.T) {
	db, h, a := photoBox(t)
	if lim, _ := db.PhotoLimitGB(h.ID); lim != 2 {
		t.Fatalf("default limit = %d GB, want 2", lim)
	}
	addPhoto(t, db, h, a, "1.jpg", 2*gb-100)
	used, n, err := db.PhotoUsage(h.ID)
	if err != nil || used != 2*gb-100 || n != 1 {
		t.Fatalf("usage = %d bytes in %d photos, %v", used, n, err)
	}
	if _, err := db.AddPhoto(h.ID, store.Photo{AlbumID: a.ID, Name: "2.jpg", Bytes: 101}); !errors.Is(err, store.ErrPhotosFull) {
		t.Errorf("a photo over the limit = %v, want ErrPhotosFull", err)
	}
	if _, err := db.AddPhoto(h.ID, store.Photo{AlbumID: a.ID, Name: "3.jpg", Bytes: 100}); err != nil {
		t.Errorf("a photo that just fits: %v", err)
	}
	if err := db.SetPhotoLimitGB(h.ID, 3); err != nil {
		t.Fatal(err)
	}
	if _, err := db.AddPhoto(h.ID, store.Photo{AlbumID: a.ID, Name: "4.jpg", Bytes: 1000}); err != nil {
		t.Errorf("after raising the limit: %v", err)
	}
	for _, bad := range []int{0, -1, 100001} {
		if err := db.SetPhotoLimitGB(h.ID, bad); !errors.Is(err, store.ErrInvalid) {
			t.Errorf("SetPhotoLimitGB(%d) = %v, want ErrInvalid", bad, err)
		}
	}
}

func TestFolderPhotosAreRememberedBySourcePath(t *testing.T) {
	db, h, _ := photoBox(t)
	fa, err := db.EnsureFolderAlbum(h.ID, "Weihnachten 2025")
	if err != nil || fa.Folder != "Weihnachten 2025" || fa.Name != "Weihnachten 2025" {
		t.Fatalf("folder album = %+v, %v", fa, err)
	}
	again, _ := db.EnsureFolderAlbum(h.ID, "Weihnachten 2025")
	if again.ID != fa.ID {
		t.Error("a second call made a second album")
	}
	if _, err := db.AddPhoto(h.ID, store.Photo{AlbumID: fa.ID, Name: "a.jpg", Bytes: 5, SourcePath: "Weihnachten 2025/a.jpg", SourceStamp: "5:1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.AddPhoto(h.ID, store.Photo{AlbumID: fa.ID, Name: "a.jpg", Bytes: 5, SourcePath: "Weihnachten 2025/a.jpg", SourceStamp: "5:1"}); !errors.Is(err, store.ErrInvalid) {
		t.Errorf("the same source twice = %v, want ErrInvalid", err)
	}
	known, err := db.FolderPhotos(h.ID)
	if err != nil || len(known) != 1 || known["Weihnachten 2025/a.jpg"].SourceStamp != "5:1" {
		t.Errorf("FolderPhotos = %+v, %v", known, err)
	}
	if err := db.RenameAlbum(h.ID, fa.ID, "Neu"); !errors.Is(err, store.ErrInvalid) {
		t.Errorf("renaming a folder album = %v, want ErrInvalid (it is changed in the folder)", err)
	}
	if err := db.DeleteAlbum(h.ID, fa.ID); !errors.Is(err, store.ErrInvalid) {
		t.Errorf("deleting a folder album = %v, want ErrInvalid", err)
	}
	if err := db.MovePhotos(h.ID, []string{known["Weihnachten 2025/a.jpg"].ID}, fa.ID); !errors.Is(err, store.ErrInvalid) {
		t.Errorf("moving a folder photo = %v, want ErrInvalid", err)
	}
}

func TestRemovingFolderPhotosAndEmptyFolderAlbums(t *testing.T) {
	db, h, _ := photoBox(t)
	fa, _ := db.EnsureFolderAlbum(h.ID, "Sommerfest")
	p, _ := db.AddPhoto(h.ID, store.Photo{AlbumID: fa.ID, Name: "a.jpg", Bytes: 5, SourcePath: "Sommerfest/a.jpg"})
	gone, err := db.RemoveFolderPhotos(h.ID, []string{p.ID})
	if err != nil || len(gone) != 1 {
		t.Fatalf("RemoveFolderPhotos = %v, %v", gone, err)
	}
	if err := db.RemoveFolderAlbumsNotIn(h.ID, nil); err != nil {
		t.Fatal(err)
	}
	if list, _ := db.Albums(h.ID); len(list) != 1 {
		t.Errorf("albums = %+v, want only the default album", list)
	}
}

func TestANewDisplayShowsEveryAlbumAndTheScreensaverIsOn(t *testing.T) {
	db, h, _ := photoBox(t)
	kitchen := savedDisplay(t, db, h, "Küche", store.DisplayScreen)
	hall := savedDisplay(t, db, h, "Flur", store.DisplayEInkBW)
	if s := kitchen.Saver; !s.Enabled || s.IdleMinutes != 10 || s.IntervalMinutes != 1 || s.Albums != nil {
		t.Errorf("kitchen saver = %+v, want on, 10 and 1 minutes, all albums", s)
	}
	if hall.Saver.IntervalMinutes != 15 {
		t.Errorf("E-Ink interval = %d, want 15", hall.Saver.IntervalMinutes)
	}
}

func TestSaverSettingsAreSavedAndValidated(t *testing.T) {
	db, h, a := photoBox(t)
	b, _ := db.CreateAlbum(h.ID, "Urlaub")
	d := savedDisplay(t, db, h, "Küche", store.DisplayScreen)
	want := store.Saver{Enabled: false, IdleMinutes: 5, IntervalMinutes: 3, Albums: []string{b.ID}}
	if err := db.SetDisplaySaver(h.ID, d.ID, want); err != nil {
		t.Fatal(err)
	}
	got := mustDisplay(t, db, h, d.ID).Saver
	if got.Enabled || got.IdleMinutes != 5 || got.IntervalMinutes != 3 || len(got.Albums) != 1 || got.Albums[0] != b.ID {
		t.Errorf("saved = %+v, want %+v", got, want)
	}
	if err := db.SetDisplaySaver(h.ID, d.ID, store.Saver{Enabled: true, IdleMinutes: 10, IntervalMinutes: 1, Albums: []string{a.ID, b.ID}}); err != nil {
		t.Fatal(err)
	}
	if err := db.SetDisplaySaver(h.ID, d.ID, store.Saver{Enabled: true, IdleMinutes: 10, IntervalMinutes: 1}); err != nil {
		t.Fatal(err)
	}
	if s := mustDisplay(t, db, h, d.ID).Saver; s.Albums != nil {
		t.Errorf("no albums chosen = %v, want nil (all albums)", s.Albums)
	}
	for name, bad := range map[string]store.Saver{
		"idle 0":        {Enabled: true, IdleMinutes: 0, IntervalMinutes: 1},
		"idle 241":      {Enabled: true, IdleMinutes: 241, IntervalMinutes: 1},
		"interval 0":    {Enabled: true, IdleMinutes: 10, IntervalMinutes: 0},
		"interval 241":  {Enabled: true, IdleMinutes: 10, IntervalMinutes: 241},
		"unknown album": {Enabled: true, IdleMinutes: 10, IntervalMinutes: 1, Albums: []string{"nope"}},
	} {
		if err := db.SetDisplaySaver(h.ID, d.ID, bad); !errors.Is(err, store.ErrInvalid) {
			t.Errorf("%s: err = %v, want ErrInvalid", name, err)
		}
	}
}

func mustDisplay(t *testing.T, db *store.DB, h store.Household, id string) store.Display {
	t.Helper()
	list, err := db.Displays(h.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range list {
		if d.ID == id {
			return d
		}
	}
	t.Fatalf("display %s not found", id)
	return store.Display{}
}

func TestDeletingTheOnlyChosenAlbumSendsADisplayBackToAllAlbums(t *testing.T) {
	db, h, _ := photoBox(t)
	b, _ := db.CreateAlbum(h.ID, "Urlaub")
	d := savedDisplay(t, db, h, "Küche", store.DisplayScreen)
	if err := db.SetDisplaySaver(h.ID, d.ID, store.Saver{Enabled: true, IdleMinutes: 10, IntervalMinutes: 1, Albums: []string{b.ID}}); err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteAlbum(h.ID, b.ID); err != nil {
		t.Fatal(err)
	}
	if s := mustDisplay(t, db, h, d.ID).Saver; s.Albums != nil {
		t.Errorf("albums after the delete = %v, want nil (all albums)", s.Albums)
	}
}

func TestSaverPhotosFollowTheDisplaysAlbumsAndSkipHiddenPhotos(t *testing.T) {
	db, h, a := photoBox(t)
	b, _ := db.CreateAlbum(h.ID, "Urlaub")
	pa := addPhoto(t, db, h, a, "a.jpg", 1)
	pb := addPhoto(t, db, h, b, "b.jpg", 1)
	hidden := addPhoto(t, db, h, b, "c.jpg", 1)
	_ = db.SetPhotoHidden(h.ID, hidden.ID, true)
	d := savedDisplay(t, db, h, "Küche", store.DisplayScreen)

	ids := func(ps []store.Photo) map[string]bool {
		m := map[string]bool{}
		for _, p := range ps {
			m[p.ID] = true
		}
		return m
	}
	all, err := db.SaverPhotos(h.ID, d)
	if err != nil || len(all) != 2 || !ids(all)[pa.ID] || !ids(all)[pb.ID] {
		t.Fatalf("all albums: %+v, %v; want a and b without the hidden one", all, err)
	}
	d.Saver.Albums = []string{b.ID}
	only, _ := db.SaverPhotos(h.ID, d)
	if len(only) != 1 || only[0].ID != pb.ID {
		t.Errorf("album Urlaub only: %+v", only)
	}
}

func TestAlbumsShownOnADisplay(t *testing.T) {
	db, h, a := photoBox(t)
	b, _ := db.CreateAlbum(h.ID, "Urlaub")
	_ = savedDisplay(t, db, h, "Küche", store.DisplayScreen)
	own := savedDisplay(t, db, h, "Flur", store.DisplayScreen)
	_ = db.SetDisplaySaver(h.ID, own.ID, store.Saver{Enabled: true, IdleMinutes: 10, IntervalMinutes: 1, Albums: []string{b.ID}})
	off := savedDisplay(t, db, h, "Bad", store.DisplayScreen)
	_ = db.SetDisplaySaver(h.ID, off.ID, store.Saver{Enabled: false, IdleMinutes: 10, IntervalMinutes: 1})
	names := func(a store.Album) []string {
		var out []string
		list, _ := db.Displays(h.ID)
		for _, d := range list {
			if d.Saver.Shows(a.ID) {
				out = append(out, d.Name)
			}
		}
		return out
	}
	if got := names(a); len(got) != 1 || got[0] != "Küche" {
		t.Errorf("album Unsere Fotos is shown on %v, want Küche only (Flur chose another album, Bad has it off)", got)
	}
	if got := names(b); len(got) != 2 {
		t.Errorf("album Urlaub is shown on %v, want Küche and Flur", got)
	}
}
