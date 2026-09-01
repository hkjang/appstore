package config

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

const ListenAddress = ":8080"

// Config deliberately exposes exactly four environment-backed settings. All
// mutable operational settings live in PostgreSQL and are edited in Admin.
type Config struct {
	PostgresDSN            string
	BootstrapAdmin         string
	BootstrapAdminPassword string
	EncryptionKey          string
	ListenAddress          string
}

func Load() (Config, error) {
	cfg := Config{
		PostgresDSN:            strings.TrimSpace(os.Getenv("POSTGRES_DSN")),
		BootstrapAdmin:         strings.TrimSpace(os.Getenv("BOOTSTRAP_ADMIN")),
		BootstrapAdminPassword: os.Getenv("BOOTSTRAP_ADMIN_PASSWORD"),
		EncryptionKey:          os.Getenv("ENCRYPTION_KEY"),
		ListenAddress:          ListenAddress,
	}

	var missing []string
	if cfg.PostgresDSN == "" {
		missing = append(missing, "POSTGRES_DSN")
	}
	if cfg.BootstrapAdmin == "" {
		missing = append(missing, "BOOTSTRAP_ADMIN")
	}
	if cfg.BootstrapAdminPassword == "" {
		missing = append(missing, "BOOTSTRAP_ADMIN_PASSWORD")
	}
	if cfg.EncryptionKey == "" {
		missing = append(missing, "ENCRYPTION_KEY")
	}
	if len(missing) > 0 {
		return Config{}, fmt.Errorf("missing required environment variables: %s", strings.Join(missing, ", "))
	}
	if len(cfg.BootstrapAdminPassword) < 12 {
		return Config{}, errors.New("BOOTSTRAP_ADMIN_PASSWORD must be at least 12 characters")
	}
	return cfg, nil
}
