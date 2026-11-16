package server

import (
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/hejmo-home/hejmo/internal/photo"
	"github.com/hejmo-home/hejmo/internal/store"
)

// photoBox is where the box keeps photos (ticket 178): the folder the family
// puts photos in (<data>/photos, every subfolder an album) and the shrunk
// copies it keeps (<data>/photo-files). The originals are never kept.
type photoBox struct {
	folder string
	files  *photo.Files
	slots  chan struct{} // at most two photos are decoded at once: a large photo takes much memory

	mu      sync.Mutex // one folder scan at a time
	broken  map[string]string
	waiting map[string]int // folder ("" is the folder itself) -> photos that wait for space
	pending int            // photos of the folder not read yet
}

func newPhotoBox(dir string) *photoBox {
	return &photoBox{
		folder: filepath.Join(dir, "photos"), files: photo.NewFiles(filepath.Join(dir, "photo-files")),
		slots: make(chan struct{}, 2), broken: map[string]string{}, waiting: map[string]int{},
	}
}

// waitingFolder says how many photos of one folder wait for space.
type waitingFolder struct {
	Folder string
	Count  int
}

// folderState is what the last scan of the folder left open.
func (p *photoBox) folderState() (waiting []waitingFolder, pending int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for f, n := range p.waiting {
		waiting = append(waiting, waitingFolder{f, n})
	}
	sort.Slice(waiting, func(i, j int) bool { return waiting[i].Folder < waiting[j].Folder })
	return waiting, p.pending
}

// photoScanBudget is how long one visit of a page spends on reading new photos
// of the folder; the rest follows on the next visit and on the box's own rounds.
const photoScanBudget = 3 * time.Second

// syncPhotoFolder makes the albums and photos of the household match the photo
// folder: a subfolder is an album, a photo lying in the folder itself is in the
// default album, a photo that left the folder is forgotten. New photos are
// shrunk like uploads; when the space for photos is full they wait. Reading
// stops after budget; what is left follows at the next call.
func (s *server) syncPhotoFolder(h store.Household, budget time.Duration) {
	p := s.photos
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	scanned, err := photo.Scan(p.folder)
	if err != nil {
		s.logger.Error("scan photo folder", "err", err)
		return
	}
	def, err := s.db.EnsureDefaultAlbum(h.ID, s.i18n.T(s.i18n.Match(""), "photos.album.default"))
	if err != nil {
		s.logger.Error("default album", "err", err)
		return
	}
	albums := map[string]store.Album{"": def}
	for _, f := range scanned.Folders {
		a, err := s.db.EnsureFolderAlbum(h.ID, f)
		if err != nil {
			s.logger.Error("folder album", "folder", f, "err", err)
			return
		}
		albums[f] = a
	}
	known, err := s.db.FolderPhotos(h.ID)
	if err != nil {
		s.logger.Error("folder photos", "err", err)
		return
	}
	deadline := time.Now().Add(budget)
	waiting, pending, full := map[string]int{}, 0, false
	present := map[string]bool{}
	for _, src := range scanned.Photos {
		present[src.Path] = true
		old, isKnown := known[src.Path]
		if isKnown && old.SourceStamp == src.Stamp || p.broken[src.Path] == src.Stamp {
			continue
		}
		switch {
		case full && !isKnown:
			waiting[src.Folder]++
			continue
		case time.Now().After(deadline):
			pending++
			continue
		}
		res, err := p.read(src)
		if err != nil {
			p.broken[src.Path] = src.Stamp
			s.logger.Warn("photo in the folder is skipped", "path", src.Path, "err", err)
			continue
		}
		bytes := int64(len(res.Display) + len(res.Thumb))
		if isKnown { // the file changed: make the copy again
			if err := p.files.Put(old.ID, res.Display, res.Thumb); err == nil {
				err = s.db.UpdateFolderPhoto(h.ID, old.ID, bytes, res.Width, res.Height, src.Stamp)
				if err != nil {
					s.logger.Error("update folder photo", "err", err)
				}
			}
			continue
		}
		ph, err := s.db.AddPhoto(h.ID, store.Photo{AlbumID: albums[src.Folder].ID, Name: filepath.Base(src.Path), Bytes: bytes, Width: res.Width, Height: res.Height, SourcePath: src.Path, SourceStamp: src.Stamp})
		if err == store.ErrPhotosFull {
			full = true
			waiting[src.Folder]++
			continue
		}
		if err != nil {
			s.logger.Error("add folder photo", "path", src.Path, "err", err)
			continue
		}
		if err := p.files.Put(ph.ID, res.Display, res.Thumb); err != nil {
			s.logger.Error("store folder photo", "path", src.Path, "err", err)
			_, _ = s.db.RemoveFolderPhotos(h.ID, []string{ph.ID})
		}
	}
	var gone []string
	for path, k := range known {
		if !present[path] {
			gone = append(gone, k.ID)
		}
	}
	if removed, err := s.db.RemoveFolderPhotos(h.ID, gone); err != nil {
		s.logger.Error("forget folder photos", "err", err)
	} else {
		for _, id := range removed {
			_ = p.files.Remove(id)
		}
	}
	if err := s.db.RemoveFolderAlbumsNotIn(h.ID, scanned.Folders); err != nil {
		s.logger.Error("remove folder albums", "err", err)
	}
	p.waiting, p.pending = waiting, pending
}

// read decodes one photo of the folder.
func (p *photoBox) read(src photo.Source) (photo.Result, error) {
	f, err := os.Open(src.Abs)
	if err != nil {
		return photo.Result{}, err
	}
	defer f.Close()
	p.slots <- struct{}{}
	defer func() { <-p.slots }()
	return photo.Process(f)
}

// watchPhotoFolder looks at the folder every few minutes until the box stops.
func (s *server) watchPhotoFolder(done <-chan struct{}) {
	tick := time.NewTicker(5 * time.Minute)
	defer tick.Stop()
	for first := true; ; first = false {
		if !first {
			select {
			case <-done:
				return
			case <-tick.C:
			}
		}
		if s.db.Configured() {
			if h, err := s.db.HomeHousehold(); err == nil {
				s.syncPhotoFolder(h, 30*time.Second)
			}
		}
	}
}
