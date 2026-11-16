// Package picture turns what a parent uploads or what the box downloads from
// the web into the one picture Hejmo stores (ADR 0019, ticket 164): decoded,
// turned upright, scaled down to at most 512 px, and written again as JPEG, so
// that no metadata (place, camera) and no active content survives. It also
// downloads web images safely, see Fetcher.
package picture

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"io"
	"strings"

	// The decoders of the types Hejmo takes.
	_ "image/gif"
	_ "image/png"

	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

// Limits.
const (
	// MaxBytes is the largest photo or download Hejmo takes.
	MaxBytes = 15 << 20
	// MaxSide is the longest side of the stored picture, in pixels.
	MaxSide = 512
	// MaxPixels is the most pixels a picture may have before it is decoded; a
	// small file can claim a gigantic image.
	MaxPixels = 40_000_000
	quality   = 85
)

// Errors that the pages turn into a sentence at the field.
var (
	// ErrNotAnImage: the bytes are not a JPEG, PNG, WebP or GIF.
	ErrNotAnImage = errors.New("picture: not a JPEG, PNG, WebP or GIF image")
	// ErrWebPage: the address or file holds a web page, not a picture.
	ErrWebPage = errors.New("picture: this is a web page, not a picture")
	// ErrTooManyPixels: the picture is too large to decode safely.
	ErrTooManyPixels = errors.New("picture: too many pixels")
)

// TooLargeError says the photo or download is over MaxBytes; Bytes is its
// size when known, else the number of bytes read before giving up.
type TooLargeError struct{ Bytes int64 }

func (e *TooLargeError) Error() string {
	return fmt.Sprintf("picture: %d bytes is over the limit of %d bytes", e.Bytes, int64(MaxBytes))
}

// Result is the stored picture and what happened to it, for the page to say in words.
type Result struct {
	JPEG                  []byte
	FromW, FromH          int
	ToW, ToH              int
	FromBytes             int
	FromType              string // "jpeg", "png", "gif" or "webp"
	FromMetadata, Rotated bool
}

// one is taken while a picture is decoded: Process holds several full-size
// copies at once, and a small machine has no room for two of them.
var one = make(chan struct{}, 1)

// Process reads a picture of at most MaxBytes and returns it as the stored JPEG.
func Process(r io.Reader) (Result, error) {
	data, err := io.ReadAll(io.LimitReader(r, MaxBytes+1))
	if err != nil {
		return Result{}, err
	}
	if len(data) > MaxBytes {
		return Result{}, &TooLargeError{Bytes: int64(len(data))}
	}
	if looksLikeWebPage(data) {
		return Result{}, ErrWebPage
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return Result{}, ErrNotAnImage
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width*cfg.Height > MaxPixels {
		return Result{}, ErrTooManyPixels
	}
	one <- struct{}{}
	defer func() { <-one }()
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return Result{}, ErrNotAnImage
	}
	res := Result{FromW: cfg.Width, FromH: cfg.Height, FromBytes: len(data), FromType: format}

	// Flatten onto white: JPEG has no transparency.
	rgba := image.NewRGBA(src.Bounds())
	draw.Draw(rgba, rgba.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	draw.Draw(rgba, rgba.Bounds(), src, src.Bounds().Min, draw.Over)

	if format == "jpeg" {
		res.FromMetadata = bytes.Contains(data[:min(len(data), 1<<16)], []byte("Exif")) || bytes.Contains(data[:min(len(data), 1<<16)], []byte("http://ns.adobe.com/xap"))
		if o := exifOrientation(data); o > 1 {
			rgba = orient(rgba, o)
			res.Rotated = o >= 5
		}
	}
	rgba = scale(rgba)
	res.ToW, res.ToH = rgba.Bounds().Dx(), rgba.Bounds().Dy()

	var out bytes.Buffer
	if err := jpeg.Encode(&out, rgba, &jpeg.Options{Quality: quality}); err != nil {
		return Result{}, err
	}
	res.JPEG = out.Bytes()
	return res, nil
}

// looksLikeWebPage reports whether the start of data is HTML.
func looksLikeWebPage(data []byte) bool {
	head := strings.ToLower(strings.TrimLeft(string(data[:min(len(data), 512)]), " \t\r\n\xef\xbb\xbf"))
	for _, p := range []string{"<!doctype html", "<html", "<head", "<body", "<!--"} {
		if strings.HasPrefix(head, p) {
			return true
		}
	}
	return false
}

func scale(src *image.RGBA) *image.RGBA {
	w, h := src.Bounds().Dx(), src.Bounds().Dy()
	if w <= MaxSide && h <= MaxSide {
		return src
	}
	nw, nh := MaxSide, MaxSide
	if w >= h {
		nh = max(1, (h*MaxSide+w/2)/w)
	} else {
		nw = max(1, (w*MaxSide+h/2)/h)
	}
	dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), src, src.Bounds(), xdraw.Over, nil)
	return dst
}

