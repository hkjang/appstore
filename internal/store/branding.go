package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

// BrandingKinds are the images an administrator can replace.
const (
	BrandingLogo    = "logo"
	BrandingFavicon = "favicon"
)

type BrandingAsset struct {
	Kind        string    `json:"kind"`
	ContentType string    `json:"contentType"`
	Content     []byte    `json:"-"`
	Checksum    string    `json:"checksum"`
	Size        int       `json:"size"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

func ValidBrandingKind(kind string) bool {
	return kind == BrandingLogo || kind == BrandingFavicon
}

func (r *Repository) GetBrandingAsset(ctx context.Context, kind string) (BrandingAsset, error) {
	if !ValidBrandingKind(kind) {
		return BrandingAsset{}, fmt.Errorf("branding kind: %w", ErrInvalid)
	}
	var asset BrandingAsset
	err := r.pool.QueryRow(ctx, `
		SELECT kind, content_type, content, checksum, updated_at
		FROM branding_assets WHERE kind = $1`, kind).Scan(
		&asset.Kind, &asset.ContentType, &asset.Content, &asset.Checksum, &asset.UpdatedAt)
	if err != nil {
		return BrandingAsset{}, normalizeError("get branding asset", err)
	}
	asset.Size = len(asset.Content)
	return asset, nil
}

// ListBrandingChecksums reports which images are stored, without loading the
// bytes. The public config uses it to build cache-busting URLs.
func (r *Repository) ListBrandingChecksums(ctx context.Context) (map[string]string, error) {
	rows, err := r.pool.Query(ctx, `SELECT kind, checksum FROM branding_assets`)
	if err != nil {
		return nil, normalizeError("list branding assets", err)
	}
	defer rows.Close()
	result := map[string]string{}
	for rows.Next() {
		var kind, checksum string
		if err := rows.Scan(&kind, &checksum); err != nil {
			return nil, normalizeError("scan branding asset", err)
		}
		result[kind] = checksum
	}
	if err := rows.Err(); err != nil {
		return nil, normalizeError("list branding assets", err)
	}
	return result, nil
}

func (r *Repository) SaveBrandingAsset(ctx context.Context, kind, contentType string, content []byte) (BrandingAsset, error) {
	if !ValidBrandingKind(kind) {
		return BrandingAsset{}, fmt.Errorf("branding kind: %w", ErrInvalid)
	}
	contentType = strings.TrimSpace(contentType)
	if len(content) == 0 || contentType == "" {
		return BrandingAsset{}, fmt.Errorf("branding content: %w", ErrInvalid)
	}
	sum := sha256.Sum256(content)
	checksum := hex.EncodeToString(sum[:])
	if _, err := r.pool.Exec(ctx, `
		INSERT INTO branding_assets(kind, content_type, content, checksum, updated_at)
		VALUES ($1, $2, $3, $4, now())
		ON CONFLICT (kind) DO UPDATE SET
			content_type = EXCLUDED.content_type,
			content = EXCLUDED.content,
			checksum = EXCLUDED.checksum,
			updated_at = now()`, kind, contentType, content, checksum); err != nil {
		return BrandingAsset{}, normalizeError("save branding asset", err)
	}
	return r.GetBrandingAsset(ctx, kind)
}

func (r *Repository) DeleteBrandingAsset(ctx context.Context, kind string) error {
	if !ValidBrandingKind(kind) {
		return fmt.Errorf("branding kind: %w", ErrInvalid)
	}
	if _, err := r.pool.Exec(ctx, `DELETE FROM branding_assets WHERE kind = $1`, kind); err != nil {
		return normalizeError("delete branding asset", err)
	}
	return nil
}
