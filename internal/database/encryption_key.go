package database

import (
	"context"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"

	appcrypto "github.com/hkjang/appstore/internal/crypto"
	"github.com/jackc/pgx/v5/pgxpool"
)

const encryptionKeyAdvisoryLockID int64 = 0x61707073656e636b
const encryptionKeyFingerprintLabel = "appstore:encryption-key:fingerprint:v1"

// VerifyEncryptionKey records a keyed fingerprint on first initialization and
// fails closed when a later process starts with a different master key. The
// master key itself and any reversible derivative are never stored.
func VerifyEncryptionKey(ctx context.Context, pool *pgxpool.Pool, key string) error {
	fingerprint, err := encryptionKeyFingerprint(key)
	if err != nil {
		return err
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin encryption key verification: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, encryptionKeyAdvisoryLockID); err != nil {
		return fmt.Errorf("lock encryption key verification: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO system_settings(key, value)
		VALUES ('encryption_key_fingerprint', jsonb_build_object('digest', $1::text))
		ON CONFLICT (key) DO NOTHING`, fingerprint); err != nil {
		return fmt.Errorf("store encryption key fingerprint: %w", err)
	}
	var stored string
	if err := tx.QueryRow(ctx, `
		SELECT value->>'digest' FROM system_settings
		WHERE key = 'encryption_key_fingerprint'`).Scan(&stored); err != nil {
		return fmt.Errorf("read encryption key fingerprint: %w", err)
	}
	if subtle.ConstantTimeCompare([]byte(stored), []byte(fingerprint)) != 1 {
		return errors.New("ENCRYPTION_KEY does not match the initialized database")
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit encryption key verification: %w", err)
	}
	return nil
}

func encryptionKeyFingerprint(key string) (string, error) {
	box, err := appcrypto.NewSecretBox(key)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(box.Digest(encryptionKeyFingerprintLabel)), nil
}
