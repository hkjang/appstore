package httpapi

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/hkjang/appstore/internal/model"
	"github.com/hkjang/appstore/internal/seccheck"
)

func bindingApp() model.App {
	return model.App{
		ID: uuid.MustParse("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"), Name: "Agent Hub", Slug: "agent-hub",
		Summary: "요약", Description: "설명", Icon: "AH", ServiceURL: "https://agent.example.internal",
		CategoryID: uuid.MustParse("11111111-1111-4111-8111-111111111111"), Tags: []string{"AI"},
		Screenshots: []string{}, Version: "1.0.0", Visibility: "public",
	}
}

// The binding is the whole proof: an owner must not be able to produce a
// matching block for an app they are not registering, and it has to survive
// SecCheck's description field intact.
func TestAppSecurityBindingIsOneWholeBlock(t *testing.T) {
	nonce := uuid.MustParse("bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb")
	binding := appSecurityBinding(bindingApp(), nonce)
	if !strings.HasPrefix(binding, seccheck.BindingStart+"\n") || !strings.HasSuffix(binding, "\n"+seccheck.BindingEnd) {
		t.Fatalf("binding = %q", binding)
	}
	for _, want := range []string{
		"app_id=aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
		"challenge=bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb",
		`name="Agent Hub"`,
		`slug="agent-hub"`,
		`service_url="https://agent.example.internal"`,
		"content_sha256=",
	} {
		if !strings.Contains(binding, want) {
			t.Errorf("binding is missing %q:\n%s", want, binding)
		}
	}
	if strings.Contains(binding, "\r") {
		t.Error("the binding carries a carriage return")
	}
}

func TestAppSecurityBindingChangesWithTheAppAndTheChallenge(t *testing.T) {
	first := uuid.MustParse("bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb")
	second := uuid.MustParse("cccccccc-cccc-4ccc-8ccc-cccccccccccc")
	base := appSecurityBinding(bindingApp(), first)
	if appSecurityBinding(bindingApp(), second) == base {
		t.Error("a new challenge produced the same binding")
	}
	edited := bindingApp()
	edited.ServiceURL = "https://elsewhere.example.internal"
	if appSecurityBinding(edited, first) == base {
		t.Error("a changed service URL produced the same binding")
	}
	retagged := bindingApp()
	retagged.Tags = []string{"AI", "Agent"}
	if appSecurityBinding(retagged, first) == base {
		t.Error("changed tags produced the same binding")
	}
	// The same app and challenge always describe themselves the same way, or an
	// owner could never paste a stable block into SecCheck.
	if appSecurityBinding(bindingApp(), first) != base {
		t.Error("the binding is not stable for unchanged content")
	}
}

// A reader has to be told whether to fix the review, wait, or call the
// administrator, so each reason lands on its own status.
func TestSecurityCheckErrorMapsReasonsToStatuses(t *testing.T) {
	cases := map[seccheck.Reason]int{
		seccheck.ReasonBindingMismatch: http.StatusConflict,
		seccheck.ReasonServiceMismatch: http.StatusConflict,
		seccheck.ReasonNotApproved:     http.StatusConflict,
		seccheck.ReasonIncomplete:      http.StatusConflict,
		seccheck.ReasonNotFound:        http.StatusUnprocessableEntity,
		seccheck.ReasonInvalidReviewID: http.StatusUnprocessableEntity,
		seccheck.ReasonUnauthorized:    http.StatusBadGateway,
		seccheck.ReasonForbidden:       http.StatusBadGateway,
		seccheck.ReasonTimeout:         http.StatusBadGateway,
		seccheck.ReasonUnavailable:     http.StatusBadGateway,
	}
	for reason, want := range cases {
		err := securityCheckError(&seccheck.Error{Reason: reason})
		var api *APIError
		if !errors.As(err, &api) {
			t.Fatalf("%s did not produce an API error", reason)
		}
		if api.Status != want {
			t.Errorf("%s status = %d, want %d", reason, api.Status, want)
		}
		if api.Message == "" {
			t.Errorf("%s carries no message", reason)
		}
	}
}

func TestPublicSecurityCheckSettingsNeverCarryTheCredential(t *testing.T) {
	public := publicSecurityCheckSettings(model.SecurityCheckSettings{
		Enabled: true, BaseURL: "https://sec.example", APIKeyEncrypted: "ciphertext", TimeoutSeconds: 10,
	})
	if public.APIKeyEncrypted != "" {
		t.Fatal("the stored credential was about to be serialized")
	}
	if !public.APIKeySet {
		t.Fatal("the screen cannot tell that a key is stored")
	}
}
