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

func TestValidateAppInputBoundsTheSingleLineFields(t *testing.T) {
	// The request body allows two megabytes, so an unbounded label field lands
	// in the database whole and then renders on every shelf that lists the app.
	for _, testCase := range []struct {
		field  string
		mutate func(*model.AppInput)
	}{
		{"icon", func(i *model.AppInput) { i.Icon = strings.Repeat("🚀", maxIconRunes+1) }},
		{"gradient", func(i *model.AppInput) { i.Gradient = strings.Repeat("g", maxGradientRunes+1) }},
		{"language", func(i *model.AppInput) { i.Language = strings.Repeat("가", maxLabelRunes+1) }},
		{"framework", func(i *model.AppInput) { i.Framework = strings.Repeat("f", maxLabelRunes+1) }},
		{"team", func(i *model.AppInput) { i.Team = strings.Repeat("t", maxTeamRunes+1) }},
		{"version", func(i *model.AppInput) { i.Version = strings.Repeat("9", maxVersionRunes+1) }},
		{"serviceUrl", func(i *model.AppInput) {
			i.ServiceURL = "https://apps.internal/" + strings.Repeat("p", maxServiceURLRunes)
		}},
		{"name", func(i *model.AppInput) { i.Name = "Cata\nlog" }},
		{"team", func(i *model.AppInput) { i.Team = "플랫폼\t실" }},
		{"description", func(i *model.AppInput) { i.Description = "상세\x00설명" }},
	} {
		input := validAppInput()
		testCase.mutate(&input)
		err := ValidateAppInput(&input)
		apiError, ok := err.(*APIError)
		if !ok {
			t.Fatalf("%s: error = %v, want *APIError", testCase.field, err)
		}
		if _, reported := apiError.Details[testCase.field]; !reported {
			t.Fatalf("%s: details = %v, want the field reported", testCase.field, apiError.Details)
		}
	}
}

func TestValidateAppInputDropsUnusableTagsAndScreenshots(t *testing.T) {
	input := validAppInput()
	input.Tags = []string{" 검색 ", "with\nnewline", "검색", ""}
	input.Screenshots = []string{"https://apps.internal/a.png", "https://apps.internal/\x00.png"}
	if err := ValidateAppInput(&input); err != nil {
		t.Fatal(err)
	}
	if len(input.Tags) != 1 || input.Tags[0] != "검색" {
		t.Fatalf("tags = %#v", input.Tags)
	}
	if len(input.Screenshots) != 1 || input.Screenshots[0] != "https://apps.internal/a.png" {
		t.Fatalf("screenshots = %#v", input.Screenshots)
	}
}

func validAppInput() model.AppInput {
	return model.AppInput{
		Name: "Catalog", Slug: "catalog", Summary: "설명", Description: "상세 설명",
		ServiceURL: "https://apps.internal/catalog", CategoryID: uuid.NewString(),
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
