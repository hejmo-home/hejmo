package web_test

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"

	"github.com/hejmo-home/hejmo/web"
)

var conflictMarker = regexp.MustCompile(`(?m)^(<<<<<<< |=======$|>>>>>>> )`)

// web/static is embedded and served as it is: no editor or sed backup (a name
// ending in "-e" or "~") and no merge conflict marker may ship in it.
func TestStaticHoldsNoBackupsOrConflictMarkers(t *testing.T) {
	err := fs.WalkDir(web.Static, "static", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if strings.HasSuffix(path, "-e") || strings.HasSuffix(path, "~") || strings.HasSuffix(path, ".orig") {
			t.Errorf("%s looks like a backup file", path)
		}
		b, err := fs.ReadFile(web.Static, path)
		if err != nil {
			return err
		}
		if strings.HasSuffix(path, ".woff2") || strings.HasSuffix(path, ".png") {
			return nil
		}
		if conflictMarker.Match(b) {
			t.Errorf("%s contains a merge conflict marker", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
