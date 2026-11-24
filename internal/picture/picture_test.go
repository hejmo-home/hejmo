package picture_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"strings"
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/picture"
)

// Ticket 164 (ADR 0019): every picture is stored re-encoded, scaled down to
// at most 512 px, as JPEG, without metadata.

func gradient(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{uint8(x * 255 / w), uint8(y * 255 / h), 120, 255})
		}
	}
	return img
}

func encodeJPEG(t *testing.T, img image.Image) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := jpeg.Encode(&b, img, nil); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func size(t *testing.T, jpg []byte) (int, int) {
	t.Helper()
	img, err := jpeg.Decode(bytes.NewReader(jpg))
	if err != nil {
		t.Fatalf("the result is not a JPEG: %v", err)
	}
	return img.Bounds().Dx(), img.Bounds().Dy()
}

// withEXIF puts an APP1 segment with a GPS block, a camera name and an
// orientation behind the start of a JPEG.
func withEXIF(jpg []byte, orientation uint16) []byte {
	var tiff bytes.Buffer
	tiff.WriteString("MM\x00\x2a\x00\x00\x00\x08")
	entries := [][]byte{}
	entry := func(tag, typ uint16, count uint32, val uint32) []byte {
		var e bytes.Buffer
		binary.Write(&e, binary.BigEndian, tag)
		binary.Write(&e, binary.BigEndian, typ)
		binary.Write(&e, binary.BigEndian, count)
		binary.Write(&e, binary.BigEndian, val)
		return e.Bytes()
	}
	entries = append(entries, entry(0x010f, 2, 6, 0x4b6f6461)) // Make: "Koda.."
	entries = append(entries, entry(0x0112, 3, 1, uint32(orientation)<<16))
	entries = append(entries, entry(0x8825, 4, 1, 0x0000ffff)) // GPS IFD pointer
	binary.Write(&tiff, binary.BigEndian, uint16(len(entries)))
	for _, e := range entries {
		tiff.Write(e)
	}
	binary.Write(&tiff, binary.BigEndian, uint32(0))
	payload := append([]byte("Exif\x00\x00"), tiff.Bytes()...)
	seg := []byte{0xff, 0xe1}
	seg = binary.BigEndian.AppendUint16(seg, uint16(len(payload)+2))
	seg = append(seg, payload...)
	return append(append(append([]byte{}, jpg[:2]...), seg...), jpg[2:]...)
}

func TestAPhotoIsScaledDownToFiveHundredAndTwelvePixelsAndKeepsItsProportions(t *testing.T) {
	res, err := picture.Process(bytes.NewReader(encodeJPEG(t, gradient(2000, 1000))))
	if err != nil {
		t.Fatal(err)
	}
	if w, h := size(t, res.JPEG); w != 512 || h != 256 {
		t.Errorf("result %d x %d, want 512 x 256", w, h)
	}
	if res.FromW != 2000 || res.FromH != 1000 || res.ToW != 512 || res.ToH != 256 || res.FromBytes == 0 {
		t.Errorf("facts = %+v", res)
	}
	tall, _ := picture.Process(bytes.NewReader(encodeJPEG(t, gradient(600, 1800))))
	if w, h := size(t, tall.JPEG); w != 171 || h != 512 {
		t.Errorf("tall result %d x %d, want 171 x 512", w, h)
	}
}

func TestASmallPictureIsNotEnlarged(t *testing.T) {
	res, err := picture.Process(bytes.NewReader(encodeJPEG(t, gradient(120, 80))))
	if err != nil {
		t.Fatal(err)
	}
	if w, h := size(t, res.JPEG); w != 120 || h != 80 {
		t.Errorf("result %d x %d", w, h)
	}
}

func TestMetadataIsRemoved(t *testing.T) {
	src := withEXIF(encodeJPEG(t, gradient(300, 200)), 1)
	if !bytes.Contains(src, []byte("Exif")) {
		t.Fatal("the test photo has no EXIF")
	}
	res, err := picture.Process(bytes.NewReader(src))
	if err != nil {
		t.Fatal(err)
	}
	for _, leak := range []string{"Exif", "Koda", "\xff\xe1", "XMP", "http://ns.adobe.com"} {
		if bytes.Contains(res.JPEG, []byte(leak)) {
			t.Errorf("the result still holds %q", leak)
		}
	}
}

func TestThePersonalDeviceOrientationIsAppliedBeforeTheMetadataIsDropped(t *testing.T) {
	// A 300 x 200 photo that the personal device took lying down: orientation 6 means "turn right".
	src := withEXIF(encodeJPEG(t, gradient(300, 200)), 6)
	res, err := picture.Process(bytes.NewReader(src))
	if err != nil {
		t.Fatal(err)
	}
	if w, h := size(t, res.JPEG); w != 200 || h != 300 {
		t.Errorf("result %d x %d, want 200 x 300", w, h)
	}
}

