// Package recipephoto prepares and keeps the own photos of recipes (ticket 280): a family photo is decoded,
// turned upright, shrunk to at most 1600 px and written again as JPEG, so that the location and the camera
// data are gone, and two smaller files are made beside it: 640 px for the cover and the grid, and a square crop
// of 168 px for the rows of lists. Lists and the grid never load the full file.
package recipephoto

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/hejmo-home/hejmo/internal/picture"
)

// The sizes in pixels.
const (
	FullSide   = picture.RecipeMaxSide // the longest side of the full photo
	MediumSide = 640                   // the longest side of the cover and grid size
	SmallSide  = 168                   // the side of the square for list rows (56 px at 3x)
)

// Size names one of the three files of a photo.
type Size int

// The files of a photo.
const (
	Full Size = iota
	Medium
	Small
)

// ErrHEIC says the file is a HEIC photo. The browser's script turns HEIC into JPEG before it sends the file; the
// box has no HEIC decoder (ADR 0004).
var ErrHEIC = errors.New("recipephoto: HEIC photos are converted in the browser")

// Set is a prepared photo: its three JPEG files and the size of the full one.
type Set struct {
	Full, Medium, Small []byte
	Width, Height       int
}

// Prepare reads one uploaded photo of at most picture.MaxBytes. The errors are those of package picture,
// and ErrHEIC.
func Prepare(ctx context.Context, r io.Reader) (Set, error) {
	data, err := io.ReadAll(io.LimitReader(r, picture.MaxBytes+1))
	if err != nil {
		return Set{}, err
	}
	if len(data) <= picture.MaxBytes && isHEIC(data) {
		return Set{}, ErrHEIC
	}
	full, err := picture.ProcessMax(ctx, bytes.NewReader(data), FullSide)
	if err != nil {
		return Set{}, err
	}
	medium, err := picture.ProcessMax(ctx, bytes.NewReader(full.JPEG), MediumSide)
	if err != nil {
		return Set{}, err
	}
	small, err := picture.ProcessSquare(ctx, bytes.NewReader(full.JPEG), SmallSide)
	if err != nil {
		return Set{}, err
	}
	return Set{Full: full.JPEG, Medium: medium.JPEG, Small: small.JPEG, Width: full.ToW, Height: full.ToH}, nil
}

// isHEIC reports whether data starts like a HEIC or HEIF file.
func isHEIC(b []byte) bool {
	if len(b) < 12 || string(b[4:8]) != "ftyp" {
		return false
	}
	switch string(b[8:12]) {
	case "heic", "heix", "hevc", "hevx", "heim", "heis", "hevm", "hevs", "mif1", "msf1", "heif":
		return true
	}
	return false
}

func scaled(w, h, side int) (int, int) {
	if w <= side && h <= side {
		return w, h
	}
	if w >= h {
		return side, max(1, (h*side+w/2)/w)
	}
	return max(1, (w*side+h/2)/h), side
}

// MediumSize is the size of the medium file of a photo whose full file is w by h.
func MediumSize(w, h int) (int, int) { return scaled(w, h, MediumSide) }

// SmallSize is the size of the small file: the square cut from the full photo, never scaled up.
func SmallSize(w, h int) (int, int) {
	s := min(w, h, SmallSide)
	return s, s
}

// Files keeps the photos on disk: <dir>/<id>.jpg, <id>.m.jpg and <id>.s.jpg.
type Files struct{ dir string }

// NewFiles returns the photo files in dir; the directory is made on first use.
func NewFiles(dir string) *Files { return &Files{dir: dir} }

func (f *Files) name(id string, size Size) (string, error) {
	if id == "" || id == "." || id == ".." || strings.ContainsAny(id, `/\`) {
		return "", fmt.Errorf("recipephoto: bad id %q", id)
	}
	return filepath.Join(f.dir, id+[]string{".jpg", ".m.jpg", ".s.jpg"}[size]), nil
}

// Put writes the three files of a photo; a half-written file is never visible.
func (f *Files) Put(id string, s Set) error {
	if err := os.MkdirAll(f.dir, 0o755); err != nil {
		return err
	}
	for _, c := range []struct {
		size Size
		data []byte
	}{{Full, s.Full}, {Medium, s.Medium}, {Small, s.Small}} {
		name, err := f.name(id, c.size)
		if err != nil {
			return err
		}
		if err := writeAtomic(name, c.data); err != nil {
			return err
		}
	}
	return nil
}

func writeAtomic(name string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(name), ".tmp-*")
	if err != nil {
		return err
	}
	_, err = tmp.Write(data)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp.Name(), name)
	}
	if err != nil {
		os.Remove(tmp.Name())
	}
	return err
}

// Open opens one file of a photo.
func (f *Files) Open(id string, size Size) (*os.File, error) {
	name, err := f.name(id, size)
	if err != nil {
		return nil, err
	}
	return os.Open(name)
}

// Remove deletes the three files of a photo; a missing file is not an error.
func (f *Files) Remove(id string) error {
	for _, size := range []Size{Full, Medium, Small} {
		name, err := f.name(id, size)
		if err != nil {
			return err
		}
		if err := os.Remove(name); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	return nil
}
