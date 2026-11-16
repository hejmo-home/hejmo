package photo_test

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/hejmo-home/hejmo/internal/photo"
)

func TestFilesStoreAndRemoveAPhotoAndItsThumbnail(t *testing.T) {
	f := photo.NewFiles(filepath.Join(t.TempDir(), "photos"))
	if err := f.Put("abc", []byte("big"), []byte("small")); err != nil {
		t.Fatal(err)
	}
	read := func(thumb bool) string {
		r, err := f.Open("abc", thumb)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Close()
		b, _ := io.ReadAll(r)
		return string(b)
	}
	if read(false) != "big" || read(true) != "small" {
		t.Errorf("read back %q and %q", read(false), read(true))
	}
	if err := f.Remove("abc"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Open("abc", false); !os.IsNotExist(err) {
		t.Errorf("Open after Remove = %v, want not exist", err)
	}
	if err := f.Remove("abc"); err != nil {
		t.Errorf("removing twice = %v, want nil", err)
	}
}

func TestFilesRefuseIDsThatLeaveTheirDirectory(t *testing.T) {
	f := photo.NewFiles(t.TempDir())
	for _, id := range []string{"../x", "a/b", "", ".."} {
		if err := f.Put(id, []byte("x"), []byte("x")); err == nil {
			t.Errorf("Put(%q) = nil, want an error", id)
		}
		if _, err := f.Open(id, false); err == nil {
			t.Errorf("Open(%q) = nil, want an error", id)
		}
	}
}

func write(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestScanFindsPhotosAndSubfolders(t *testing.T) {
	root := t.TempDir()
	write(t, root, "lose.jpg", "1")
	write(t, root, "Weihnachten 2025/a.JPG", "22")
	write(t, root, "Weihnachten 2025/tief/b.png", "333")
	write(t, root, "Weihnachten 2025/notiz.txt", "x")
	write(t, root, "Weihnachten 2025/.versteckt.jpg", "x")
	write(t, root, "Weihnachten 2025/foto.heic", "x")
	if err := os.MkdirAll(filepath.Join(root, "Leer"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/etc/passwd", filepath.Join(root, "Leer", "link.jpg")); err != nil {
		t.Fatal(err)
	}
	res, err := photo.Scan(root)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, s := range res.Photos {
		got[s.Path] = s.Folder
	}
	want := map[string]string{"lose.jpg": "", "Weihnachten 2025/a.JPG": "Weihnachten 2025", "Weihnachten 2025/tief/b.png": "Weihnachten 2025"}
	if len(got) != len(want) {
		t.Fatalf("photos = %v, want %v", got, want)
	}
	for p, f := range want {
		if got[p] != f {
			t.Errorf("photo %q in folder %q, want %q", p, got[p], f)
		}
	}
	if len(res.Folders) != 2 {
		t.Errorf("folders = %v, want Leer and Weihnachten 2025", res.Folders)
	}
	if res.Photos[0].Stamp == "" {
		t.Error("a photo has no stamp")
	}
}

func TestScanOfAMissingFolderFindsNothing(t *testing.T) {
	res, err := photo.Scan(filepath.Join(t.TempDir(), "nope"))
	if err != nil || len(res.Photos) != 0 || len(res.Folders) != 0 {
		t.Errorf("Scan = %+v, %v", res, err)
	}
}

func TestStampChangesWithTheFile(t *testing.T) {
	root := t.TempDir()
	write(t, root, "a.jpg", "1")
	before, _ := photo.Scan(root)
	write(t, root, "a.jpg", "12")
	after, _ := photo.Scan(root)
	if before.Photos[0].Stamp == after.Photos[0].Stamp {
		t.Error("stamp did not change when the file changed")
	}
}
