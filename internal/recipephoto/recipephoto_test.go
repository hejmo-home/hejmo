package recipephoto_test

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"path/filepath"
	"testing"

	"github.com/hejmo-home/hejmo/internal/picture"
	"github.com/hejmo-home/hejmo/internal/recipephoto"
)

// Ticket 280: the files of a recipe photo: the full photo at 1600 px, a cover and grid size at 640 px and a
// square crop of 168 px for list rows. Lists never load the full file.

func jpegOf(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{uint8(x % 256), uint8(y % 256), 90, 255})
		}
	}
	var b bytes.Buffer
	if err := jpeg.Encode(&b, img, nil); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func sides(t *testing.T, data []byte) (int, int) {
	t.Helper()
	img, err := jpeg.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	return img.Bounds().Dx(), img.Bounds().Dy()
}

func TestPrepareMakesThreeSizesAndTheStatedDimensionsAreTheRealOnes(t *testing.T) {
	t.Parallel()
	set, err := recipephoto.Prepare(context.Background(), bytes.NewReader(jpegOf(t, 2400, 1800)))
	if err != nil {
		t.Fatal(err)
	}
	if w, h := sides(t, set.Full); w != 1600 || h != 1200 || set.Width != 1600 || set.Height != 1200 {
		t.Errorf("full = %dx%d, stated %dx%d: want 1600x1200", w, h, set.Width, set.Height)
	}
	if w, h := sides(t, set.Medium); w != 640 || h != 480 {
		t.Errorf("medium = %dx%d", w, h)
	}
	if w, h := sides(t, set.Small); w != 168 || h != 168 {
		t.Errorf("small = %dx%d, want a square of 168", w, h)
	}
	for _, c := range []struct{ w, h int }{{1600, 1200}, {1200, 1600}, {100, 80}, {640, 640}} {
		set, err := recipephoto.Prepare(context.Background(), bytes.NewReader(jpegOf(t, c.w, c.h)))
		if err != nil {
			t.Fatal(err)
		}
		mw, mh := sides(t, set.Medium)
		sw, sh := sides(t, set.Small)
		gw, gh := recipephoto.MediumSize(set.Width, set.Height)
		qw, qh := recipephoto.SmallSize(set.Width, set.Height)
		if gw != mw || gh != mh || qw != sw || qh != sh {
			t.Errorf("%dx%d: stated medium %dx%d small %dx%d, real %dx%d and %dx%d", c.w, c.h, gw, gh, qw, qh, mw, mh, sw, sh)
		}
	}
}

func TestPrepareRefusesWhatIsNoPictureAndWhatIsTooBig(t *testing.T) {
	t.Parallel()
	if _, err := recipephoto.Prepare(context.Background(), bytes.NewReader([]byte("%PDF-1.4 nothing"))); err == nil {
		t.Error("a PDF was taken")
	}
	heic := append([]byte{0, 0, 0, 24}, []byte("ftypheic")...)
	if _, err := recipephoto.Prepare(context.Background(), bytes.NewReader(append(heic, make([]byte, 20)...))); err != recipephoto.ErrHEIC {
		t.Errorf("HEIC = %v, want ErrHEIC", err)
	}
	big := bytes.Repeat([]byte{1}, picture.MaxBytes+10)
	if _, err := recipephoto.Prepare(context.Background(), bytes.NewReader(big)); err == nil {
		t.Error("16 MB were taken")
	}
}

func TestFilesKeepThreeSizesAndRemoveThemTogether(t *testing.T) {
	t.Parallel()
	f := recipephoto.NewFiles(filepath.Join(t.TempDir(), "recipe-photos"))
	if err := f.Put("abc", recipephoto.Set{Full: []byte("F"), Medium: []byte("M"), Small: []byte("S")}); err != nil {
		t.Fatal(err)
	}
	for size, want := range map[recipephoto.Size]string{recipephoto.Full: "F", recipephoto.Medium: "M", recipephoto.Small: "S"} {
		file, err := f.Open("abc", size)
		if err != nil {
			t.Fatal(err)
		}
		var b bytes.Buffer
		_, _ = b.ReadFrom(file)
		file.Close()
		if b.String() != want {
			t.Errorf("%v = %q, want %q", size, b.String(), want)
		}
	}
	if err := f.Remove("abc"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Open("abc", recipephoto.Small); !os.IsNotExist(err) {
		t.Errorf("after Remove: %v", err)
	}
	if err := f.Remove("abc"); err != nil {
		t.Errorf("removing twice: %v", err)
	}
	if err := f.Put("../x", recipephoto.Set{}); err == nil {
		t.Error("a path as id was taken")
	}
}
