// Package photo prepares photos for the screensaver (ticket 178): it decodes a
// JPEG or PNG, turns it upright by the camera's orientation, shrinks it to
// display size and encodes it again as JPEG. Nothing of the original file is
// kept, so the location and every other piece of metadata is gone.
package photo

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	"image/draw"
	"image/jpeg"
	_ "image/png" // registers the PNG decoder
	"io"
)

const (
	// MaxEdge is the longest edge of the stored photo in pixels: the size of a
	// large display, so a cover crop on any display stays sharp.
	MaxEdge = 2048
	// ThumbEdge is the longest edge of the small picture shown on the photo pages.
	ThumbEdge = 400
	// MaxPixels is the most pixels a photo may have before it is decoded (a 60
	// megapixel image needs about 240 MB as RGBA).
	MaxPixels = 60_000_000
	// MaxBytes is the largest file the box reads.
	MaxBytes = 64 << 20
	quality  = 85
)

var (
	// ErrNotPhoto is returned for anything that is not a JPEG or PNG image
	// (HEIC is converted in the browser before it is sent).
	ErrNotPhoto = errors.New("photo: not a JPEG or PNG image")
	// ErrTooLarge is returned for an image with too many pixels or bytes.
	ErrTooLarge = errors.New("photo: image is too large")
)

// Result is a prepared photo.
type Result struct {
	Display, Thumb []byte // JPEG
	Width, Height  int    // of Display
}

// Process prepares the photo read from r.
func Process(r io.Reader) (Result, error) {
	raw, err := io.ReadAll(io.LimitReader(r, MaxBytes+1))
	if err != nil {
		return Result{}, err
	}
	if len(raw) > MaxBytes {
		return Result{}, ErrTooLarge
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil || (format != "jpeg" && format != "png") {
		return Result{}, ErrNotPhoto
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width*cfg.Height > MaxPixels {
		return Result{}, ErrTooLarge
	}
	src, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return Result{}, ErrNotPhoto
	}
	rgba := image.NewRGBA(src.Bounds())
	draw.Draw(rgba, rgba.Bounds(), src, src.Bounds().Min, draw.Src)
	src = nil

	main := fit(rgba, MaxEdge)
	if format == "jpeg" {
		main = orient(main, orientation(raw))
	}
	thumb := fit(main, ThumbEdge)
	res := Result{Width: main.Bounds().Dx(), Height: main.Bounds().Dy()}
	if res.Display, err = encode(main); err != nil {
		return Result{}, err
	}
	if res.Thumb, err = encode(thumb); err != nil {
		return Result{}, err
	}
	return res, nil
}

func encode(img *image.RGBA) ([]byte, error) {
	var b bytes.Buffer
	if err := jpeg.Encode(&b, img, &jpeg.Options{Quality: quality}); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// fit shrinks img so its longest edge is at most edge pixels; a smaller image
// is returned as it is.
func fit(img *image.RGBA, edge int) *image.RGBA {
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	if w <= edge && h <= edge {
		return img
	}
	nw, nh := edge, max(1, h*edge/w)
	if h > w {
		nw, nh = max(1, w*edge/h), edge
	}
	return shrink(img, nw, nh)
}

// shrink averages the source pixels that fall onto each target pixel, first
// across, then down.
func shrink(src *image.RGBA, w, h int) *image.RGBA {
	sw, sh := src.Bounds().Dx(), src.Bounds().Dy()
	across := image.NewRGBA(image.Rect(0, 0, w, sh))
	for y := range sh {
		row := src.Pix[y*src.Stride:]
		out := across.Pix[y*across.Stride:]
		for x := range w {
			x0, x1 := x*sw/w, max((x+1)*sw/w, x*sw/w+1)
			var sum [4]int
			for sx := x0; sx < x1; sx++ {
				for c := range 4 {
					sum[c] += int(row[sx*4+c])
				}
			}
			n := x1 - x0
			for c := range 4 {
				out[x*4+c] = uint8(sum[c] / n)
			}
		}
	}
	down := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		y0, y1 := y*sh/h, max((y+1)*sh/h, y*sh/h+1)
		out := down.Pix[y*down.Stride:]
		for x := range w * 4 {
			sum := 0
			for sy := y0; sy < y1; sy++ {
				sum += int(across.Pix[sy*across.Stride+x])
			}
			out[x] = uint8(sum / (y1 - y0))
		}
	}
	return down
}

// orientation reads the EXIF orientation (1 to 8) of a JPEG; 1 when there is none.
func orientation(raw []byte) int {
	if len(raw) < 4 || raw[0] != 0xff || raw[1] != 0xd8 {
		return 1
	}
	for i := 2; i+4 <= len(raw) && raw[i] == 0xff; {
		marker, n := raw[i+1], int(binary.BigEndian.Uint16(raw[i+2:]))
		if marker == 0xda || n < 2 { // start of scan: no more headers
			return 1
		}
		seg := raw[min(i+4, len(raw)):min(i+2+n, len(raw))]
		if marker == 0xe1 && bytes.HasPrefix(seg, []byte("Exif\x00\x00")) {
			return tiffOrientation(seg[6:])
		}
		i += 2 + n
	}
	return 1
}

func tiffOrientation(t []byte) int {
	if len(t) < 8 {
		return 1
	}
	var bo binary.ByteOrder
	switch string(t[:2]) {
	case "II":
		bo = binary.LittleEndian
	case "MM":
		bo = binary.BigEndian
	default:
		return 1
	}
	off := int(bo.Uint32(t[4:]))
	if off < 8 || off+2 > len(t) {
		return 1
	}
	count := int(bo.Uint16(t[off:]))
	for i := range count {
		e := off + 2 + i*12
		if e+12 > len(t) {
			break
		}
		if bo.Uint16(t[e:]) == 0x0112 && bo.Uint16(t[e+2:]) == 3 {
			if o := int(bo.Uint16(t[e+8:])); o >= 1 && o <= 8 {
				return o
			}
		}
	}
	return 1
}

// orient turns an image upright for EXIF orientation o (1 is as it is).
func orient(src *image.RGBA, o int) *image.RGBA {
	if o <= 1 {
		return src
	}
	w, h := src.Bounds().Dx(), src.Bounds().Dy()
	dw, dh := w, h
	if o >= 5 {
		dw, dh = h, w
	}
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	for y := range h {
		for x := range w {
			var dx, dy int
			switch o {
			case 2: // mirrored left to right
				dx, dy = w-1-x, y
			case 3: // turned 180 degrees
				dx, dy = w-1-x, h-1-y
			case 4: // mirrored top to bottom
				dx, dy = x, h-1-y
			case 5: // transposed
				dx, dy = y, x
			case 6: // turned 90 degrees clockwise
				dx, dy = h-1-y, x
			case 7: // transversed
				dx, dy = h-1-y, w-1-x
			default: // 8: turned 90 degrees counter-clockwise
				dx, dy = y, w-1-x
			}
			copy(dst.Pix[dy*dst.Stride+dx*4:dy*dst.Stride+dx*4+4], src.Pix[y*src.Stride+x*4:y*src.Stride+x*4+4])
		}
	}
	return dst
}
