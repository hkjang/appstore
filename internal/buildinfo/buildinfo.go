// Package buildinfo exposes immutable metadata injected by the release build.
// It intentionally does not read environment variables so runtime configuration
// remains limited to the four settings declared by internal/config.
package buildinfo

import (
	"runtime"
	"strings"
)

// These values are replaced with -ldflags during a release image build.
var (
	Version   = "dev"
	Commit    = "unknown"
	BuildDate = "unknown"
)

// Info is safe to expose from the unauthenticated version endpoint and UI.
type Info struct {
	Version     string `json:"version"`
	Commit      string `json:"commit"`
	BuildDate   string `json:"buildDate"`
	GoVersion   string `json:"goVersion"`
	Environment string `json:"environment"`
}

// Current returns normalized build metadata. Local builds use the explicit
// "dev" environment while tagged builds use "production".
func Current() Info {
	version := strings.TrimSpace(Version)
	if version == "" {
		version = "dev"
	}
	commit := strings.TrimSpace(Commit)
	if commit == "" {
		commit = "unknown"
	}
	buildDate := strings.TrimSpace(BuildDate)
	if buildDate == "" {
		buildDate = "unknown"
	}
	environment := "production"
	if version == "dev" {
		environment = "development"
	}

	return Info{
		Version:     version,
		Commit:      commit,
		BuildDate:   buildDate,
		GoVersion:   runtime.Version(),
		Environment: environment,
	}
}
