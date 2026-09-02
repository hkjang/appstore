package auth

import (
	"crypto/subtle"
	"net/http"
	"net/url"
	"strings"
	"time"

	appcrypto "github.com/hkjang/appstore/internal/crypto"
)

const (
	SessionCookieName = "appstore_session"
	CSRFCookieName    = "appstore_csrf"
	SessionLifetime   = 12 * time.Hour
)

type SessionMaterial struct {
	Token     string
	TokenHash []byte
	CSRF      string
	CSRFHash  []byte
	ExpiresAt time.Time
}

func NewSessionMaterial(box *appcrypto.SecretBox, now time.Time) (SessionMaterial, error) {
	token, err := appcrypto.RandomToken(32)
	if err != nil {
		return SessionMaterial{}, err
	}
	csrf, err := appcrypto.RandomToken(24)
	if err != nil {
		return SessionMaterial{}, err
	}
	return SessionMaterial{
		Token: token, TokenHash: box.Digest("session:" + token),
		CSRF: csrf, CSRFHash: box.Digest("csrf:" + csrf),
		ExpiresAt: now.Add(SessionLifetime),
	}, nil
}

func SetSessionCookies(w http.ResponseWriter, material SessionMaterial, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name: SessionCookieName, Value: material.Token, Path: "/", Expires: material.ExpiresAt,
		MaxAge: int(time.Until(material.ExpiresAt).Seconds()), HttpOnly: true, Secure: secure,
		SameSite: http.SameSiteLaxMode,
	})
	http.SetCookie(w, &http.Cookie{
		Name: CSRFCookieName, Value: material.CSRF, Path: "/", Expires: material.ExpiresAt,
		MaxAge: int(time.Until(material.ExpiresAt).Seconds()), HttpOnly: false, Secure: secure,
		SameSite: http.SameSiteStrictMode,
	})
}

func ClearSessionCookies(w http.ResponseWriter, secure bool) {
	for _, name := range []string{SessionCookieName, CSRFCookieName} {
		http.SetCookie(w, &http.Cookie{
			Name: name, Value: "", Path: "/", MaxAge: -1, Expires: time.Unix(1, 0),
			HttpOnly: name == SessionCookieName, Secure: secure, SameSite: http.SameSiteLaxMode,
		})
	}
}

func SessionToken(r *http.Request) string {
	cookie, err := r.Cookie(SessionCookieName)
	if err != nil {
		return ""
	}
	return cookie.Value
}

func VerifyCSRF(r *http.Request, expectedHash []byte, box *appcrypto.SecretBox) bool {
	header := r.Header.Get("X-CSRF-Token")
	cookie, err := r.Cookie(CSRFCookieName)
	if err != nil || header == "" || cookie.Value == "" {
		return false
	}
	if subtle.ConstantTimeCompare([]byte(header), []byte(cookie.Value)) != 1 {
		return false
	}
	actual := box.Digest("csrf:" + header)
	return subtle.ConstantTimeCompare(actual, expectedHash) == 1
}

// SafeReturnTo keeps the post-login redirect on this origin. Browsers normalize
// a backslash to a slash and strip tab, CR and LF from a URL before resolving
// it, so "/\evil.test" and "/<TAB>/evil.test" would otherwise reach the browser
// as the protocol-relative "//evil.test". Legitimate values arrive percent
// encoded, so rejecting backslashes and control characters costs nothing.
func SafeReturnTo(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || !strings.HasPrefix(value, "/") || strings.HasPrefix(value, "//") {
		return "/"
	}
	if strings.ContainsRune(value, '\\') || strings.IndexFunc(value, isControl) >= 0 {
		return "/"
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "" || parsed.Host != "" || parsed.User != nil {
		return "/"
	}
	return value
}

func isControl(value rune) bool {
	return value < 0x20 || value == 0x7f
}