// exifOrientation reads the orientation (1 to 8) from the EXIF block of a
// JPEG, or 0 when there is none.
func exifOrientation(data []byte) int {
	if len(data) < 4 || data[0] != 0xff || data[1] != 0xd8 {
		return 0
	}
	i := 2
	for i+4 <= len(data) {
		if data[i] != 0xff {
			return 0
		}
		marker := data[i+1]
		if marker == 0xd8 || (marker >= 0xd0 && marker <= 0xd7) || marker == 0x01 {
			i += 2
			continue
		}
		if marker == 0xda || marker == 0xd9 { // start of scan: no more headers
			return 0
		}
		n := int(data[i+2])<<8 | int(data[i+3])
		if n < 2 || i+2+n > len(data) {
			return 0
		}
		seg := data[i+4 : i+2+n]
		if marker == 0xe1 && len(seg) > 14 && string(seg[:6]) == "Exif\x00\x00" {
			return tiffOrientation(seg[6:])
		}
		i += 2 + n
	}
	return 0
}

func tiffOrientation(t []byte) int {
	if len(t) < 8 {
		return 0
	}
	var u16 func([]byte) int
	var u32 func([]byte) int
	switch string(t[:2]) {
	case "II":
		u16 = func(b []byte) int { return int(b[0]) | int(b[1])<<8 }
		u32 = func(b []byte) int { return int(b[0]) | int(b[1])<<8 | int(b[2])<<16 | int(b[3])<<24 }
	case "MM":
		u16 = func(b []byte) int { return int(b[0])<<8 | int(b[1]) }
		u32 = func(b []byte) int { return int(b[0])<<24 | int(b[1])<<16 | int(b[2])<<8 | int(b[3]) }
	default:
		return 0
	}
	ifd := u32(t[4:])
	if ifd < 8 || ifd+2 > len(t) {
		return 0
	}
	count := u16(t[ifd:])
	for e := 0; e < count; e++ {
		at := ifd + 2 + e*12
		if at+12 > len(t) {
			return 0
		}
		if u16(t[at:]) == 0x0112 && u16(t[at+2:]) == 3 {
			if o := u16(t[at+8:]); o >= 1 && o <= 8 {
				return o
			}
			return 0
		}
	}
	return 0
}

// orient turns the picture upright for EXIF orientation o (2 to 8).
func orient(src *image.RGBA, o int) *image.RGBA {
	w, h := src.Bounds().Dx(), src.Bounds().Dy()
	dw, dh := w, h
	if o >= 5 {
		dw, dh = h, w
	}
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	for dy := 0; dy < dh; dy++ {
		for dx := 0; dx < dw; dx++ {
			var sx, sy int
			switch o {
			case 2:
				sx, sy = w-1-dx, dy
			case 3:
				sx, sy = w-1-dx, h-1-dy
			case 4:
				sx, sy = dx, h-1-dy
			case 5:
				sx, sy = dy, dx
			case 6:
				sx, sy = dy, h-1-dx
			case 7:
				sx, sy = w-1-dy, h-1-dx
			case 8:
				sx, sy = w-1-dy, dx
			default:
				sx, sy = dx, dy
			}
			dst.SetRGBA(dx, dy, src.RGBAAt(src.Bounds().Min.X+sx, src.Bounds().Min.Y+sy))
		}
	}
	return dst
}
