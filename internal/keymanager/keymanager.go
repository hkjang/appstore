package keymanager

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	appcrypto "github.com/hkjang/appstore/internal/crypto"
	"github.com/hkjang/appstore/internal/model"
)

const keyPrefix = "aps_"

type Generated struct {
	Plaintext   string
	Prefix      string
	Hash        []byte
	ExpiresAt   *time.Time
	Permissions []string
}

func Generate(box *appcrypto.SecretBox, policy model.KeyPolicy, permissions []string, allowed map[string]bool, now time.Time) (Generated, error) {
	if policy.MaxKeys < 1 || policy.MaxKeys > 100 {
		return Generated{}, errors.New("invalid key policy")
	}
	validated, err := ValidatePermissions(permissions, allowed)
	if err != nil {
		return Generated{}, err
	}
	random, err := appcrypto.RandomToken(32)
	if err != nil {
		return Generated{}, err
	}
	plain := keyPrefix + random
	prefix := plain
	if len(prefix) > 12 {
		prefix = prefix[:12]
	}
	var expiresAt *time.Time
	if policy.DefaultExpiryDays > 0 {
		value := now.Add(time.Duration(policy.DefaultExpiryDays) * 24 * time.Hour)
		expiresAt = &value
	}
	return Generated{
		Plaintext: plain, Prefix: prefix, Hash: box.Digest("apikey:" + plain),
		ExpiresAt: expiresAt, Permissions: validated,
	}, nil
}

func ValidatePermissions(requested []string, allowed map[string]bool) ([]string, error) {
	seen := make(map[string]bool, len(requested))
	result := make([]string, 0, len(requested))
	for _, permission := range requested {
		permission = strings.TrimSpace(permission)
		if permission == "" || seen[permission] {
			continue
		}
		if !allowed[permission] && !allowed["*"] {
			return nil, fmt.Errorf("permission %q is not available", permission)
		}
		seen[permission] = true
		result = append(result, permission)
	}
	if len(result) == 0 {
		return nil, errors.New("at least one permission is required")
	}
	sort.Strings(result)
	return result, nil
}

func Digest(box *appcrypto.SecretBox, plaintext string) ([]byte, error) {
	if !strings.HasPrefix(plaintext, keyPrefix) || len(plaintext) < 24 {
		return nil, errors.New("invalid AppStore API key")
	}
	return box.Digest("apikey:" + plaintext), nil
}

func RotationExpiry(policy model.KeyPolicy, now time.Time) time.Time {
	days := policy.RotationGraceDays
	if days < 0 {
		days = 0
	}
	return now.Add(time.Duration(days) * 24 * time.Hour)
}
