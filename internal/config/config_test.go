package config

import "testing"

func TestLoadRequiresExactlyDocumentedValues(t *testing.T) {
	t.Setenv("POSTGRES_DSN", "postgres://app:secret@db/appstore")
	t.Setenv("BOOTSTRAP_ADMIN", "admin")
	t.Setenv("BOOTSTRAP_ADMIN_PASSWORD", "correct-horse-battery-staple")
	t.Setenv("ENCRYPTION_KEY", "01234567890123456789012345678901")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.ListenAddress != ":8080" {
		t.Fatalf("ListenAddress = %q", cfg.ListenAddress)
	}
}

func TestLoadRejectsShortBootstrapPassword(t *testing.T) {
	t.Setenv("POSTGRES_DSN", "postgres://db/appstore")
	t.Setenv("BOOTSTRAP_ADMIN", "admin")
	t.Setenv("BOOTSTRAP_ADMIN_PASSWORD", "too-short")
	t.Setenv("ENCRYPTION_KEY", "01234567890123456789012345678901")
	if _, err := Load(); err == nil {
		t.Fatal("Load() expected an error")
	}
}
