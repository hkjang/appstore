package keymanager

import (
	"bytes"
	"testing"
	"time"

	appcrypto "github.com/hkjang/appstore/internal/crypto"
	"github.com/hkjang/appstore/internal/model"
)

func TestGenerateAndDigest(t *testing.T) {
	box, _ := appcrypto.NewSecretBox("01234567890123456789012345678901")
	now := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	generated, err := Generate(box, model.KeyPolicy{MaxKeys: 5, DefaultExpiryDays: 90}, []string{"mcp:read", "apps:read"}, map[string]bool{"mcp:read": true, "apps:read": true}, now)
	if err != nil {
		t.Fatal(err)
	}
	if generated.Plaintext[:4] != "aps_" || generated.Prefix == generated.Plaintext {
		t.Fatalf("generated = %#v", generated)
	}
	digest, err := Digest(box, generated.Plaintext)
	if err != nil || !bytes.Equal(digest, generated.Hash) {
		t.Fatal("generated digest mismatch")
	}
	if generated.ExpiresAt == nil || !generated.ExpiresAt.Equal(now.Add(90*24*time.Hour)) {
		t.Fatalf("expiresAt = %v", generated.ExpiresAt)
	}
}

func TestRejectsUnknownPermission(t *testing.T) {
	box, _ := appcrypto.NewSecretBox("01234567890123456789012345678901")
	_, err := Generate(box, model.KeyPolicy{MaxKeys: 5}, []string{"admin:*"}, map[string]bool{"apps:read": true}, time.Now())
	if err == nil {
		t.Fatal("expected permission error")
	}
}
