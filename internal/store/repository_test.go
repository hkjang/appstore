package store

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/hkjang/appstore/internal/model"
	"github.com/jackc/pgx/v5"
)

func TestNormalizeError(t *testing.T) {
	err := normalizeError("read", pgx.ErrNoRows)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v, want ErrNotFound", err)
	}
}

func TestNormalizePage(t *testing.T) {
	limit, offset := normalizePage(0, -3, 24)
	if limit != 24 || offset != 0 {
		t.Fatalf("got (%d,%d)", limit, offset)
	}
	limit, _ = normalizePage(1000, 0, 24)
	if limit != 200 {
		t.Fatalf("maximum limit = %d", limit)
	}
}

func TestUniqueStrings(t *testing.T) {
	got := uniqueStrings([]string{" apps:read ", "apps:read", "", "mcp:read"})
	if len(got) != 2 || got[0] != "apps:read" || got[1] != "mcp:read" {
		t.Fatalf("unique strings = %#v", got)
	}
}

func TestValidateAppInput(t *testing.T) {
	input := model.AppInput{
		Name: " App ", Slug: "My-App", Summary: "Summary", Description: "Description",
		CategoryID: uuid.NewString(), Visibility: "PUBLIC",
	}
	got, err := validateAppInput(input)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "App" || got.Slug != "my-app" || got.Icon != "📦" || got.Visibility != "public" {
		t.Fatalf("normalized app = %#v", got)
	}
	input.Description = ""
	if _, err := validateAppInput(input); !errors.Is(err, ErrInvalid) {
		t.Fatalf("missing description error = %v", err)
	}
}

func TestValidateAIProviderSupports256K(t *testing.T) {
	provider := model.AIProvider{
		Name: "Local", Kind: "openai_compatible", BaseURL: "http://ai.local/v1",
		DefaultModel: "model", ContextWindow: 262144, MaxInputTokens: 0,
		MaxOutputTokens: 262144, Temperature: 0.7, TimeoutSeconds: 120,
		Streaming: true,
	}
	if _, err := validateAIProvider(provider); err != nil {
		t.Fatalf("256K provider rejected: %v", err)
	}
	provider.MaxOutputTokens++
	if _, err := validateAIProvider(provider); !errors.Is(err, ErrInvalid) {
		t.Fatalf("oversized provider error = %v", err)
	}
	provider.MaxOutputTokens = 62145
	provider.MaxInputTokens = 200000
	if _, err := validateAIProvider(provider); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid token envelope error = %v", err)
	}
}

func TestValidateRoleInput(t *testing.T) {
	role, err := validateRoleInput(RoleInput{Key: " Custom-Role ", Name: "Custom", Permissions: []string{"apps:read", "apps:read"}})
	if err != nil {
		t.Fatal(err)
	}
	if role.Key != "custom-role" || len(role.Permissions) != 1 {
		t.Fatalf("role = %#v", role)
	}
	if _, err := validateRoleInput(RoleInput{Key: "!", Name: "bad"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid role error = %v", err)
	}
}

func TestNullableAuditJSON(t *testing.T) {
	if value, err := nullableJSON(nil); err != nil || value != nil {
		t.Fatalf("nil audit JSON = %#v, %v", value, err)
	}
	if _, err := nullableJSON(json.RawMessage(`{"ok":true}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := nullableJSON(json.RawMessage(`{broken`)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid audit JSON error = %v", err)
	}
}

func TestEncryptedSettingsCannotBeSerialized(t *testing.T) {
	for name, value := range map[string]any{
		"OIDC": model.OIDCSettings{ClientSecret: "enc:v1:oidc-ciphertext", ClientSecretSet: true},
		"AI":   model.AIProvider{APIKey: "enc:v1:ai-ciphertext", APIKeySet: true},
	} {
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatalf("marshal %s settings: %v", name, err)
		}
		if string(encoded) == "" || strings.Contains(string(encoded), "ciphertext") {
			t.Fatalf("%s settings leaked encrypted secret: %s", name, encoded)
		}
	}
}
