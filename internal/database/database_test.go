package database

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	appstore "github.com/hkjang/appstore"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestOpenDoesNotLeakInvalidDSN(t *testing.T) {
	const secret = "do-not-leak-this-password"
	pool, err := Open(context.Background(), "postgres://appstore:"+secret+"@%zz/appstore")
	if pool != nil {
		pool.Close()
		t.Fatal("Open unexpectedly returned a pool")
	}
	if err == nil {
		t.Fatal("Open unexpectedly succeeded")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("error leaked DSN password: %v", err)
	}
}

func TestOpenDoesNotLeakPasswordOnPingFailure(t *testing.T) {
	const secret = "do-not-leak-ping-password"
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	pool, err := Open(ctx, "postgres://appstore:"+secret+"@127.0.0.1:1/appstore?sslmode=disable")
	if pool != nil {
		pool.Close()
		t.Fatal("Open unexpectedly returned a pool")
	}
	if err == nil {
		t.Fatal("Open unexpectedly succeeded")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("ping error leaked DSN password: %v", err)
	}
}

func TestEmbeddedMigrationsAreOrderedAndChecksummed(t *testing.T) {
	files, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no migrations loaded")
	}
	for i, file := range files {
		if file.Version == "" || file.Name == "" || len(file.Checksum) != 64 || strings.TrimSpace(file.SQL) == "" {
			t.Fatalf("invalid migration: %#v", file)
		}
		if i > 0 && files[i-1].Version >= file.Version {
			t.Fatalf("migrations not strictly ordered: %s >= %s", files[i-1].Version, file.Version)
		}
	}
}

func TestBundledSeedHas73UniqueAppsAndNoGitHubFields(t *testing.T) {
	apps, categories, err := decodeSeedApps(appstore.DefaultAppsJSON)
	if err != nil {
		t.Fatal(err)
	}
	if len(apps) != 73 {
		t.Fatalf("seed app count = %d, want 73", len(apps))
	}
	if len(categories) != 7 {
		t.Fatalf("seed category count = %d, want 7", len(categories))
	}
	seen := map[string]bool{}
	for _, app := range apps {
		slug := slugify(app.Name, app.ID)
		if seen[slug] {
			t.Fatalf("duplicate slug %q", slug)
		}
		seen[slug] = true
		encoded, err := json.Marshal(app)
		if err != nil {
			t.Fatal(err)
		}
		for _, forbidden := range []string{"html_url", "clone_url", "github"} {
			if strings.Contains(strings.ToLower(string(encoded)), forbidden) {
				t.Fatalf("seed representation contains forbidden field %q", forbidden)
			}
		}
	}
}

func TestSlugify(t *testing.T) {
	tests := map[string]string{
		"AgentHub":          "agenthub",
		"  vibe-coders  ":   "vibe-coders",
		"App / Store + API": "app-store-api",
		"한글 앱":              "한글-앱",
	}
	for input, want := range tests {
		if got := slugify(input, 99); got != want {
			t.Errorf("slugify(%q) = %q, want %q", input, got, want)
		}
	}
	if got := slugify("---", 42); got != "app-42" {
		t.Fatalf("fallback slug = %q", got)
	}
}

func TestEncryptionKeyFingerprintIsStableAndKeyed(t *testing.T) {
	one, err := encryptionKeyFingerprint("01234567890123456789012345678901")
	if err != nil {
		t.Fatal(err)
	}
	again, _ := encryptionKeyFingerprint("01234567890123456789012345678901")
	two, _ := encryptionKeyFingerprint("abcdefghijklmnopqrstuvwxyzABCDEF")
	if one != again {
		t.Fatal("fingerprint is not deterministic")
	}
	if one == two {
		t.Fatal("different keys produced the same fingerprint")
	}
	if strings.Contains(one, "0123456789") || len(one) != 64 {
		t.Fatalf("unexpected fingerprint representation %q", one)
	}
}

type fakeRow struct {
	values []any
	err    error
}

func (r fakeRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	if len(dest) != len(r.values) {
		return errors.New("unexpected scan destination count")
	}
	for i, value := range r.values {
		switch target := dest[i].(type) {
		case *uuid.UUID:
			*target = value.(uuid.UUID)
		default:
			return errors.New("unsupported fake scan target")
		}
	}
	return nil
}

type fakeBootstrapTx struct {
	existingID uuid.UUID
	createdID  uuid.UUID
	created    bool
}

func (tx *fakeBootstrapTx) Exec(_ context.Context, sql string, _ ...any) (pgconn.CommandTag, error) {
	if strings.Contains(sql, "INSERT INTO user_roles") {
		return pgconn.NewCommandTag("INSERT 0 1"), nil
	}
	return pgconn.NewCommandTag("SELECT 1"), nil
}

func (tx *fakeBootstrapTx) QueryRow(_ context.Context, sql string, _ ...any) pgx.Row {
	switch {
	case strings.Contains(sql, "SELECT id FROM users"):
		if tx.existingID == uuid.Nil {
			return fakeRow{err: pgx.ErrNoRows}
		}
		return fakeRow{values: []any{tx.existingID}}
	case strings.Contains(sql, "INSERT INTO users"):
		tx.created = true
		return fakeRow{values: []any{tx.createdID}}
	default:
		return fakeRow{err: errors.New("unexpected query")}
	}
}

func TestExistingBootstrapAdminIgnoresEnvironmentPassword(t *testing.T) {
	existing := uuid.New()
	tx := &fakeBootstrapTx{existingID: existing}
	hashCalls := 0
	got, err := ensureBootstrapAdmin(context.Background(), tx, "", "", func(string) (string, error) {
		hashCalls++
		return "must-not-be-used", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got != existing || hashCalls != 0 || tx.created {
		t.Fatalf("existing bootstrap was reset: id=%s hashCalls=%d created=%v", got, hashCalls, tx.created)
	}
}

func TestNewBootstrapAdminRequiresInitialCredentialsBeforeHashing(t *testing.T) {
	tx := &fakeBootstrapTx{createdID: uuid.New()}
	hashCalls := 0
	_, err := ensureBootstrapAdmin(context.Background(), tx, "", "short", func(string) (string, error) {
		hashCalls++
		return "must-not-be-used", nil
	})
	if err == nil || hashCalls != 0 || tx.created {
		t.Fatalf("invalid initial credential was consumed: err=%v hashCalls=%d created=%v", err, hashCalls, tx.created)
	}
}

func TestNewBootstrapAdminHashesOnce(t *testing.T) {
	created := uuid.New()
	tx := &fakeBootstrapTx{createdID: created}
	hashCalls := 0
	got, err := ensureBootstrapAdmin(context.Background(), tx, "admin", "correct-horse-battery-staple", func(password string) (string, error) {
		hashCalls++
		if password != "correct-horse-battery-staple" {
			t.Fatalf("unexpected password passed to hash function")
		}
		return "bcrypt-hash", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got != created || hashCalls != 1 || !tx.created {
		t.Fatalf("bootstrap was not created exactly once: id=%s hashCalls=%d created=%v", got, hashCalls, tx.created)
	}
}
