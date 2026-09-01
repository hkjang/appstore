package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

const envelopePrefix = "enc:v1:"

type SecretBox struct {
	aead cipher.AEAD
	key  []byte
}

func NewSecretBox(value string) (*SecretBox, error) {
	key, err := parseKey(value)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create AES cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create AES-GCM: %w", err)
	}
	return &SecretBox{aead: aead, key: key}, nil
}

func parseKey(value string) ([]byte, error) {
	if len(value) == 32 {
		return []byte(value), nil
	}
	if len(value) == 64 {
		if decoded, err := hex.DecodeString(value); err == nil && len(decoded) == 32 {
			return decoded, nil
		}
	}
	for _, encoding := range []*base64.Encoding{
		base64.StdEncoding,
		base64.RawStdEncoding,
		base64.URLEncoding,
		base64.RawURLEncoding,
	} {
		if decoded, err := encoding.DecodeString(value); err == nil && len(decoded) == 32 {
			return decoded, nil
		}
	}
	return nil, errors.New("ENCRYPTION_KEY must be 32 raw bytes, 64 hexadecimal characters, or base64 encoding of 32 bytes")
}

func (s *SecretBox) Encrypt(plain string) (string, error) {
	if plain == "" {
		return "", nil
	}
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("generate encryption nonce: %w", err)
	}
	sealed := s.aead.Seal(nonce, nonce, []byte(plain), []byte(envelopePrefix))
	return envelopePrefix + base64.RawURLEncoding.EncodeToString(sealed), nil
}

func (s *SecretBox) Decrypt(envelope string) (string, error) {
	if envelope == "" {
		return "", nil
	}
	if !strings.HasPrefix(envelope, envelopePrefix) {
		return "", errors.New("unsupported encrypted value format")
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(envelope, envelopePrefix))
	if err != nil {
		return "", errors.New("invalid encrypted value encoding")
	}
	if len(raw) < s.aead.NonceSize() {
		return "", errors.New("encrypted value is truncated")
	}
	nonce, ciphertext := raw[:s.aead.NonceSize()], raw[s.aead.NonceSize():]
	plain, err := s.aead.Open(nil, nonce, ciphertext, []byte(envelopePrefix))
	if err != nil {
		return "", errors.New("encrypted value authentication failed")
	}
	return string(plain), nil
}

// Digest returns a keyed, deterministic digest suitable for opaque, random
// bearer tokens. The original token is never persisted.
func (s *SecretBox) Digest(value string) []byte {
	mac := hmac.New(sha256.New, s.key)
	_, _ = mac.Write([]byte(value))
	return mac.Sum(nil)
}

func RandomToken(bytes int) (string, error) {
	value := make([]byte, bytes)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func Mask(value string) string {
	if value == "" {
		return ""
	}
	prefix := value
	if len(prefix) > 7 {
		prefix = prefix[:7]
	}
	return prefix + "************"
}
