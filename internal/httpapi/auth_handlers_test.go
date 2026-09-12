package httpapi

import (
	"testing"

	"github.com/hkjang/appstore/internal/model"
)

func TestSilentLoginRequestedIsGrantedByTheSettingOnly(t *testing.T) {
	// Anyone can append ?prompt=none to the login address; only the
	// administrator setting may turn that into a silent attempt.
	for _, testCase := range []struct {
		name      string
		prompt    string
		autoLogin bool
		want      bool
	}{
		{"setting off, no prompt", "", false, false},
		{"setting off, prompt=none", "none", false, false},
		{"setting on, no prompt", "", true, false},
		{"setting on, prompt=none", "none", true, true},
		{"setting on, other prompt", "login", true, false},
	} {
		got := silentLoginRequested(testCase.prompt, model.OIDCSettings{AutoLogin: testCase.autoLogin})
		if got != testCase.want {
			t.Errorf("%s: silentLoginRequested = %v, want %v", testCase.name, got, testCase.want)
		}
	}
}

func TestSilentRefusalPathKeepsTheMarkerAndASafeReturnTo(t *testing.T) {
	for _, testCase := range []struct {
		returnTo string
		want     string
	}{
		{"", "/login?sso=none"},
		{"/", "/login?sso=none"},
		{"/my/apps?tab=drafts", "/login?sso=none&returnTo=%2Fmy%2Fapps%3Ftab%3Ddrafts"},
		// A refused attempt must not become a way out of this origin.
		{"//evil.example/phish", "/login?sso=none"},
		{"https://evil.example/", "/login?sso=none"},
	} {
		if got := silentRefusalPath(testCase.returnTo); got != testCase.want {
			t.Errorf("silentRefusalPath(%q) = %q, want %q", testCase.returnTo, got, testCase.want)
		}
	}
}
