package appstore

import _ "embed"

// DefaultAppsJSON contains the curated catalog used only to seed an empty
// installation. Runtime edits always win over later application restarts.
//
//go:embed apps_enriched.json
var DefaultAppsJSON []byte
