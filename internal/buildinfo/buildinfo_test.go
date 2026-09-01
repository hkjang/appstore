package buildinfo

import "testing"

func TestCurrentNormalizesDevelopmentBuild(t *testing.T) {
	originalVersion, originalCommit, originalDate := Version, Commit, BuildDate
	t.Cleanup(func() {
		Version, Commit, BuildDate = originalVersion, originalCommit, originalDate
	})

	Version, Commit, BuildDate = "", "", ""
	got := Current()
	if got.Version != "dev" || got.Environment != "development" {
		t.Fatalf("unexpected development metadata: %+v", got)
	}
	if got.Commit != "unknown" || got.BuildDate != "unknown" || got.GoVersion == "" {
		t.Fatalf("missing normalized metadata: %+v", got)
	}
}

func TestCurrentReportsTaggedBuildAsProduction(t *testing.T) {
	originalVersion, originalCommit, originalDate := Version, Commit, BuildDate
	t.Cleanup(func() {
		Version, Commit, BuildDate = originalVersion, originalCommit, originalDate
	})

	Version = "v2.3.1"
	Commit = "4ab9102"
	BuildDate = "2026-09-01T08:00:00Z"
	got := Current()
	if got.Version != Version || got.Commit != Commit || got.BuildDate != BuildDate {
		t.Fatalf("release metadata changed: %+v", got)
	}
	if got.Environment != "production" {
		t.Fatalf("environment = %q, want production", got.Environment)
	}
}