func TestPNGWithTransparencyGIFAndWebPAreAccepted(t *testing.T) {
	var pngBytes bytes.Buffer
	img := image.NewNRGBA(image.Rect(0, 0, 40, 30))
	img.Set(5, 5, color.NRGBA{200, 0, 0, 255})
	if err := png.Encode(&pngBytes, img); err != nil {
		t.Fatal(err)
	}
	res, err := picture.Process(&pngBytes)
	if err != nil {
		t.Fatalf("png: %v", err)
	}
	out, _ := jpeg.Decode(bytes.NewReader(res.JPEG))
	if r, g, b, _ := out.At(30, 20).RGBA(); r>>8 < 240 || g>>8 < 240 || b>>8 < 240 {
		t.Errorf("transparent pixels must turn white, got %d %d %d", r>>8, g>>8, b>>8)
	}

	var gifBytes bytes.Buffer
	pal := image.NewPaletted(image.Rect(0, 0, 20, 20), color.Palette{color.White, color.Black})
	if err := gif.Encode(&gifBytes, pal, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := picture.Process(&gifBytes); err != nil {
		t.Errorf("gif: %v", err)
	}

	webp, _ := base64.StdEncoding.DecodeString("UklGRhoAAABXRUJQVlA4TA0AAAAvAAAAEAcQERGIiP4HAA==")
	if _, err := picture.Process(bytes.NewReader(webp)); err != nil {
		t.Errorf("webp: %v", err)
	}
}

func TestWhatIsNotAPictureIsRefused(t *testing.T) {
	for name, body := range map[string]string{
		"text":      "Das ist kein Bild.",
		"empty":     "",
		"svg":       `<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`,
		"pdf":       "%PDF-1.4 …",
		"cut jpeg":  string(encodeJPEGCut()),
		"exe":       "MZ\x90\x00\x03",
		"heic like": "\x00\x00\x00\x18ftypheic\x00\x00\x00\x00",
	} {
		if _, err := picture.Process(strings.NewReader(body)); !errors.Is(err, picture.ErrNotAnImage) {
			t.Errorf("%s: err = %v, want ErrNotAnImage", name, err)
		}
	}
}

func encodeJPEGCut() []byte {
	var b bytes.Buffer
	_ = jpeg.Encode(&b, gradient(300, 300), nil)
	return b.Bytes()[:200]
}

func TestAWebPageInsteadOfAPictureIsNamedAsSuch(t *testing.T) {
	for _, body := range []string{"<!DOCTYPE html><html><body>Shop</body></html>", "  \n<html lang=de>", "<HTML>"} {
		if _, err := picture.Process(strings.NewReader(body)); !errors.Is(err, picture.ErrWebPage) {
			t.Errorf("%q: err = %v, want ErrWebPage", body, err)
		}
	}
}

func TestTooBigIsRefusedWithItsSize(t *testing.T) {
	big := bytes.NewReader(append([]byte{0xff, 0xd8}, make([]byte, picture.MaxBytes+10)...))
	_, err := picture.Process(big)
	var tooLarge *picture.TooLargeError
	if !errors.As(err, &tooLarge) || tooLarge.Bytes <= picture.MaxBytes {
		t.Fatalf("err = %v, want TooLargeError over the limit", err)
	}
	if picture.MaxBytes != 15<<20 {
		t.Errorf("the limit is %d, want 15 MB", picture.MaxBytes)
	}
}

func TestAPictureWithAHugeNumberOfPixelsIsRefusedBeforeItIsDecoded(t *testing.T) {
	// A 30000 x 30000 PNG header: tiny file, 900 megapixels once decoded.
	var b bytes.Buffer
	b.Write([]byte("\x89PNG\r\n\x1a\n"))
	chunk := func(kind string, data []byte) {
		binary.Write(&b, binary.BigEndian, uint32(len(data)))
		b.WriteString(kind)
		b.Write(data)
		sum := crc32.NewIEEE()
		sum.Write([]byte(kind))
		sum.Write(data)
		binary.Write(&b, binary.BigEndian, sum.Sum32())
	}
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:], 30000)
	binary.BigEndian.PutUint32(ihdr[4:], 30000)
	ihdr[8], ihdr[9] = 8, 2
	chunk("IHDR", ihdr)
	chunk("IEND", nil)
	if _, err := picture.Process(&b); !errors.Is(err, picture.ErrTooManyPixels) {
		t.Errorf("err = %v, want ErrTooManyPixels", err)
	}
}

