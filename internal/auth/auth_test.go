package auth

import (
	"testing"

	"github.com/hkjang/appstore/internal/model"
)

func TestSafeReturnTo(t *testing.T) {
	unsafeValues := []string{
		"", "https://evil.test", "//evil.test", "/ok\r\nLocation: evil",
		`/\evil.test`, `\\evil.test`, `/apps\..\..`,
		"/\t/evil.test", "/\n/evil.test", "/\x7f/evil.test",
		"/\v/evil.test", "/\x00/evil.test",
	}
	for _, unsafe := range unsafeValues {
		if got := SafeReturnTo(unsafe); got != "/" {
			t.Fatalf("SafeReturnTo(%q) = %q", unsafe, got)
		}
	}
	for _, safe := range []string{"/submit?draft=1", "/apps/my-app", "/search?q=%2F%5C", "/apps/my-app#top"} {
		if got := SafeReturnTo(safe); got != safe {
			t.Fatalf("SafeReturnTo(%q) = %q", safe, got)
		}
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
