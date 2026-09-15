package appstore

import (
	"embed"
	"io/fs"
)

//go:embed guides
var bundledGuideFiles embed.FS

// BundledGuides holds one directory per app slug, each carrying the user and
// administrator guides collected for that service. The server attaches them
// to the catalog entry of the same slug (compared in lower case) on every start, so a new release
// ships the current manuals without anyone uploading a file. Refresh the
// directory with scripts/sync-guides.sh before a release.
var BundledGuides = mustSub(bundledGuideFiles, "guides")

func mustSub(files embed.FS, dir string) fs.FS {
	sub, err := fs.Sub(files, dir)
	if err != nil {
		panic(err)
	}
	return sub
}