func TestAPictureOfThirtyMegapixelsIsRefusedBeforeItIsDecoded(t *testing.T) {
	// 6000 x 5000 = 30 MP: more than a 1 GB box can decode next to everything else.
	var b bytes.Buffer
	b.Write([]byte("\x89PNG\r\n\x1a\n"))
	chunk := func(kind string, data []byte) {
		binary.Write(&b, binary.BigEndian, uint32(len(data)))
		b.WriteString(kind)
		b.Write(data)
		sum := crc32.NewIEEE()
		sum.Write([]byte(kind))
		sum.Write(data)
		binary.Write(&b, binary.BigEndian, sum.Sum32())
	}
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:], 6000)
	binary.BigEndian.PutUint32(ihdr[4:], 5000)
	ihdr[8], ihdr[9] = 8, 2
	chunk("IHDR", ihdr)
	chunk("IEND", nil)
	if _, err := picture.Process(&b); !errors.Is(err, picture.ErrTooManyPixels) {
		t.Errorf("err = %v, want ErrTooManyPixels", err)
	}
}

func TestTwoPicturesAreNotDecodedAtTheSameTime(t *testing.T) {
	release := picture.Hold() // a picture is being decoded
	data := encodeJPEG(t, gradient(120, 80))
	done := make(chan error, 1)
	go func() {
		_, err := picture.Process(bytes.NewReader(data))
		done <- err
	}()
	select {
	case <-done:
		t.Fatal("a second picture was decoded while the first still held the slot")
	case <-time.After(150 * time.Millisecond):
	}
	release()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("err = %v after the slot was free", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the waiting picture was never decoded after the slot was free")
	}
}

func TestACancelledRequestStopsWaitingForTheSlot(t *testing.T) {
	release := picture.Hold()
	defer release()
	ctx, cancel := context.WithCancel(context.Background())
	data := encodeJPEG(t, gradient(120, 80))
	done := make(chan error, 1)
	go func() {
		_, err := picture.ProcessContext(ctx, bytes.NewReader(data))
		done <- err
	}()
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("err = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a cancelled request kept waiting for the slot")
	}
}

// Ticket 276: the longest side is a parameter, so recipes (1600 px) can keep
// larger pictures than articles (512 px).

func TestACallerCanAskForALargerMaximumAndStillGetsAReEncodedPictureWithoutMetadata(t *testing.T) {
	if picture.RecipeMaxSide != 1600 {
		t.Fatalf("RecipeMaxSide = %d, want 1600", picture.RecipeMaxSide)
	}
	src := withEXIF(encodeJPEG(t, gradient(3000, 2000)), 1)
	res, err := picture.ProcessMax(context.Background(), bytes.NewReader(src), picture.RecipeMaxSide)
	if err != nil {
		t.Fatal(err)
	}
	if w, h := size(t, res.JPEG); w != 1600 || h != 1067 {
		t.Errorf("result %d x %d, want 1600 x 1067", w, h)
	}
	if res.ToW != 1600 || res.ToH != 1067 {
		t.Errorf("facts = %+v", res)
	}
	for _, leak := range []string{"Exif", "Koda", "\xff\xe1", "XMP", "http://ns.adobe.com"} {
		if bytes.Contains(res.JPEG, []byte(leak)) {
			t.Errorf("the result still holds %q", leak)
		}
	}
	// Smaller than the limit: not enlarged.
	small, err := picture.ProcessMax(context.Background(), bytes.NewReader(encodeJPEG(t, gradient(1200, 800))), picture.RecipeMaxSide)
	if err != nil {
		t.Fatal(err)
	}
	if w, h := size(t, small.JPEG); w != 1200 || h != 800 {
		t.Errorf("small result %d x %d", w, h)
	}
}

func TestTheSameLargePhotoIsScaledToEachCallersOwnMaximum(t *testing.T) {
	data := encodeJPEG(t, gradient(3000, 2000))
	for _, c := range []struct{ max, w, h int }{{512, 512, 341}, {1600, 1600, 1067}, {0, 512, 341}, {-1, 512, 341}} {
		res, err := picture.ProcessMax(context.Background(), bytes.NewReader(data), c.max)
		if err != nil {
			t.Fatal(err)
		}
		if w, h := size(t, res.JPEG); w != c.w || h != c.h {
			t.Errorf("max %d: result %d x %d, want %d x %d", c.max, w, h, c.w, c.h)
		}
	}
	// Process stays what it was: 512 px.
	res, err := picture.Process(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if w, h := size(t, res.JPEG); w != 512 || h != 341 {
		t.Errorf("Process result %d x %d, want 512 x 341", w, h)
	}
}

// Ticket 297: a contact photo is cut to a square before it is scaled to
// ContactMaxSide: landscape from the centre, portrait with a quarter of the
// excess taken from the top (heads stay whole).

// bands is a picture whose colour changes with the long axis: every 10 px a
// new grey step, so the crop can be read from the result.
func bands(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			v := uint8(0)
			if w > h {
				v = uint8(x * 255 / w)
			} else {
				v = uint8(y * 255 / h)
			}
			img.Set(x, y, color.RGBA{v, v, v, 255})
		}
	}
	return img
}

