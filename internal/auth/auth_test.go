package auth

import (
	"testing"

	"github.com/hkjang/appstore/internal/model"
)

func TestSafeReturnTo(t *testing.T) {
	for _, unsafe := range []string{"", "https://evil.test", "//evil.test", "/ok\r\nLocation: evil"} {
		if got := SafeReturnTo(unsafe); got != "/" {
			t.Fatalf("SafeReturnTo(%q) = %q", unsafe, got)
		}
	}
	if got := SafeReturnTo("/submit?draft=1"); got != "/submit?draft=1" {
		t.Fatalf("valid return path = %q", got)
	}
}

func TestIdentityRoleMapping(t *testing.T) {
	claims := map[string]any{
		"sub": "123", "preferred_username": "hong",
		"realm_access": map[string]any{"roles": []any{"company-reviewers"}},
		"groups":       []any{"platform"},
	}
	settings := model.OIDCSettings{
		RoleClaimPath: "realm_access.roles", GroupClaimPath: "groups",
		RoleMappings:  map[string][]string{"company-reviewers": {"reviewer"}},
		GroupMappings: map[string][]string{"platform": {"contributor"}},
	}
	identity := identityFromClaims(claims, settings)
	if identity.Subject != "123" || len(identity.Roles) != 2 || identity.Team != "platform" {
		t.Fatalf("identity = %#v", identity)
	}
}

func TestPasswordHash(t *testing.T) {
	hash, err := HashPassword("correct-horse-battery-staple")
	if err != nil {
		t.Fatal(err)
	}
	if !CheckPassword(hash, "correct-horse-battery-staple") || CheckPassword(hash, "wrong") {
		t.Fatal("password verification mismatch")
	}
}
