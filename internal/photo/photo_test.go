package photo_test

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"

	"github.com/hejmo-home/hejmo/internal/photo"
)

// gradient is a w x h image whose left half is red and right half blue, so a
// rotation shows in the pixels.
func gradient(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		row := img.Pix[y*img.Stride:]
		for x := range w {
			if x < w/2 {
				row[x*4], row[x*4+3] = 255, 255
			} else {
				row[x*4+2], row[x*4+3] = 255, 255
			}
		}
	}
	return img
}

func jpegOf(t *testing.T, img image.Image) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := jpeg.Encode(&b, img, nil); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// withEXIF inserts an APP1 EXIF segment holding an orientation and a GPS
// pointer after the JPEG start marker.
func withEXIF(t *testing.T, raw []byte, orientation uint16) []byte {
	t.Helper()
	var tiff bytes.Buffer
	tiff.WriteString("MM\x00\x2a")
	binary.Write(&tiff, binary.BigEndian, uint32(8))
	binary.Write(&tiff, binary.BigEndian, uint16(2)) // two entries
	binary.Write(&tiff, binary.BigEndian, uint16(0x0112))
	binary.Write(&tiff, binary.BigEndian, uint16(3)) // SHORT
	binary.Write(&tiff, binary.BigEndian, uint32(1))
	binary.Write(&tiff, binary.BigEndian, orientation)
	binary.Write(&tiff, binary.BigEndian, uint16(0))
	binary.Write(&tiff, binary.BigEndian, uint16(0x8825)) // GPS IFD pointer
	binary.Write(&tiff, binary.BigEndian, uint16(4))      // LONG
	binary.Write(&tiff, binary.BigEndian, uint32(1))
	binary.Write(&tiff, binary.BigEndian, uint32(0x12345678))
	binary.Write(&tiff, binary.BigEndian, uint32(0))
	seg := append([]byte("Exif\x00\x00"), tiff.Bytes()...)
	out := []byte{0xff, 0xd8, 0xff, 0xe1}
	out = binary.BigEndian.AppendUint16(out, uint16(len(seg)+2))
	out = append(out, seg...)
	return append(out, raw[2:]...)
}

func decode(t *testing.T, raw []byte) image.Image {
	t.Helper()
	img, err := jpeg.Decode(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("result is not a JPEG: %v", err)
	}
	return img
}

func TestLargePhotoIsShrunkToDisplaySizeKeepingProportions(t *testing.T) {
	res, err := photo.Process(bytes.NewReader(jpegOf(t, gradient(2800, 2100))))
	if err != nil {
		t.Fatal(err)
	}
	if res.Width != photo.MaxEdge || res.Height != photo.MaxEdge*3/4 {
		t.Errorf("size = %dx%d, want %dx%d", res.Width, res.Height, photo.MaxEdge, photo.MaxEdge*3/4)
	}
	if b := decode(t, res.Display).Bounds(); b.Dx() != res.Width || b.Dy() != res.Height {
		t.Errorf("stored image is %dx%d, result says %dx%d", b.Dx(), b.Dy(), res.Width, res.Height)
	}
	if b := decode(t, res.Thumb).Bounds(); b.Dx() != photo.ThumbEdge || b.Dy() != photo.ThumbEdge*3/4 {
		t.Errorf("thumbnail is %dx%d", b.Dx(), b.Dy())
	}
}

func TestSmallPhotoIsNotEnlarged(t *testing.T) {
	res, err := photo.Process(bytes.NewReader(jpegOf(t, gradient(300, 200))))
	if err != nil {
		t.Fatal(err)
	}
	if res.Width != 300 || res.Height != 200 {
		t.Errorf("size = %dx%d, want 300x200", res.Width, res.Height)
	}
}

func TestLocationAndOtherMetadataAreRemoved(t *testing.T) {
	raw := withEXIF(t, jpegOf(t, gradient(200, 100)), 1)
	if !bytes.Contains(raw, []byte("Exif")) {
		t.Fatal("test input has no EXIF")
	}
	res, err := photo.Process(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	for name, out := range map[string][]byte{"photo": res.Display, "thumbnail": res.Thumb} {
		if bytes.Contains(out, []byte("Exif")) || bytes.Contains(out, []byte{0x12, 0x34, 0x56, 0x78}) {
			t.Errorf("%s still carries EXIF data", name)
		}
	}
}

func TestOrientationFromTheCameraIsApplied(t *testing.T) {
	// Orientation 6: the stored pixels must be turned 90 degrees clockwise to
	// show upright, so a 200x100 image becomes 100x200 and its red left half
	// ends up on top.
	raw := withEXIF(t, jpegOf(t, gradient(200, 100)), 6)
	res, err := photo.Process(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if res.Width != 100 || res.Height != 200 {
		t.Fatalf("size = %dx%d, want 100x200", res.Width, res.Height)
	}
	img := decode(t, res.Display)
	top, bottom := color.RGBAModel.Convert(img.At(50, 20)).(color.RGBA), color.RGBAModel.Convert(img.At(50, 180)).(color.RGBA)
	if top.R < 200 || bottom.B < 200 {
		t.Errorf("top = %v, bottom = %v; want red on top and blue below", top, bottom)
	}
}

func TestPNGIsAccepted(t *testing.T) {
	var b bytes.Buffer
	if err := png.Encode(&b, gradient(120, 80)); err != nil {
		t.Fatal(err)
	}
	res, err := photo.Process(&b)
	if err != nil {
		t.Fatal(err)
	}
	if res.Width != 120 || res.Height != 80 {
		t.Errorf("size = %dx%d", res.Width, res.Height)
	}
	decode(t, res.Display)
}

func TestNotAPhotoIsRefused(t *testing.T) {
	heic := append([]byte{0, 0, 0, 24}, []byte("ftypheic")...)
	for name, in := range map[string][]byte{"text": []byte("Reiseplan Gardasee"), "pdf": []byte("%PDF-1.7 ..."), "heic": heic, "empty": nil} {
		if _, err := photo.Process(bytes.NewReader(in)); err == nil || err != photo.ErrNotPhoto {
			t.Errorf("%s: err = %v, want ErrNotPhoto", name, err)
		}
	}
}

func TestHugeImageIsRefusedBeforeItIsDecoded(t *testing.T) {
	// A PNG header that promises 40000 x 40000 pixels.
	var b bytes.Buffer
	if err := png.Encode(&b, gradient(8, 8)); err != nil {
		t.Fatal(err)
	}
	raw := b.Bytes()
	binary.BigEndian.PutUint32(raw[16:], 40000)
	binary.BigEndian.PutUint32(raw[20:], 40000)
	binary.BigEndian.PutUint32(raw[29:], crc32.ChecksumIEEE(raw[12:29])) // the header's checksum
	if _, err := photo.Process(bytes.NewReader(raw)); err != photo.ErrTooLarge {
		t.Errorf("err = %v, want ErrTooLarge", err)
	}
}
