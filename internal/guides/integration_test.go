package guides

import (
	"context"
	"os"
	"testing"
	"testing/fstest"
	"time"

	"github.com/hkjang/appstore/internal/config"
	"github.com/hkjang/appstore/internal/database"
	"github.com/hkjang/appstore/internal/store"
)

// TestPostgreSQLBundledGuideSync runs the sync against a seeded catalog: the
// seed carries an app with slug "dataworks", so the bundle below must land on
// it even when the directory is spelled DataWorks, survive a second pass
// untouched, and be replaced in place afterwards.
func TestPostgreSQLBundledGuideSync(t *testing.T) {
	dsn := os.Getenv("APPSTORE_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("APPSTORE_TEST_POSTGRES_DSN is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	pool, err := database.Initialize(ctx, config.Config{
		PostgresDSN:            dsn,
		BootstrapAdmin:         "bootstrap-admin",
		BootstrapAdminPassword: "initial-bootstrap-password",
		EncryptionKey:          "01234567890123456789012345678901",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	lock, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lock.Exec(ctx, `SELECT pg_advisory_lock($1)`, int64(0x41505053544f5245)); err != nil {
		lock.Release()
		t.Fatal(err)
	}
	defer func() {
		unlockCtx, unlockCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer unlockCancel()
		_, _ = lock.Exec(unlockCtx, `SELECT pg_advisory_unlock($1)`, int64(0x41505053544f5245))
		lock.Release()
	}()
	repository := store.New(pool)
	app, err := repository.GetAppBySlugFold(ctx, "dataworks")
	if err != nil {
		t.Fatal(err)
	}
	// The test owns these two names on the seeded app and removes them at the
	// end, so a previous aborted run cannot skew the counts either.
	cleanup := func() {
		documents, err := repository.ListAppDocuments(ctx, app.ID)
		if err != nil {
			t.Fatal(err)
		}
		for _, document := range documents {
			if document.FileName == "USER_GUIDE.pdf" || document.FileName == "ADMIN_GUIDE.pdf" {
				if err := repository.DeleteAppDocument(ctx, app.ID, document.ID); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	cleanup()
	defer cleanup()

	// The directory is spelled in mixed case on purpose: matching lowers both
	// sides, so DataWorks must reach the seeded app "dataworks".
	first := fstest.MapFS{
		"DataWorks/USER_GUIDE.pdf":   &fstest.MapFile{Data: []byte("%PDF-1.7 user v1")},
		"DataWorks/ADMIN_GUIDE.pdf":  &fstest.MapFile{Data: []byte("%PDF-1.7 admin")},
		"no-such-app/USER_GUIDE.pdf": &fstest.MapFile{Data: []byte("%PDF-1.7 orphan")},
	}
	summary, err := Sync(ctx, repository, first, quiet())
	if err != nil {
		t.Fatal(err)
	}
	if summary.Attached != 2 || summary.Unmatched != 1 || summary.Apps != 1 || summary.Failed != 0 {
		t.Fatalf("first pass summary = %+v", summary)
	}
	documents, err := repository.ListAppDocuments(ctx, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	var userGuide, adminGuide string
	var userGuideID = app.ID
	for _, document := range documents {
		switch document.FileName {
		case "USER_GUIDE.pdf":
			userGuide, userGuideID = document.Title, document.ID
		case "ADMIN_GUIDE.pdf":
			adminGuide = document.Title
		}
	}
	if userGuide != "사용자 가이드" || adminGuide != "관리자 가이드" {
		t.Fatalf("titles = %q / %q", userGuide, adminGuide)
	}

	summary, err = Sync(ctx, repository, first, quiet())
	if err != nil {
		t.Fatal(err)
	}
	if summary.Unchanged != 2 || summary.Attached != 0 || summary.Replaced != 0 {
		t.Fatalf("second pass summary = %+v", summary)
	}

	second := fstest.MapFS{
		"dataworks/USER_GUIDE.pdf":  &fstest.MapFile{Data: []byte("%PDF-1.7 user v2")},
		"dataworks/ADMIN_GUIDE.pdf": &fstest.MapFile{Data: []byte("%PDF-1.7 admin")},
	}
	summary, err = Sync(ctx, repository, second, quiet())
	if err != nil {
		t.Fatal(err)
	}
	if summary.Replaced != 1 || summary.Unchanged != 1 {
		t.Fatalf("third pass summary = %+v", summary)
	}
	document, content, err := repository.GetAppDocumentContent(ctx, app.ID, userGuideID)
	if err != nil {
		t.Fatalf("the replaced guide lost its identifier: %v", err)
	}
	if string(content) != "%PDF-1.7 user v2" || document.Size != len(content) {
		t.Fatalf("content after replacement = %q (size %d)", content, document.Size)
	}
}
