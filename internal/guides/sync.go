// Package guides attaches the manuals bundled with the binary to the catalog
// entries they describe. The bundle is one directory per app slug; an app is
// touched only when a directory whose name, lowered, equals its slug, lowered,
// exists, so a guide can never land on a neighbour with a similar name.
package guides

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"path"
	"strings"

	"github.com/google/uuid"
	"github.com/hkjang/appstore/internal/model"
	"github.com/hkjang/appstore/internal/store"
)

// Repository is the slice of the store the sync needs. It reads an app by its
// slug and manages that app's documents; it never creates or edits an app.
type Repository interface {
	GetAppBySlugFold(ctx context.Context, slug string) (model.App, error)
	ListAppDocuments(ctx context.Context, appID uuid.UUID) ([]model.AppDocument, error)
	CreateAppDocument(ctx context.Context, appID uuid.UUID, input store.AppDocumentInput) (model.AppDocument, error)
	ReplaceAppDocument(ctx context.Context, appID, documentID uuid.UUID, input store.AppDocumentInput) (model.AppDocument, error)
}

// Summary counts what one pass did, for the startup log.
type Summary struct {
	// Apps is the number of bundled directories that matched a catalog entry.
	Apps int
	// Unmatched is the number of bundled directories with no app of that slug.
	// They wait: once such an app is registered, the next start attaches them.
	Unmatched int
	Attached  int
	Replaced  int
	Unchanged int
	Failed    int
}

// titles names the two files every service ships. Anything else is shown by
// its file name without the extension, the same fallback an upload gets.
var titles = map[string]string{
	"USER_GUIDE":  "사용자 가이드",
	"ADMIN_GUIDE": "관리자 가이드",
}

// Sync walks the bundle and makes each matching app carry exactly the bundled
// bytes: a missing file is attached, a changed one is replaced in place, an
// identical one is left alone. Files an owner uploaded under other names are
// never touched. The returned error is reserved for the store failing; a
// single bad file is logged, counted and skipped.
func Sync(ctx context.Context, repository Repository, bundle fs.FS, logger *slog.Logger) (Summary, error) {
	var summary Summary
	entries, err := fs.ReadDir(bundle, ".")
	if err != nil {
		return summary, fmt.Errorf("read bundled guides: %w", err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		// Case is not identity: DataWorks and dataworks are the same app.
		app, err := repository.GetAppBySlugFold(ctx, strings.ToLower(entry.Name()))
		if errors.Is(err, store.ErrNotFound) {
			summary.Unmatched++
			continue
		}
		if err != nil {
			return summary, fmt.Errorf("look up app %q: %w", entry.Name(), err)
		}
		summary.Apps++
		if err := syncApp(ctx, repository, bundle, entry.Name(), app, &summary, logger); err != nil {
			return summary, err
		}
	}
	return summary, nil
}

func syncApp(ctx context.Context, repository Repository, bundle fs.FS, dir string, app model.App, summary *Summary, logger *slog.Logger) error {
	existing, err := repository.ListAppDocuments(ctx, app.ID)
	if err != nil {
		return fmt.Errorf("list documents of %q: %w", app.Slug, err)
	}
	files, err := fs.ReadDir(bundle, dir)
	if err != nil {
		return fmt.Errorf("read bundled guides of %q: %w", app.Slug, err)
	}
	for _, file := range files {
		if file.IsDir() {
			continue
		}
		input, ok := readBundled(bundle, dir, file.Name(), logger)
		if !ok {
			summary.Failed++
			continue
		}
		sum := sha256.Sum256(input.Content)
		checksum := hex.EncodeToString(sum[:])
		current, found := findByName(existing, input.FileName)
		switch {
		case found && current.Checksum == checksum:
			summary.Unchanged++
		case found:
			if _, err := repository.ReplaceAppDocument(ctx, app.ID, current.ID, input); err != nil {
				if isStoreRejection(err) {
					logger.Warn("bundled guide not replaced", "app", app.Slug, "file", input.FileName, "error", err)
					summary.Failed++
					continue
				}
				return fmt.Errorf("replace %s of %q: %w", input.FileName, app.Slug, err)
			}
			summary.Replaced++
		default:
			if _, err := repository.CreateAppDocument(ctx, app.ID, input); err != nil {
				if isStoreRejection(err) {
					// The owner already filled the app's ten slots, or a
					// concurrent replica attached the same file first.
					logger.Warn("bundled guide not attached", "app", app.Slug, "file", input.FileName, "error", err)
					summary.Failed++
					continue
				}
				return fmt.Errorf("attach %s to %q: %w", input.FileName, app.Slug, err)
			}
			summary.Attached++
		}
	}
	return nil
}

// readBundled loads one file and applies the upload rules: a known extension
// and a size inside the limit. Nothing here can fail because of the database,
// so a refusal is a bundle mistake worth a log line, not a reason to stop.
func readBundled(bundle fs.FS, dir, name string, logger *slog.Logger) (store.AppDocumentInput, bool) {
	contentType, ok := model.AppDocumentContentType(name)
	if !ok {
		logger.Warn("bundled guide has an extension a guide may not carry", "app", dir, "file", name)
		return store.AppDocumentInput{}, false
	}
	content, err := fs.ReadFile(bundle, path.Join(dir, name))
	if err != nil {
		logger.Warn("bundled guide could not be read", "app", dir, "file", name, "error", err)
		return store.AppDocumentInput{}, false
	}
	if len(content) == 0 || len(content) > model.MaxAppDocumentBytes {
		logger.Warn("bundled guide is empty or over the size limit", "app", dir, "file", name, "bytes", len(content))
		return store.AppDocumentInput{}, false
	}
	return store.AppDocumentInput{
		Title:       Title(name),
		FileName:    name,
		ContentType: contentType,
		Content:     content,
	}, true
}

// Title is what the app detail page shows for a bundled file.
func Title(fileName string) string {
	base := strings.TrimSuffix(fileName, path.Ext(fileName))
	if title, ok := titles[strings.ToUpper(base)]; ok {
		return title
	}
	return base
}

// findByName matches the way the unique index does: one name per app,
// regardless of case.
func findByName(documents []model.AppDocument, fileName string) (model.AppDocument, bool) {
	for _, document := range documents {
		if strings.EqualFold(document.FileName, fileName) {
			return document, true
		}
	}
	return model.AppDocument{}, false
}

func isStoreRejection(err error) bool {
	return errors.Is(err, store.ErrConflict) || errors.Is(err, store.ErrInvalid) || errors.Is(err, store.ErrNotFound)
}
