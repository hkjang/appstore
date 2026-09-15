package appstore

import (
	"io/fs"
	"regexp"
	"testing"

	"github.com/hkjang/appstore/internal/model"
	"github.com/hkjang/appstore/internal/store"
)

// slugPattern is what the seed's slugify produces, case aside: the sync lowers
// the directory name before matching, so DataWorks is fine but a space or a
// dot can never match an app.
var slugPattern = regexp.MustCompile(`(?i)^[a-z0-9]+(-[a-z0-9]+)*$`)

func TestBundledGuidesMatchWhatTheServerAccepts(t *testing.T) {
	entries, err := fs.ReadDir(BundledGuides, ".")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatal("no bundled guides; run scripts/sync-guides.sh")
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			t.Errorf("guides/%s: only per-slug directories belong at the top level", entry.Name())
			continue
		}
		if !slugPattern.MatchString(entry.Name()) {
			t.Errorf("guides/%s: directory name is not an app slug", entry.Name())
		}
		files, err := fs.ReadDir(BundledGuides, entry.Name())
		if err != nil {
			t.Fatal(err)
		}
		if len(files) == 0 || len(files) > store.MaxAppDocuments {
			t.Errorf("guides/%s: %d files, want 1..%d", entry.Name(), len(files), store.MaxAppDocuments)
		}
		for _, file := range files {
			if file.IsDir() {
				t.Errorf("guides/%s/%s: nested directories are not attached", entry.Name(), file.Name())
				continue
			}
			if _, ok := model.AppDocumentContentType(file.Name()); !ok {
				t.Errorf("guides/%s/%s: extension is not one a guide may carry", entry.Name(), file.Name())
			}
			info, err := file.Info()
			if err != nil {
				t.Fatal(err)
			}
			if info.Size() == 0 || info.Size() > model.MaxAppDocumentBytes {
				t.Errorf("guides/%s/%s: %d bytes, want 1..%d", entry.Name(), file.Name(), info.Size(), model.MaxAppDocumentBytes)
			}
		}
	}
}
