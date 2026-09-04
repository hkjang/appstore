package store

import "testing"

func TestBaselineRoleFollowsTheApprovalWorkflow(t *testing.T) {
	// With review in front of the catalogue, contributing is safe to grant to
	// everyone who signs in; without it a submission publishes immediately.
	if got := BaselineRole(true); got != ContributorRole {
		t.Fatalf("BaselineRole(true) = %q, want %q", got, ContributorRole)
	}
	if got := BaselineRole(false); got != DefaultUserRole {
		t.Fatalf("BaselineRole(false) = %q, want %q", got, DefaultUserRole)
	}
}
