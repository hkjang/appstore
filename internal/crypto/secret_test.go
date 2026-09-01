package crypto

import (
	"bytes"
	"testing"
)

func TestSecretBoxRoundTrip(t *testing.T) {
	box, err := NewSecretBox("01234567890123456789012345678901")
	if err != nil {
		t.Fatal(err)
	}
	encrypted, err := box.Encrypt("top-secret")
	if err != nil {
		t.Fatal(err)
	}
	if encrypted == "top-secret" {
		t.Fatal("ciphertext exposed plaintext")
	}
	plain, err := box.Decrypt(encrypted)
	if err != nil {
		t.Fatal(err)
	}
	if plain != "top-secret" {
		t.Fatalf("plain = %q", plain)
	}
}

func TestDigestIsStableAndKeyed(t *testing.T) {
	one, _ := NewSecretBox("01234567890123456789012345678901")
	two, _ := NewSecretBox("abcdefghijklmnopqrstuvwxyzABCDEF")
	if !bytes.Equal(one.Digest("token"), one.Digest("token")) {
		t.Fatal("digest is not deterministic")
	}
	if bytes.Equal(one.Digest("token"), two.Digest("token")) {
		t.Fatal("digest is not keyed")
	}
}
