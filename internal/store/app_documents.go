package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/hkjang/appstore/internal/model"
)

// MaxAppDocuments bounds how many guide files one app may carry. A catalog
// entry is a landing page, not a document library, and the bytes live in the
// database where an unbounded list would be paid for by every backup.
const MaxAppDocuments = 10

const appDocumentColumns = `
	d.id, d.app_id, d.title, d.file_name, d.content_type, d.size, d.checksum,
	d.uploaded_by, COALESCE(NULLIF(u.display_name, ''), u.username, ''), d.created_at`

const appDocumentFrom = `
	FROM app_documents d
	LEFT JOIN users u ON u.id = d.uploaded_by`

func scanAppDocument(row rowScanner) (model.AppDocument, error) {
	var document model.AppDocument
	err := row.Scan(
		&document.ID, &document.AppID, &document.Title, &document.FileName,
		&document.ContentType, &document.Size, &document.Checksum,
		&document.UploadedBy, &document.UploaderName, &document.CreatedAt,
	)
	return document, err
}

// ListAppDocuments returns the metadata of every guide attached to an app,
// oldest first, without loading a single byte of content.
func (r *Repository) ListAppDocuments(ctx context.Context, appID uuid.UUID) ([]model.AppDocument, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+appDocumentColumns+appDocumentFrom+`
		WHERE d.app_id = $1 ORDER BY d.created_at, d.id`, appID)
	if err != nil {
		return nil, normalizeError("list app documents", err)
	}
	defer rows.Close()
	documents := []model.AppDocument{}
	for rows.Next() {
		document, err := scanAppDocument(rows)
		if err != nil {
			return nil, normalizeError("scan app document", err)
		}
		documents = append(documents, document)
	}
	if err := rows.Err(); err != nil {
		return nil, normalizeError("list app documents", err)
	}
	return documents, nil
}

// GetAppDocumentContent loads one guide with its bytes. The app is part of the
// lookup so a document identifier from one app cannot be read through another.
func (r *Repository) GetAppDocumentContent(ctx context.Context, appID, documentID uuid.UUID) (model.AppDocument, []byte, error) {
	var document model.AppDocument
	var content []byte
	err := r.pool.QueryRow(ctx, `
		SELECT `+appDocumentColumns+`, d.content`+appDocumentFrom+`
		WHERE d.app_id = $1 AND d.id = $2`, appID, documentID).Scan(
		&document.ID, &document.AppID, &document.Title, &document.FileName,
		&document.ContentType, &document.Size, &document.Checksum,
		&document.UploadedBy, &document.UploaderName, &document.CreatedAt, &content,
	)
	if err != nil {
		return model.AppDocument{}, nil, normalizeError("get app document", err)
	}
	return document, content, nil
}

type AppDocumentInput struct {
	Title       string
	FileName    string
	ContentType string
	Content     []byte
	UploadedBy  *uuid.UUID
}

// CreateAppDocument stores one guide file. It refuses an app that already
// carries MaxAppDocuments files and a name that is already taken, so repeated
// clicks on an upload button cannot quietly pile up duplicates.
func (r *Repository) CreateAppDocument(ctx context.Context, appID uuid.UUID, input AppDocumentInput) (model.AppDocument, error) {
	fileName := strings.TrimSpace(input.FileName)
	contentType := strings.TrimSpace(input.ContentType)
	if fileName == "" || contentType == "" || len(input.Content) == 0 {
		return model.AppDocument{}, fmt.Errorf("app document: %w", ErrInvalid)
	}
	var count int
	if err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM app_documents WHERE app_id = $1`, appID).Scan(&count); err != nil {
		return model.AppDocument{}, normalizeError("count app documents", err)
	}
	if count >= MaxAppDocuments {
		return model.AppDocument{}, fmt.Errorf("app document limit: %w", ErrConflict)
	}
	sum := sha256.Sum256(input.Content)
	var id uuid.UUID
	err := r.pool.QueryRow(ctx, `
		INSERT INTO app_documents(app_id, title, file_name, content_type, content, size, checksum, uploaded_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id`,
		appID, strings.TrimSpace(input.Title), fileName, contentType, input.Content,
		len(input.Content), hex.EncodeToString(sum[:]), input.UploadedBy).Scan(&id)
	if err != nil {
		return model.AppDocument{}, normalizeError("create app document", err)
	}
	document, err := scanAppDocument(r.pool.QueryRow(ctx,
		`SELECT `+appDocumentColumns+appDocumentFrom+` WHERE d.id = $1`, id))
	return document, normalizeError("read app document", err)
}

func (r *Repository) DeleteAppDocument(ctx context.Context, appID, documentID uuid.UUID) error {
	tag, err := r.pool.Exec(ctx,
		`DELETE FROM app_documents WHERE app_id = $1 AND id = $2`, appID, documentID)
	if err != nil {
		return normalizeError("delete app document", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("delete app document: %w", ErrNotFound)
	}
	return nil
}

// ReplaceAppDocument swaps the bytes of one guide in place, so a re-issued
// manual keeps its slot and a reader who bookmarked the download link gets the
// new file. The upload date moves to now: the list shows when the current
// bytes arrived, not when the name was first taken.
func (r *Repository) ReplaceAppDocument(ctx context.Context, appID, documentID uuid.UUID, input AppDocumentInput) (model.AppDocument, error) {
	fileName := strings.TrimSpace(input.FileName)
	contentType := strings.TrimSpace(input.ContentType)
	if fileName == "" || contentType == "" || len(input.Content) == 0 {
		return model.AppDocument{}, fmt.Errorf("app document: %w", ErrInvalid)
	}
	sum := sha256.Sum256(input.Content)
	tag, err := r.pool.Exec(ctx, `
		UPDATE app_documents
		SET title = $3, file_name = $4, content_type = $5, content = $6, size = $7,
		    checksum = $8, uploaded_by = $9, created_at = now()
		WHERE app_id = $1 AND id = $2`,
		appID, documentID, strings.TrimSpace(input.Title), fileName, contentType, input.Content,
		len(input.Content), hex.EncodeToString(sum[:]), input.UploadedBy)
	if err != nil {
		return model.AppDocument{}, normalizeError("replace app document", err)
	}
	if tag.RowsAffected() == 0 {
		return model.AppDocument{}, fmt.Errorf("replace app document: %w", ErrNotFound)
	}
	document, err := scanAppDocument(r.pool.QueryRow(ctx,
		`SELECT `+appDocumentColumns+appDocumentFrom+` WHERE d.id = $1`, documentID))
	return document, normalizeError("read app document", err)
}
