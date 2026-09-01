package migrations

import "embed"

// Files contains ordered PostgreSQL migrations.
//
//go:embed *.sql
var Files embed.FS
