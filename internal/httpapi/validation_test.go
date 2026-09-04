package httpapi

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/hkjang/appstore/internal/model"
)

func TestValidateAppInput(t *testing.T) {
	input := model.AppInput{
		Name: " Catalog ", Slug: "My-App", Summary: "설명", Description: "상세 설명",
		ServiceURL: "https://apps.internal/catalog", CategoryID: uuid.NewString(), Visibility: "",
	}
	if err := ValidateAppInput(&input); err != nil {
		t.Fatal(err)
	}
	if input.Slug != "my-app" || input.Visibility != "public" || input.Name != "Catalog" {
		t.Fatalf("normalized input = %#v", input)
	}
}

func TestValidateAppInputRejectsRepositoryInsteadOfServiceURL(t *testing.T) {
	input := model.AppInput{Name: "A", Slug: "bad slug", Summary: "", Description: "", ServiceURL: "git@github.com:test/repo", CategoryID: "nope"}
	if err := ValidateAppInput(&input); err == nil {
		t.Fatal("expected validation error")
	}
}

func TestNormalizedSortLeavesTheDefaultToTheStore(t *testing.T) {
	// An unset or unknown sort must not become "updated" here: only the store
	// knows that a featured-only list defaults to the editorial order instead.
	for _, value := range []string{"", "  ", "bogus"} {
		if got := normalizedSort(value); got != "" {
			t.Fatalf("normalizedSort(%q) = %q, want empty", value, got)
		}
	}
	for _, value := range []string{"featured", "Featured", " published "} {
		want := strings.ToLower(strings.TrimSpace(value))
		if got := normalizedSort(value); got != want {
			t.Fatalf("normalizedSort(%q) = %q, want %q", value, got, want)
		}
	}
}