func greyAt(t *testing.T, jpg []byte, fx, fy float64) int {
	t.Helper()
	img, err := jpeg.Decode(bytes.NewReader(jpg))
	if err != nil {
		t.Fatal(err)
	}
	b := img.Bounds()
	r, _, _, _ := img.At(b.Min.X+int(fx*float64(b.Dx()-1)), b.Min.Y+int(fy*float64(b.Dy()-1))).RGBA()
	return int(r >> 8)
}

func TestContactPhotoIsSquareAtMost512(t *testing.T) {
	t.Parallel()
	for name, c := range map[string]struct{ w, h, want int }{
		"portrait":        {600, 1200, 512},
		"landscape":       {1600, 900, 512},
		"square":          {700, 700, 512},
		"small portrait":  {100, 300, 100},
		"small landscape": {300, 120, 120},
	} {
		res, err := picture.ProcessSquare(context.Background(), bytes.NewReader(encodeJPEG(t, bands(c.w, c.h))), picture.ContactMaxSide)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		w, h := size(t, res.JPEG)
		if w != c.want || h != c.want || res.ToW != c.want || res.ToH != c.want {
			t.Errorf("%s: result %d x %d (%d x %d), want %d x %d", name, w, h, res.ToW, res.ToH, c.want, c.want)
		}
		if res.FromW != c.w || res.FromH != c.h {
			t.Errorf("%s: from %d x %d, want %d x %d", name, res.FromW, res.FromH, c.w, c.h)
		}
	}
	if picture.ContactMaxSide != 512 {
		t.Errorf("ContactMaxSide = %d", picture.ContactMaxSide)
	}
}

func TestContactPhotoCropPortraitKeepsMoreAtTheTopLandscapeIsCentred(t *testing.T) {
	t.Parallel()
	// 400 x 800: the square is 400 tall and starts at excess/4 = 100, so it covers
	// rows 100 to 500 of 800: grey 31 at its top, 62 at its bottom (about).
	res, err := picture.ProcessSquare(context.Background(), bytes.NewReader(encodeJPEG(t, bands(400, 800))), picture.ContactMaxSide)
	if err != nil {
		t.Fatal(err)
	}
	if top, bottom := greyAt(t, res.JPEG, 0.5, 0), greyAt(t, res.JPEG, 0.5, 1); abs(top-100*255/800) > 6 || abs(bottom-500*255/800) > 6 {
		t.Errorf("portrait crop: top grey %d (want about %d), bottom %d (want about %d)", top, 100*255/800, bottom, 500*255/800)
	}
	// 800 x 400: the square covers columns 200 to 600.
	res, err = picture.ProcessSquare(context.Background(), bytes.NewReader(encodeJPEG(t, bands(800, 400))), picture.ContactMaxSide)
	if err != nil {
		t.Fatal(err)
	}
	if left, right := greyAt(t, res.JPEG, 0, 0.5), greyAt(t, res.JPEG, 1, 0.5); abs(left-200*255/800) > 6 || abs(right-600*255/800) > 6 {
		t.Errorf("landscape crop: left grey %d (want about %d), right %d (want about %d)", left, 200*255/800, right, 600*255/800)
	}
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

func TestContactPhotoKeepsTheRulesOfEveryPicture(t *testing.T) {
	t.Parallel()
	// An upright portrait taken sideways (orientation 6) is turned first, then cropped; no metadata survives.
	res, err := picture.ProcessSquare(context.Background(), bytes.NewReader(withEXIF(encodeJPEG(t, bands(800, 400)), 6)), picture.ContactMaxSide)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Rotated || !res.FromMetadata || bytes.Contains(res.JPEG, []byte("Exif")) {
		t.Errorf("rotated=%v fromMetadata=%v exifLeft=%v", res.Rotated, res.FromMetadata, bytes.Contains(res.JPEG, []byte("Exif")))
	}
	if _, err := picture.ProcessSquare(context.Background(), strings.NewReader("<html>"), picture.ContactMaxSide); !errors.Is(err, picture.ErrWebPage) {
		t.Errorf("web page = %v", err)
	}
	if _, err := picture.ProcessSquare(context.Background(), strings.NewReader("hello"), picture.ContactMaxSide); !errors.Is(err, picture.ErrNotAnImage) {
		t.Errorf("text = %v", err)
	}
	var big *picture.TooLargeError
	if _, err := picture.ProcessSquare(context.Background(), bytes.NewReader(make([]byte, picture.MaxBytes+1)), picture.ContactMaxSide); !errors.As(err, &big) {
		t.Errorf("15 MB + 1 = %v", err)
	}
}
