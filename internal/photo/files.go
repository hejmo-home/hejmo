package photo

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// Files keeps the prepared photos on disk: <dir>/<id>.jpg and the thumbnail
// <dir>/<id>.t.jpg.
type Files struct{ dir string }

// NewFiles returns the photo files in dir; the directory is made on first use.
func NewFiles(dir string) *Files { return &Files{dir: dir} }

func (f *Files) name(id string, thumb bool) (string, error) {
	if id == "" || id == "." || id == ".." || strings.ContainsAny(id, `/\`) {
		return "", fmt.Errorf("photo: bad id %q", id)
	}
	if thumb {
		return filepath.Join(f.dir, id+".t.jpg"), nil
	}
	return filepath.Join(f.dir, id+".jpg"), nil
}

// Put writes a photo and its thumbnail; a half-written file is never visible.
func (f *Files) Put(id string, display, thumb []byte) error {
	if err := os.MkdirAll(f.dir, 0o755); err != nil {
		return err
	}
	for _, c := range []struct {
		thumb bool
		data  []byte
	}{{false, display}, {true, thumb}} {
		name, err := f.name(id, c.thumb)
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

// Open opens the photo, or its thumbnail.
func (f *Files) Open(id string, thumb bool) (*os.File, error) {
	name, err := f.name(id, thumb)
	if err != nil {
		return nil, err
	}
	return os.Open(name)
}

// Remove deletes the photo and its thumbnail; a missing file is not an error.
func (f *Files) Remove(id string) error {
	for _, thumb := range []bool{false, true} {
		name, err := f.name(id, thumb)
		if err != nil {
			return err
		}
		if err := os.Remove(name); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	return nil
}

// Source is a photo lying in the box's photo folder.
type Source struct {
	Path   string // relative to the folder, with slashes
	Folder string // the first subfolder it lies in, empty when it lies in the folder itself
	Stamp  string // size and modification time: it changes when the file does
	Abs    string // where to read it
}

// Scanned is what lies in the photo folder.
type Scanned struct {
	Photos  []Source
	Folders []string // the subfolders; each becomes an album
}

// Scan lists the JPEG and PNG files of the photo folder and its subfolders.
// Hidden files, symbolic links and other files are left out. A missing folder
// holds nothing.
func Scan(root string) (Scanned, error) {
	var out Scanned
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if p == root && errors.Is(err, fs.ErrNotExist) {
				return fs.SkipAll
			}
			return nil // an unreadable entry is left out
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		if rel == "." {
			return nil
		}
		if strings.HasPrefix(d.Name(), ".") {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			if !strings.Contains(rel, "/") {
				out.Folders = append(out.Folders, rel)
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		switch strings.ToLower(path.Ext(rel)) {
		case ".jpg", ".jpeg", ".png":
		default:
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		folder, _, nested := strings.Cut(rel, "/")
		if !nested {
			folder = ""
		}
		out.Photos = append(out.Photos, Source{Path: rel, Folder: folder, Stamp: fmt.Sprintf("%d:%d", info.Size(), info.ModTime().UnixNano()), Abs: p})
		return nil
	})
	if err != nil {
		return Scanned{}, err
	}
	sort.Strings(out.Folders)
	sort.Slice(out.Photos, func(i, j int) bool { return out.Photos[i].Path < out.Photos[j].Path })
	return out, nil
}
